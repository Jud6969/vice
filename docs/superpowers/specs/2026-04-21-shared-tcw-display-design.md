# Shared TCW Display State — Design

**Date:** 2026-04-21
**Scope:** STARS only (ERAM deferred)
**Branch:** `shared-tcw-display`

## Problem

In real life, when two controllers sit at the same physical Terminal Controller Workstation (TCW), they look at one monitor. Any J-ring, leader-line override, range change, filter, or video-map toggle is visible to both — there is only one screen.

In `vice` today, multi-human occupancy of a single TCW only exists via **relief mode**, and even there, each client keeps its own fully local display state (`stars/prefs.go` + `stars/track.go` are client-side only). Two humans sharing a workstation see two unrelated pictures. The server also actively blocks non-relief co-occupancy via `ErrTCWAlreadyOccupied` (`server/manager.go:347`).

This design adds a new **shared TCW mode** — separate from relief — where multiple humans sign in to the same TCW and the radar picture stays visually synchronized.

## Goals

- Two or more controllers can sign in to the same TCW in "shared" mode.
- The radar picture (scope view + per-aircraft annotations) is identical across all shared clients at that TCW.
- Either controller can issue commands; a keyboard-buffer lockout prevents stomping mid-command.
- Shared state persists for the life of the sim session, regardless of who is connected.
- Personal comfort settings (brightness, fonts) remain local.

## Non-Goals

- ERAM scope support (separate future pass).
- Per-user customization *within* the shared buckets (e.g., "I want different J-rings than my partner" — not supported by design; that's what the unsynced bucket is for).
- Replacing or altering relief mode. Relief mode continues to work as it does today.
- Cross-TCW state sharing.
- Persisting shared state across sim restarts.
- Instructor / observer / shadow modes.

## User Flow

1. Controller A signs in to TCW **N01** normally. Their saved `PreferenceSet` for the TRACON loads; behavior is unchanged from today.
2. The server records an implicit `SharedTCWState` for N01, seeded from A's current synced buckets the moment A signs in.
3. Controller B opens the multi-controller / relief window and sees N01 listed as occupied. B clicks it (no prompt, same UX as joining relief).
4. B's client joins N01 in shared mode. On arrival:
   - B's **scope view** (range, center, rotation, filters, video maps, quick-look, lists positions) and **per-aircraft annotations** (J-rings, leader-line direction/offset, forced DB type, pinned/QL targets) adopt the current N01 shared state.
   - B's **unsynced** settings (brightness, character sizes, preference-set library) come from B's own saved prefs.
5. A or B makes a change in a synced bucket (e.g., J-rings JFK123 to 5nm). The mutation is sent to the server, applied to `SharedTCWState[N01]`, and pushed to the other client on the next tick (with a best-effort instant push).
6. B leaves. A keeps everything; `SharedTCWState[N01]` is unchanged.
7. A leaves. `SharedTCWState[N01]` persists, untouched. Anyone who later signs in (shared or solo) to N01 adopts the current shared state on arrival (per Q7-B).
8. Sim session ends → all `SharedTCWState` entries are discarded with the sim.

## Synced vs Unsynced Buckets

**Synced (owned by `SharedTCWState`):**

- *Scope view:* `Range`, `UserCenter`, `Rotation`, altitude filters, CA/MSAW/MCI enable flags, video-map visibility, quick-look TCP list, disabled QL regions, list positions (TAB list, VFR list, etc.).
- *Per-aircraft annotations:* per-callsign `JRingRadius`, `ConeLength`, `LeaderLineDirection`, `FDAMLeaderLineDirection`, forced full/limited datablock, pinned / QL targets, pointouts visible at this TCW.

**Unsynced (stays local, per `stars.STARSPane`):**

- Brightness levels (`Brightness.*`).
- Character / font sizes (`CharSize.*`).
- Saved preference-set library (`TRACONPreferenceSets`) — each user keeps their own.
- Any client-only UI state (dialog positions, etc.).

The exact field-by-field split will be codified in a new file `stars/shared_fields.go` so there is one authoritative list. (Add a unit test that asserts every field in `Preferences` is explicitly categorized as synced or unsynced to prevent drift.)

## Architecture

### Server side

- New type in `sim/stars.go`:

  ```go
  type SharedTCWState struct {
      // Scope view state (subset of stars.Preferences, flattened / gob-safe)
      ScopeView ScopeViewState

      // Per-aircraft annotations, keyed by callsign
      Annotations map[av.ADSBCallsign]TrackAnnotations

      // Command-authority lockout (see below)
      TypingLock *TypingLock

      // Monotonic revision; clients send last-seen rev, server replies with diffs
      Rev uint64
  }
  ```

- One `SharedTCWState` is lazily created when the first human signs in to a TCW. It is stored on the `Sim` struct, keyed by `TCW`. It is *not* serialized to disk (non-goal).

- New RPC methods on the server dispatcher (`server/dispatcher.go`):
  - `GetSharedTCWState(token, sinceRev) (state, rev)` — diff-polled as part of the normal `GetStateUpdate` flow. Server returns the full state if `sinceRev == 0`, otherwise a delta.
  - `SetSharedTCWField(token, fieldPath, value)` — generic mutation (or a handful of typed mutation calls — to be decided during planning, leaning typed to keep the protocol gob-safe and self-documenting).
  - `AcquireTypingLock(token) / ReleaseTypingLock(token)` — see Command Authority below.

- `SimStateUpdate` (`server/manager.go:667`) grows a new field `SharedTCW *SharedTCWState` populated for the caller's current TCW. Reusing the existing polling tick avoids a separate poller; a best-effort "nudge" mechanism can be added later if 1Hz feels sluggish in testing.

- **Remove** the non-relief exclusivity check in `checkTCWAvailable()` when the joining client requests "shared" mode. Relief remains a separate, unchanged code path. **Proposed:** shared and relief are mutually exclusive on a given TCW — the first joiner's choice determines the mode, and the UI reflects that. (See Open Items.)

### Client side

- `stars.STARSPane` gets a reference to the sim's `SharedTCWState` snapshot (via the existing `ControlClient`).
- When the STARS renderer reads a synced field, it reads through to the shared snapshot instead of the local `Preferences`. Unsynced fields still read from local `Preferences`.
- When the user makes a change to a synced field, the client issues the corresponding RPC mutation instead of mutating local `Preferences`. The local UI waits for the state echo to render the change (optimistic update is a later polish, not in the first cut).
- Per-aircraft `TrackState` display fields (J-ring radius, leader line, etc.) stop being the source of truth at shared TCWs; `SharedTCWState.Annotations` wins. `TrackState` non-display fields (alert history, track points, etc.) remain local.

### Join flow

- Multi-controller / relief window gains a "Join shared" action alongside the existing "Join as relief" action for occupied TCWs. No prompt to existing occupants. Session password is the only gate, same as relief.
- `ConnectToSim` in `server/manager.go` grows a `JoiningAsShared bool` flag (parallel to `JoiningAsRelief`), and `checkTCWAvailable()` is updated accordingly.

## Command Authority — Keyboard-Buffer Lockout

Both shared controllers have equal command authority, but to prevent two people stomping each other mid-command, a best-effort server-arbitrated lock is used:

- When a shared client's keyboard input buffer becomes non-empty (user starts typing a command), the client calls `AcquireTypingLock(token)`.
- The server grants the lock if no other shared client at that TCW currently holds it. Otherwise, it denies.
- The client that holds the lock completes or cancels its command; releasing (ENTER, ESC, or idle timeout ~3s with no further keystrokes) calls `ReleaseTypingLock`.
- While another client holds the lock, local typing at other shared clients is swallowed and a brief "locked by other controller" indicator appears in the scope's preview area.
- If the lock-holder disconnects unexpectedly, the lock auto-releases after a short timeout (~2s after the session is observed dead).

This is a UX feature, not a hard safety guarantee — under latency, two commands *can* still land in flight, but that's an accepted consequence. The lock covers the much more common "both of us are mid-command" case.

## Lifecycle

- **First shared client signs in:** `SharedTCWState` is lazily created, seeded from that client's current synced-bucket values.
- **Additional shared client joins:** client receives current state on first `GetStateUpdate`; local scope-view/annotation reads immediately redirect to shared.
- **Any shared client leaves:** no change to `SharedTCWState`. Remaining clients continue.
- **Last shared client leaves:** `SharedTCWState` persists on the `Sim`. It is not written to disk.
- **A new, solo (non-shared) client later signs in to the same TCW:** also adopts the current `SharedTCWState` synced buckets (same hybrid-inherit rule as a shared joiner). The `SharedTCWState` continues to exist — a "solo" controller at a TCW that has shared state is effectively just "the only shared client." This avoids a pathological reset if someone disconnects and reconnects.
- **Sim ends:** all `SharedTCWState` entries are discarded.

## Data Flow Diagram (synced mutation)

```
Client A                  Server (Sim)                Client B
   |                         |                          |
   | SetSharedTCWField       |                          |
   | (J-ring JFK123 = 5nm)   |                          |
   |------------------------>|                          |
   |                         | apply to SharedTCWState  |
   |                         | Rev++                    |
   |                         |                          |
   |                         |<---- GetStateUpdate -----|
   |                         |      (sinceRev = prev)   |
   |                         |                          |
   |                         | diff -> SharedTCW delta  |
   |                         |------------------------->|
   |                         |                          | re-render
   |<-- GetStateUpdate ------|                          |
   |    (echo of own change) |                          |
   | re-render               |                          |
```

## Error Handling

- **RPC failures on mutation:** client shows a transient "not synced" indicator; retries once on the next tick; falls back to local-only display after a grace period with a clearly-rendered banner.
- **Stale `sinceRev`:** server detects and sends a full snapshot; client resets its mirror.
- **Aircraft referenced by an annotation no longer exists:** server prunes stale entries on each tick; clients ignore annotations for callsigns not in the current track set.
- **Typing-lock deadlock (both clients think they hold it):** server is authoritative; on a mismatch, server replies with the real current holder and the client corrects.

## Testing

- **Unit tests** in `sim/` for `SharedTCWState` mutation, rev increment, and annotation pruning.
- **Unit test** in `stars/` asserting every `Preferences` field is categorized as synced or unsynced (prevents silent drift).
- **Integration test** in `server/` spinning up one sim, two mock clients in shared mode, asserting a mutation on one appears in the other's state within one tick.
- **Integration test** for the last-leaves-then-rejoins case (Q7-B).
- **Manual test plan:**
  - Two real vice instances, same session, N01. Toggle a J-ring on one, confirm on the other.
  - Move a datablock leader on one, confirm on the other.
  - Change range; change center; toggle a video map.
  - Start typing a command on one — confirm the other is soft-locked.
  - Both drop, rejoin — confirm state persists.
  - Relief mode on a different TCW still works unchanged.

## Migration / Compatibility

- Existing solo-controller flow is unchanged; `SharedTCWState` is created even for solo sign-ins but has zero observable effect.
- Save file format: unchanged (shared state is not persisted to disk).
- RPC protocol: additive only (new methods, new optional field on `SimStateUpdate`). Old clients on a new server keep working (they just never request shared mode).

## Open Items (to be resolved in the implementation plan)

- **Typed vs generic mutation RPC** — several typed calls (`SetSharedRange`, `SetSharedJRing`, …) vs one generic `SetSharedField(path, value)`. Leaning typed.
- **Push vs poll cadence for the shared state delta** — whether the 1Hz `GetStateUpdate` cadence is responsive enough for J-ring/leader changes or whether an event-driven push is needed. Start with poll; measure.
- **Exact UI wording for "shared" in the multi-controller window.**
- **Behavior when a shared controller opens a saved preference set from their personal library** — does loading that set push *its* synced fields to `SharedTCWState`, or does it only apply to unsynced fields? Proposed: it pushes synced fields to shared (the "I want the scope configured this way" intent wins).
- **Shared vs relief on the same TCW** — the spec proposes they are mutually exclusive, mode fixed by the first joiner. Alternatives to consider: allow coexistence (relief controllers at a shared TCW also see the shared display), or block shared mode whenever a relief session is active. Confirm before implementation.
