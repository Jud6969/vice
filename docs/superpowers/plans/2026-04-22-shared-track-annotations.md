# Shared Track Annotations — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the shipped scope-view sync (Range/UserCenter/RangeRingRadius) with per-aircraft track annotation sync across relief controllers at the same TCW. Reuse the proven push+poll+RPC architecture; only the synced state changes.

**Spec:** `docs/superpowers/specs/2026-04-22-shared-track-annotations-design.md`

**Branch:** continue on `shared-tcw-display` (not yet pushed upstream).

**Predecessor plan:** `docs/superpowers/plans/2026-04-21-shared-tcw-display-foundation.md` (superseded by this plan).

**Synced fields (all from `stars.TrackState`):**

- `JRingRadius`, `ConeLength`
- `LeaderLineDirection`, `FDAMLeaderLineDirection`, `UseGlobalLeaderLine`
- `DisplayFDB`, `DisplayPTL`
- `DisplayTPASize`, `DisplayATPAMonitor`, `DisplayATPAWarnAlert`
- `DisplayRequestedAltitude`, `DisplayLDBBeaconCode`

---

## File Structure

### Rip-out (Task 1)

- Modify: `sim/tcw_display.go` — delete `ScopeViewState`, `ScopeView`, `SetTCWRange/UserCenter/RangeRingRadius`.
- Modify: `server/dispatcher.go` — delete `SetTCWRange/UserCenter/RangeRingRadius` handlers + arg types + RPC name consts.
- Modify: `client/control.go` — delete the three client wrappers.
- Modify: `stars/stars.go` — delete `syncedRange/UserCenter/RangeRingRadius`, `mirrorTCWDisplayIntoPrefs`, the `Draw` call to it. Restore direct `ps.Range`/`ps.UserCenter`/`ps.RangeRingRadius` reads.
- Modify: `stars/dcb.go`, `stars/commands.go`, `stars/fuzz.go`, `stars/lists.go`, `stars/tools.go` — restore direct reads/writes against `ps`.
- Delete: `stars/shared_fields.go`, `stars/shared_fields_test.go`.
- Modify: `stars/prefs.go` — delete `mergeLoadedPreferences` (nothing in Preferences is synced anymore).
- Rewrite: `sim/tcw_display_test.go`, `server/shared_tcw_integration_test.go`.

### Build-up (Tasks 2–11)

- Modify: `sim/tcw_display.go` — add `TrackAnnotations` struct, `Annotations map[ACID]TrackAnnotations` on `TCWDisplayState`, `SetTrack*` methods on `Sim`, per-tick pruning helper.
- Create: `stars/track_shared.go` — authoritative list of synced `TrackAnnotations` field names; helper `(sp *STARSPane) annotations(ctx, acid)`.
- Create: `stars/track_shared_test.go` — reflective test.
- Modify: `server/dispatcher.go` — add 12 `SetTrack*` handlers.
- Modify: `client/control.go` — add 12 `SetTrack*` client wrappers.
- Modify: `sim/sim.go` — call annotation-pruning helper from tick loop.
- Modify: `stars/*.go` — route reads through `annotations(ctx, acid)` helper, writes through `ctx.Client.SetTrack*`.
- Modify: `server/shared_tcw_integration_test.go` — cover annotation sync + rejoin + prune.

---

## Task 1: Rip out scope-view sync

**Why:** Per-spec, scope view (range, pan, range-ring radius) no longer syncs. The replaced-by state (`TrackAnnotations`) is added in later tasks. Ripping first gives a clean baseline to build against and stops the wrong behavior immediately.

- [ ] **Step 1:** Delete from `sim/tcw_display.go`: `ScopeViewState` struct, `ScopeView` field on `TCWDisplayState`, `Sim.SetTCWRange`, `Sim.SetTCWUserCenter`, `Sim.SetTCWRangeRingRadius`. Keep `Sim.EnsureTCWDisplay`, `Sim.TCWDisplay`, the lazy-create pattern.
- [ ] **Step 2:** Remove the seed args from `Sim.SignOn`'s call to `EnsureTCWDisplay` in `sim/consolidation.go` (no seeds needed — the map is the seed).
- [ ] **Step 3:** Delete from `server/dispatcher.go`: the three RPC consts, arg types, and handlers (`SetTCWRange`, `SetTCWUserCenter`, `SetTCWRangeRingRadius`).
- [ ] **Step 4:** Delete from `client/control.go`: `ControlClient.SetTCWRange`, `ControlClient.SetTCWUserCenter`, `ControlClient.SetTCWRangeRingRadius`.
- [ ] **Step 5:** Delete from `stars/stars.go`: `syncedRange`, `syncedUserCenter`, `syncedRangeRingRadius`, `mirrorTCWDisplayIntoPrefs`; remove the `mirrorTCWDisplayIntoPrefs` call in `Draw`.
- [ ] **Step 6:** Restore `stars/dcb.go` spinner callbacks to read `ps.Range`/`ps.RangeRingRadius` directly and write via `ps.Range = v` (not via `ctx.Client.SetTCW*`).
- [ ] **Step 7:** Restore `stars/commands.go`, `stars/fuzz.go`, `stars/lists.go`, `stars/tools.go` call sites to direct `ps.*` reads.
- [ ] **Step 8:** Delete `stars/shared_fields.go` and `stars/shared_fields_test.go`. Remove the `mergeLoadedPreferences` function from `stars/prefs.go` and any callers.
- [ ] **Step 9:** Delete the existing `sim/tcw_display_test.go` (it targets the removed fields — will be rewritten in Task 3) and `server/shared_tcw_integration_test.go` (ditto — rewritten in Task 11).
- [ ] **Step 10:** Build: `go build ./...`. Expect clean. Run `go test ./sim/ ./server/ ./client/ ./stars/`. Expect clean; any remaining failures indicate leftover references to removed symbols.

---

## Task 2: Add `TrackAnnotations` struct and storage

**Why:** The new shared state shape. Pure type definition; no behavior yet.

- [ ] **Step 1:** In `sim/tcw_display.go`, define:

  ```go
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

- [ ] **Step 2:** Add `Annotations map[ACID]TrackAnnotations` to `TCWDisplayState`. Initialize in `EnsureTCWDisplay`.
- [ ] **Step 3:** Build: `go build ./sim/`. Expect clean.

---

## Task 3: `Sim` mutation helpers + tests

**Why:** TDD the per-field mutators. Each follows the same shape: acquire `s.mu`, ensure TCWDisplay exists, ensure per-ACID entry exists, mutate field, bump `Rev`.

- [ ] **Step 1:** In `sim/tcw_display.go`, add one method per synced field:

  ```go
  func (s *Sim) SetTrackJRingRadius(tcw TCW, acid ACID, v float32) {
      s.mu.Lock(s.lg)
      defer s.mu.Unlock(s.lg)
      d := s.ensureTCWDisplayLocked(tcw)
      entry := d.Annotations[acid]
      entry.JRingRadius = v
      d.Annotations[acid] = entry
      d.Rev++
  }
  ```

  And similar for each of the 12 fields.

- [ ] **Step 2:** Write `sim/tcw_display_test.go` — unit tests covering: (a) first mutation creates the per-ACID entry, (b) second mutation updates in place and bumps `Rev`, (c) mutation on one ACID doesn't touch another, (d) mutation on one TCW doesn't touch another TCW.
- [ ] **Step 3:** Add `Sim.pruneTCWDisplayAnnotations()` helper that iterates each TCW's `Annotations` map and removes entries whose ACID is not in the current sim track set. Test covers it.
- [ ] **Step 4:** Run `go test ./sim/ -run TCW -v`. Expect all pass.

---

## Task 4: Wire pruning into the tick loop

**Why:** Keeps `Annotations` bounded and prevents zombie entries after aircraft depart/delete.

- [ ] **Step 1:** Identify the per-tick entry point in `sim/sim.go` (likely `(*Sim).updateState` or equivalent). Add a call to `s.pruneTCWDisplayAnnotations()` on each tick.
- [ ] **Step 2:** Extend the pruning test from Task 3 to run the sim for a few ticks with an aircraft that departs and confirm the entry is gone.
- [ ] **Step 3:** Run `go test ./sim/ -v -count=1`. Expect green.

---

## Task 5: Dispatcher RPC handlers

**Why:** Server-side entry points for each mutation. Straight mirror of existing `SetTCW*` handler pattern.

- [ ] **Step 1:** For each of the 12 fields, in `server/dispatcher.go`:

  ```go
  type SetTrackJRingRadiusArgs struct {
      ControllerToken string
      ACID            sim.ACID
      Radius          float32
  }

  const SetTrackJRingRadiusRPC = "Sim.SetTrackJRingRadius"

  func (sd *dispatcher) SetTrackJRingRadius(args *SetTrackJRingRadiusArgs, update *SimStateUpdate) error {
      defer sd.sm.lg.CatchAndReportCrash()
      c := sd.sm.LookupController(args.ControllerToken)
      if c == nil {
          return ErrNoSimForControllerToken
      }
      c.sim.SetTrackJRingRadius(c.tcw, args.ACID, args.Radius)
      *update = c.GetStateUpdate()
      return nil
  }
  ```

  Repeat for all 12.

- [ ] **Step 2:** Build: `go build ./server/`. Expect clean.

---

## Task 6: Client wrappers

**Why:** Callback-style RPC glue matching the existing `SetTCWRange` pattern.

- [ ] **Step 1:** For each of the 12 fields, in `client/control.go`:

  ```go
  func (c *ControlClient) SetTrackJRingRadius(acid sim.ACID, r float32, callback func(error)) {
      var update server.SimStateUpdate
      c.addCall(makeStateUpdateRPCCall(c.client.Go(server.SetTrackJRingRadiusRPC, &server.SetTrackJRingRadiusArgs{
          ControllerToken: c.controllerToken,
          ACID:            acid,
          Radius:          r,
      }, &update, nil), &update, callback))
  }
  ```

  Repeat for all 12. Use matching types for the value (bool / `*bool` / `*math.CardinalOrdinalDirection` / etc.).

- [ ] **Step 2:** Build: `go build ./client/`. Expect clean.

---

## Task 7: Read-side helper in `stars`

**Why:** One accessor used everywhere. Keeps the read path uniform and testable.

- [ ] **Step 1:** Create `stars/track_shared.go` with:

  ```go
  // annotations returns the shared TCW annotations for the given ACID,
  // or a zero-value TrackAnnotations if no entry exists.
  func (sp *STARSPane) annotations(ctx *panes.Context, acid sim.ACID) sim.TrackAnnotations {
      d := ctx.Client.State.TCWDisplay
      if d == nil || d.Annotations == nil {
          return sim.TrackAnnotations{}
      }
      return d.Annotations[acid]
  }
  ```

- [ ] **Step 2:** Create `stars/track_shared_test.go` — reflective test ensuring (a) the list of synced field names matches the fields on `sim.TrackAnnotations` exactly, and (b) no field is listed twice or missing.
- [ ] **Step 3:** Run `go test ./stars/ -run Annotations -v`. Expect green.

---

## Task 8: Reroute STARS read sites

**Why:** Every read of a synced field today comes from `state.<field>` (where `state *TrackState`). Redirect to `sp.annotations(ctx, acid).<field>`.

- [ ] **Step 1:** `grep` for every read of the 12 fields in `stars/`. Expected concentrations: `stars/tools.go` (J-ring, cone, PTL), `stars/datablock*.go` (leader line, FDB, display flags), `stars/track.go`.
- [ ] **Step 2:** Replace each read. Where the caller has a `*TrackState` but not an `ACID`, look up the ACID via the existing track/flight-plan mapping (`trk.FlightPlan.ACID` or similar).
- [ ] **Step 3:** Delete the replaced fields from `stars.TrackState` (or keep them unused and marked deprecated if deletion is too invasive — prefer deletion).
- [ ] **Step 4:** Build `go build ./stars/`. Expect clean. Run `go test ./stars/`. Expect clean.

---

## Task 9: Reroute STARS write sites

**Why:** Every place that today writes `state.<field> = x` (J-ring command, leader-line command, datablock force command, etc.) must instead call `ctx.Client.SetTrack<Field>(acid, x, callback)` and rely on the RPC state-update echo to refresh the local mirror.

- [ ] **Step 1:** `grep` for each `state.<field> =` and `ts.<field> =` write. Expected concentrations: `stars/cmdtools.go` (J-ring, cone), `stars/cmdsetup.go` (leader-line), `stars/datablock*.go` (datablock force).
- [ ] **Step 2:** At each site, replace the direct write with the client wrapper call. Use the `sp.displayError` callback pattern from the existing foundation.
- [ ] **Step 3:** Build `go build ./stars/`. Expect clean.

---

## Task 10: Server integration test

**Why:** Prove the two-client sync path end-to-end at the server layer before manual verification.

- [ ] **Step 1:** Write `server/shared_tcw_integration_test.go` with:
  - Two human controllers at same TCW (primary signon + relief join).
  - A calls `SetTrackJRingRadius` for an ACID; B polls and sees it in `state.TCWDisplay.Annotations`.
  - B calls `SetTrackDisplayFDB` for a different ACID; A polls and sees it.
  - A disconnects and rejoins; annotations still present.
  - Aircraft for one of the annotated ACIDs is removed; next tick prunes; both clients' subsequent polls lack the entry.
- [ ] **Step 2:** Run `go test ./server/ -run SharedTCW -v`. Expect green.

---

## Task 11: Manual verification

**Why:** Exercises the real UI path the scope-view sync test exercised and confirms the pivot works in practice.

- [ ] **Step 1:** Rebuild `vice.exe`.
- [ ] **Step 2:** Launch dedicated server + Client A + Client B (isolated `APPDATA` + `-logdir` each, same as the foundation test).
- [ ] **Step 3:** Client A creates a sim on the dedicated server. Client B joins as relief at the same TCW.
- [ ] **Step 4:** On Client A, place a J-ring on an aircraft. Verify Client B's scope renders it within a second.
- [ ] **Step 5:** On Client B, toggle full datablock for a different aircraft. Verify Client A shows it.
- [ ] **Step 6:** On Client A, change leader-line direction for an aircraft. Verify Client B follows.
- [ ] **Step 7:** Confirm that changing range / panning / adjusting range-ring radius on one client does NOT propagate to the other (the rip-out is complete).
- [ ] **Step 8:** Report results. If anything misbehaves, go back to Tasks 8/9 and check the mutation was wired through the client RPC.

---

## Follow-ups (out of scope)

- **Opt-in scope-view sync.** Add a "Sync Scope State" checkbox to the "Join as Relief" dialog. Default off (plain relief only syncs datablock annotations). When on, the joiner's Range / UserCenter / RangeRingRadius (and possibly brightness / pref set) are copied from the current primary's state at join time and kept in sync. The removed `SetTCW{Range,UserCenter,RangeRingRadius}` RPCs + helpers live in git history and can be revived gated on a per-relief `SyncScopeState` flag.
- `ForceQLACIDs` sync (per-TCW quicklook to self).
- Event-driven push on top of the poll (lower latency for rapid changes).
- Command-authority typing lock.
- Datablock drag-to-reposition feature + per-ACID offset sync.
- ERAM parity.
