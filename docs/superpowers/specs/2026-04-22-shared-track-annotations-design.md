# Shared Track Annotations — Design

**Date:** 2026-04-22
**Scope:** STARS only (ERAM deferred)
**Branch:** `shared-tcw-display` (continues, with a pivot)

## Context and Pivot

An earlier foundation slice (see `2026-04-21-shared-tcw-display-design.md` and `2026-04-21-shared-tcw-display-foundation.md`) synced three scope-view fields — `Range`, `UserCenter`, `RangeRingRadius` — across relief controllers at the same TCW. Manual testing on 2026-04-22 confirmed the push+poll+RPC plumbing works end-to-end.

In practice, syncing scope view is wrong: each controller naturally pans and zooms independently, and forcing one view on the other is noisy and "pretty easily abused." What genuinely needs to be shared between relief controllers is the **per-aircraft annotations** the controllers place on tracks — J-rings, ATPA cones, leader-line overrides, forced-datablock states, and per-ACID PTL/ATPA toggles.

This design **removes** the three synced scope-view fields and **replaces** them with a per-ACID, per-TCW annotation sync. The push+poll+RPC architecture stays; only the state being synced changes.

## Problem

Two relief controllers at the same TCW must each independently control the scope view (pan, zoom) but must share the track-level annotations they place on aircraft. If controller A drops a 5 nm J-ring on N427DK to mark separation intent, controller B must see that J-ring. If B flips on the ATPA monitor cone for DAL123, A must see it. Today each client stores these in local `TrackState` and they never cross the wire.

## Goals

- Per-aircraft display annotations placed at a TCW are visible to every controller at that TCW, via the existing relief flow.
- Personal view state (scope center, zoom, range-ring radius, brightness, fonts, pref-set library) stays strictly local — no sharing, no exceptions.
- Existing relief join UX is unchanged.
- Per-TCW annotation state persists for the life of the sim session, regardless of who is connected at any instant.

## Non-Goals

- Scope-view sync (center, range, rotation, range-ring radius, altitude filters, video maps, list positions). Explicitly out.
- Preference-set sharing (each controller keeps their own library and their own loaded set).
- Pointouts / rejected pointouts (these are already server-routed workflow state, wired through a different mechanism; not touched here).
- Datablock drag-to-reposition. The code has no per-ACID datablock offset field today. Adding drag behavior is a separate feature; the sync machinery designed here will accommodate it if/when added.
- ERAM.
- Command-authority typing lock (deferred; relief behavior unchanged).
- Cross-TCW or cross-sim state.
- Persisting annotations to disk across sim restarts.

## User Flow

1. Controller A signs in to TCW **N01** normally. No observable change from today.
2. The server lazily creates a `TCWDisplayState` for N01 on first signon; its `Annotations` map is empty until A places something.
3. Controller A places a 5 nm J-ring on DAL123. A mutation RPC fires; server applies, bumps `Rev`, echoes the updated snapshot on the RPC reply, and includes the snapshot in subsequent poll responses for every controller at N01.
4. Controller B joins as relief on N01 via the existing multi-controller UI. On first poll, B's client receives the current `Annotations` map and renders the J-ring on DAL123.
5. Either A or B can place/adjust/clear annotations; each mutation follows the same path.
6. When an aircraft leaves the track list, the server prunes its entry from `Annotations`.
7. B leaves; A sees no change. A leaves; annotations persist on the `Sim`. New arrivals pick up the current state.
8. Sim session ends → all `TCWDisplayState` entries discarded with the sim.

## Synced vs Unsynced

**Synced — per TCW, per ACID (`TrackAnnotations` struct, new):**

Sourced from fields currently in `stars.TrackState`:

- `JRingRadius` *(float32, nm)*
- `ConeLength` *(float32, nm — ATPA/TPA cone)*
- `LeaderLineDirection` *(`*math.CardinalOrdinalDirection`)*
- `FDAMLeaderLineDirection` *(`*math.CardinalOrdinalDirection`)*
- `UseGlobalLeaderLine` *(bool)*
- `DisplayFDB` *(bool — force full datablock)*
- `DisplayPTL` *(bool — per-ACID projected track line)*
- `DisplayTPASize` *(`*bool`)*
- `DisplayATPAMonitor` *(`*bool`)*
- `DisplayATPAWarnAlert` *(`*bool`)*
- `DisplayRequestedAltitude` *(`*bool`)*
- `DisplayLDBBeaconCode` *(bool)*

**Unsynced — explicitly local:**

- Everything in `stars.Preferences` (brightness, fonts, saved sets, scope view, CA/MSAW enables, ATPA brightness, quick-look, list positions, etc.).
- Remainder of `stars.TrackState` beyond the fields above — alert history, flashing timers, MSAW/SPC acknowledgement state, handoff display timers, etc. These are per-user alert/UX state and MUST stay local.
- `sp.PointOuts`, `sp.RejectedPointOuts`, `sp.ForceQLACIDs`, `sp.VFRFPFirstSeen` — these maps stay local for this slice. `PointOuts`/`RejectedPointOuts` are already server-routed workflow state through a different mechanism. `ForceQLACIDs` and `VFRFPFirstSeen` could be added in a follow-up if needed.

The exact synced-field list is codified in code in `stars/track_shared.go` (renamed from the prior `stars/shared_fields.go` which operated on `Preferences` — see Migration).

## Architecture

### Server side (reuses the proven foundation)

`sim/tcw_display.go` keeps `TCWDisplayState` but the `ScopeView` field is removed and a new `Annotations map[ACID]TrackAnnotations` is added:

```go
type TCWDisplayState struct {
    Annotations map[ACID]TrackAnnotations
    Rev         uint64
}

type TrackAnnotations struct {
    JRingRadius              float32
    ConeLength               float32
    LeaderLineDirection      *math.CardinalOrdinalDirection
    FDAMLeaderLineDirection  *math.CardinalOrdinalDirection
    UseGlobalLeaderLine      bool
    DisplayFDB               bool
    DisplayPTL               bool
    DisplayTPASize           *bool
    DisplayATPAMonitor       *bool
    DisplayATPAWarnAlert     *bool
    DisplayRequestedAltitude *bool
    DisplayLDBBeaconCode     bool
}
```

- One `TCWDisplayState` is lazily created on first signon (same as today).
- `Sim` owns `TCWDisplay map[TCW]*TCWDisplayState` (unchanged).
- Mutation helpers on `Sim`: one per synced field, each takes `(tcw TCW, acid ACID, value T)`, ensures the per-ACID `TrackAnnotations` entry exists, updates the field, bumps `Rev`.
- `Sim.GetStateUpdate(tcw)` populates `StateUpdate.TCWDisplay` with the current pointer (unchanged path).
- On each tick, `Sim` prunes entries in `Annotations` for ACIDs no longer in the track set.

### RPCs (`server/dispatcher.go`)

One per field, all taking `ControllerToken` + `ACID` + value. Matches the established pattern:

- `SetTrackJRingRadius(token, acid, radius float32)`
- `SetTrackConeLength(token, acid, length float32)`
- `SetTrackLeaderLineDirection(token, acid, dir *math.CardinalOrdinalDirection)`
- `SetTrackFDAMLeaderLineDirection(token, acid, dir *math.CardinalOrdinalDirection)`
- `SetTrackUseGlobalLeaderLine(token, acid, v bool)`
- `SetTrackDisplayFDB(token, acid, v bool)`
- `SetTrackDisplayPTL(token, acid, v bool)`
- `SetTrackDisplayTPASize(token, acid, v *bool)`
- `SetTrackDisplayATPAMonitor(token, acid, v *bool)`
- `SetTrackDisplayATPAWarnAlert(token, acid, v *bool)`
- `SetTrackDisplayRequestedAltitude(token, acid, v *bool)`
- `SetTrackDisplayLDBBeaconCode(token, acid, v bool)`

Each handler echoes a fresh `SimStateUpdate` so the caller's client mirror updates immediately.

`SetTCWRange`, `SetTCWUserCenter`, `SetTCWRangeRingRadius` are **removed**.

### Client side

- `client.State.TCWDisplay` already exists and flows through the poll; its shape changes (removing `ScopeView`, adding `Annotations`).
- `stars` read paths that currently hit `state.JRingRadius`, `state.ConeLength`, `state.DisplayFDB`, etc. are replaced with a helper:

  ```go
  func (sp *STARSPane) annotations(ctx, acid) TrackAnnotations
  ```

  which reads from `ctx.Client.State.TCWDisplay.Annotations[acid]` (falling back to zero-value if absent).
- `stars` write paths that currently set `state.JRingRadius = n` now call `ctx.Client.SetTrackJRingRadius(acid, n, callback)` and the local mirror updates via the RPC state-update echo.
- The local `TrackState` fields for synced items are no longer the source of truth. They are either deleted outright or kept as scratch and ignored by the renderer; deletion is preferred to avoid two-readers confusion (see plan).

### Delivery

Same hybrid as the foundation: RPC reply carries a fresh snapshot for the caller, and every 1 Hz poll carries the current `TCWDisplay` for the caller's TCW. Event-driven push is still deferred to a later slice.

## Migration

1. **Rip out the three scope-view RPCs and helpers.** Files touched:
   - `sim/tcw_display.go`: drop `ScopeView`, `ScopeViewState`, `SetTCWRange/UserCenter/RangeRingRadius` on `Sim`.
   - `server/dispatcher.go`: drop the three `SetTCW*` handlers.
   - `client/control.go`: drop the three wrappers.
   - `stars/stars.go`: drop `syncedRange/UserCenter/RangeRingRadius` and `mirrorTCWDisplayIntoPrefs`; restore direct reads of `ps.Range`, `ps.UserCenter`, `ps.RangeRingRadius`.
   - `stars/dcb.go`, `stars/commands.go`, `stars/fuzz.go`, `stars/lists.go`, `stars/tools.go`: restore direct `ps.*` reads/writes for the three fields.
   - `stars/shared_fields.go` + test: delete (the `Preferences`-level categorization no longer applies — nothing in `Preferences` is synced).
   - `stars/prefs.go` `mergeLoadedPreferences`: delete (not needed once nothing in `Preferences` is synced).
   - `sim/tcw_display_test.go`, `server/shared_tcw_integration_test.go`, `stars/shared_fields_test.go`: rewrite against the new design.

2. **Keep** `TCWDisplayState`, `Sim.EnsureTCWDisplay`, the `Sim.TCWDisplay` map, the `StateUpdate.TCWDisplay` field, and the RPC echo/poll delivery path. These are load-bearing and proven.

3. **Add** per-ACID annotation sync on top, per Architecture above.

## Error Handling

- **RPC mutation failure:** callback receives error; client renders an error dialog (pattern matches `SetTCWRange`'s existing callback).
- **Mutation referencing a vanished ACID:** server ensures the entry then mutates; pruning on tick eventually removes.
- **Reliever's client has stale annotations after reconnect:** next poll carries authoritative snapshot; client replaces wholesale.

## Testing

- **Unit (`sim/tcw_display_test.go`):** lazy seed; per-field mutation bumps `Rev`; pruning removes entries for absent ACIDs; snapshot carried in `StateUpdate`.
- **Unit (`stars/track_shared_test.go`):** reflective test walks the new `TrackAnnotations` struct and asserts every field is listed in the synced categorization (and that no listed field is missing from the struct). Mirrors the spirit of the deleted `shared_fields_test.go` but targets the new shape.
- **Integration (`server/shared_tcw_integration_test.go`):** two tokens at the same TCW. A sets J-ring on ACID X → B's next poll shows the same J-ring. B changes leader-line direction on ACID Y → A sees it. A disconnects/reconnects → annotations still present.
- **Manual:** dedicated server + primary + relief; place J-rings, toggle full datablock, change leader-line direction; verify each propagates to the other client.

## Open Items

- **ACID vs ADSBCallsign as the key.** `stars.TrackState` is keyed by `av.ADSBCallsign`, but `ForceQLACIDs`/`PointOuts` use `sim.ACID`, and controllers interact with aircraft by ACID. The spec uses ACID because that's the user-facing identifier and matches the existing shared-ish maps. Confirm during implementation that ACID resolves cleanly from UI context at every write site.
- **Coalescing.** Rapid leader-line drags could spam RPCs. For v1 we accept the RPC-per-change rate; if it becomes a problem, add client-side debounce (last-write-wins per field per ACID).
- **Follow-ups:** `ForceQLACIDs`, event-driven push, typing lock, datablock drag-offset field + sync.
