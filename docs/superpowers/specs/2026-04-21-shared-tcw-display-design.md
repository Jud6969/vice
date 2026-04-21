# Relief Controller Display Sync — Design

**Date:** 2026-04-21
**Scope:** STARS only (ERAM deferred)
**Branch:** `shared-tcw-display`

## Problem

In real life, when two controllers sit at the same physical Terminal Controller Workstation (TCW), they look at one monitor. Any J-ring, leader-line override, range change, filter, or video-map toggle is visible to both — there is only one screen.

In `vice` today, multi-human occupancy of a single TCW happens via **relief mode** — you click an occupied TCW in the multi-controller window and you join. Relief shares the TCW identity and event stream today, but **each relief controller keeps its own fully local display state** (`stars/prefs.go` + `stars/track.go` are client-side only). Two humans "sharing" a workstation see two unrelated pictures.

This design extends relief mode so the radar picture stays visually synchronized across all relief controllers at a TCW.

## Goals

- When two or more controllers occupy the same TCW (via existing relief flow), the radar picture (scope view + per-aircraft annotations) is identical across all of their clients.
- Either controller can issue commands; a keyboard-buffer lockout prevents stomping mid-command.
- Synced TCW display state persists for the life of the sim session, regardless of who is connected.
- Personal comfort settings (brightness, fonts) remain local.
- No new join UI — the existing relief click-through in the multi-controller window is the only entry point.

## Non-Goals

- ERAM scope support (separate future pass).
- A new "shared mode" concept distinct from relief — this is an extension *of* relief, not a new mode.
- Per-user customization *within* the synced buckets (if you need your own J-ring, that's what the unsynced bucket is for).
- Cross-TCW state sharing.
- Persisting TCW display state across sim restarts.
- Instructor / observer / shadow modes.

## User Flow

1. Controller A signs in to TCW **N01** normally. Their saved `PreferenceSet` for the TRACON loads; behavior is unchanged from today.
2. The server records an implicit `TCWDisplayState` for N01, seeded from A's current synced buckets the moment A signs in. If A never has a relief partner, this state just mirrors A's local view and has no observable effect.
3. Controller B opens the multi-controller window, sees N01 occupied, and clicks "Join as relief." No new UI, no prompt, no new button — exactly today's relief flow.
4. B's client joins via relief. On arrival:
   - B's **scope view** (range, center, rotation, filters, video maps, quick-look, lists positions) and **per-aircraft annotations** (J-rings, leader-line direction/offset, forced DB type, pinned/QL targets) adopt N01's current `TCWDisplayState`.
   - B's **unsynced** settings (brightness, character sizes, preference-set library) come from B's own saved prefs.
5. A or B makes a change in a synced bucket (e.g., J-rings JFK123 to 5nm). The mutation is sent to the server, applied to `TCWDisplayState[N01]`, and pushed to other clients at the TCW via an event-driven notification on top of the existing 1Hz state poll.
6. B leaves. A keeps everything; `TCWDisplayState[N01]` is unchanged.
7. A leaves. `TCWDisplayState[N01]` persists, untouched. Anyone who later signs in to N01 (solo or as relief) adopts the current synced state on arrival.
8. Sim session ends → all `TCWDisplayState` entries are discarded with the sim.

## Synced vs Unsynced Buckets

**Synced (owned by `TCWDisplayState`):**

- *Scope view:* `Range`, `UserCenter`, `Rotation`, altitude filters, CA/MSAW/MCI enable flags, video-map visibility, quick-look TCP list, disabled QL regions, list positions (TAB list, VFR list, etc.).
- *Per-aircraft annotations:* per-callsign `JRingRadius`, `ConeLength`, `LeaderLineDirection`, `FDAMLeaderLineDirection`, forced full/limited datablock, pinned / QL targets, pointouts visible at this TCW.

**Unsynced (stays local, per `stars.STARSPane`):**

- Brightness levels (`Brightness.*`).
- Character / font sizes (`CharSize.*`).
- Saved preference-set library (`TRACONPreferenceSets`) — each user keeps their own.
- Any client-only UI state (dialog positions, etc.).

The exact field-by-field split is codified in a new file `stars/shared_fields.go` so there is one authoritative list. A unit test asserts every field in `Preferences` is explicitly categorized as synced or unsynced to prevent silent drift.

## Preference-Set Loading

When a controller loads a saved preference set from their personal library while at a TCW that has another occupant (or at any TCW — the rule is uniform):

- **Unsynced fields** from the loaded set apply to the local client as they do today (brightness, font sizes, etc.).
- **Synced fields** from the loaded set are **ignored** — the load does **not** push new values into `TCWDisplayState`. Your partner's scope view / annotations are not yanked out from under them.

If a controller wants to change synced state, they make the change explicitly (e.g., set range, move center, place a J-ring) and the mutation propagates through the normal `TCWDisplayState` mutation path.

## Architecture

### Server side

- New type in `sim/stars.go`:

  ```go
  type TCWDisplayState struct {
      // Scope view state (subset of stars.Preferences, flattened, gob-safe)
      ScopeView ScopeViewState

      // Per-aircraft annotations, keyed by callsign
      Annotations map[av.ADSBCallsign]TrackAnnotations

      // Command-authority lockout (see Command Authority below)
      TypingLock *TypingLock

      // Monotonic revision; clients send last-seen rev, server replies with diffs
      Rev uint64
  }
  ```

- One `TCWDisplayState` is lazily created the first time a human signs in to a TCW. It is stored on the `Sim` struct, keyed by `TCW`. It is *not* serialized to disk (non-goal).

- **Typed RPC methods** on the server dispatcher (`server/dispatcher.go`) — one per synced field or coherent group of fields. Approximate set:
  - `SetTCWRange(token, range)`
  - `SetTCWCenter(token, lat, lon)`
  - `SetTCWRotation(token, degrees)`
  - `SetTCWAltitudeFilter(token, slot, min, max)`
  - `SetTCWCAEnabled(token, bool)` / `SetTCWMSAWEnabled(token, bool)` / `SetTCWMCIEnabled(token, bool)`
  - `SetTCWVideoMapVisible(token, mapID, visible)`
  - `SetTCWQuickLookList(token, []tcp)` / `SetTCWDisabledQLRegions(token, []region)`
  - `SetTCWListPosition(token, listID, pos)`
  - `SetTCWJRing(token, callsign, radius)` / `SetTCWCone(token, callsign, length)`
  - `SetTCWLeaderLine(token, callsign, direction, offset)`
  - `SetTCWForcedDatablockType(token, callsign, kind)`
  - `SetTCWPinned(token, callsign, bool)`
  - `AcquireTypingLock(token)` / `ReleaseTypingLock(token)`

  These are self-documenting, gob-safe, and match existing vice RPC style. The full list is finalized in the implementation plan once the synced-field categorization is locked.

- **Delivery** uses a hybrid of push + the existing 1Hz poll:
  - When the server applies a mutation, it pushes a `TCWDisplayStateDelta` event to every other human client at that TCW via the existing event channel (piggybacks on the same mechanism that already delivers sim events to relief sessions). Low-latency path.
  - `GetStateUpdate` also returns the current `TCWDisplayState` (full or diff by `Rev`) on every poll. This is the correctness floor — reconnects, missed events, and first-load all resolve via poll.

- `SimStateUpdate` (`server/manager.go:667`) grows a new field `TCWDisplay *TCWDisplayState` populated for the caller's current TCW.

- **No change** to `checkTCWAvailable()` or the relief join path. Relief semantics at the session level are unchanged; the new thing is *what relief clients see*, not *who can join*.

### Client side

- `stars.STARSPane` gets a reference to the sim's `TCWDisplayState` snapshot (via the existing `ControlClient`).
- When the STARS renderer reads a synced field, it reads through to the shared snapshot instead of the local `Preferences`. Unsynced fields still read from local `Preferences`.
- When the user makes a change to a synced field, the client issues the corresponding typed RPC mutation instead of mutating local `Preferences`. Local UI waits for the state echo to render the change (optimistic update is a later polish, not in the first cut).
- Per-aircraft `TrackState` display fields (J-ring radius, leader line, etc.) stop being the source of truth at TCWs with `TCWDisplayState`; shared annotations win. `TrackState` non-display fields (alert history, track points, etc.) remain local.

## Command Authority — Keyboard-Buffer Lockout

All relief controllers at a TCW have equal command authority. To prevent two people stomping each other mid-command, a server-arbitrated lock is used:

- When a relief client's keyboard input buffer becomes non-empty (user starts typing a command), the client calls `AcquireTypingLock(token)`.
- The server grants the lock if no other client at that TCW currently holds it. Otherwise, it denies.
- The client holding the lock completes or cancels its command; releasing (ENTER, ESC, or idle timeout ~3s with no further keystrokes) calls `ReleaseTypingLock`.
- While another client holds the lock, local typing at other relief clients is swallowed and a brief "locked by other controller" indicator appears in the scope's preview area.
- If the lock-holder disconnects unexpectedly, the lock auto-releases after a short timeout (~2s) once the server observes the session dead.

This is a best-effort UX feature under latency. Under extreme lag two commands can still both land in flight, but the lock covers the common "both mid-command" case well.

**Note:** This is a change to relief's command-authority model. Today relief's semantics around who-can-type are not display-synchronized; this design makes "both equal, with lockout" the explicit rule. If existing relief behavior is asymmetric (primary-only typing, relief observes), that asymmetry is removed as part of this change — confirm during planning.

## Lifecycle

- **First human signs in to a TCW:** `TCWDisplayState` is lazily created, seeded from that client's current synced-bucket values.
- **Second human joins via relief:** client receives current state on first `GetStateUpdate`; local scope-view/annotation reads immediately redirect to shared state.
- **Any human leaves:** no change to `TCWDisplayState`. Remaining humans continue.
- **Last human leaves:** `TCWDisplayState` persists on the `Sim`. It is not written to disk.
- **A new human later signs in to the same TCW** (solo or relief): adopts the current `TCWDisplayState` synced buckets (same hybrid-inherit rule). The state continues to exist — the TCW *has* a picture, and people just pick it up.
- **Sim ends:** all `TCWDisplayState` entries are discarded.

## Data Flow (synced mutation)

```
Client A                  Server (Sim)                Client B
   |                         |                          |
   | SetTCWJRing             |                          |
   | (JFK123, 5nm)           |                          |
   |------------------------>|                          |
   |                         | apply to TCWDisplayState |
   |                         | Rev++                    |
   |                         | event push ------------->|
   |                         |                          | re-render
   |<-- event echo ----------|                          |
   | re-render               |                          |
   |                         |                          |
   |                         |<-- GetStateUpdate -------|
   |                         |    (sinceRev = prev)     |
   |                         |    (corrective diff,     |
   |                         |     usually empty)       |
```

## Error Handling

- **RPC mutation failure:** client shows a transient "not synced" indicator; retries once on the next tick; falls back to local-only rendering after a grace period with a clearly-rendered banner.
- **Stale `sinceRev`:** server detects and sends a full snapshot; client resets its mirror.
- **Event push missed** (event channel drop): next 1Hz poll reconciles via `Rev` diff.
- **Aircraft referenced by an annotation no longer exists:** server prunes stale entries on each tick; clients ignore annotations for callsigns not in the current track set.
- **Typing-lock mismatch** (client thinks it holds it, server disagrees): server is authoritative; on a mismatch, server replies with the real current holder and the client corrects.

## Testing

- **Unit tests** in `sim/` for `TCWDisplayState` mutation, rev increment, and annotation pruning.
- **Unit test** in `stars/` asserting every `Preferences` field is categorized as synced or unsynced (prevents silent drift).
- **Integration test** in `server/` spinning up one sim, two mock clients (one signon + one relief), asserting a mutation on one appears in the other's state via event push (and also via poll if the event is dropped).
- **Integration test** for the last-leaves-then-rejoins case: A and B set state, both disconnect, a fresh client C signs in → sees A+B's state.
- **Integration test** that loading a personal preference set does **not** alter `TCWDisplayState` (synced fields ignored on load).
- **Manual test plan:**
  - Two real vice instances, same session. A signs in to N01 solo. B clicks "Join as relief." Verify B's scope immediately shows A's view.
  - Toggle a J-ring on A → appears on B within a frame or two (via event push).
  - Move a datablock leader on B → appears on A.
  - Change range; change center; toggle a video map; adjust altitude filter — all propagate.
  - A starts typing a command (non-empty preview) → B's typing is swallowed with "locked" indicator until A commits or cancels.
  - Adjust brightness on A → B's brightness unchanged.
  - Load a saved prefs set on A → scope view stays as before; only A's local brightness/fonts change.
  - Both disconnect, one reconnects to N01 → state restored.

## Migration / Compatibility

- Existing solo-controller flow is unchanged; `TCWDisplayState` is created even for solo sign-ins but has no observable effect.
- Existing relief flow is unchanged at the join layer; the only difference is the client-side rendering path and the new command-authority lockout.
- Save file format: unchanged (TCW display state is not persisted to disk).
- RPC protocol: additive only (new methods, new optional field on `SimStateUpdate`, new event type). Old clients against a new server keep working — they simply never call the new mutation methods and will see a stale local view, which is no worse than today's behavior.

## Open Items (to be resolved in the implementation plan)

- **Final synced-field list** — exact inventory of `Preferences` fields that move from local to `TCWDisplayState`. The categorization test will drive this; needs field-by-field walk-through.
- **Existing relief command-authority behavior** — confirm current relief semantics around who-can-type before replacing them with "both equal + keyboard-buffer lockout." If relief today is already symmetric, the lockout is pure addition; if it's asymmetric, this is a behavioral change worth calling out in release notes.
- **Max relief controllers per TCW** — the lockout and event-push design generalize cleanly to N>2, but existing relief may have a cap. Spec should match existing behavior; confirm during planning.
- **Event-push backpressure** — if a user rapidly drags a datablock leader, many mutations fire. Decide whether to coalesce on the server (e.g., debounce by field, last-write-wins within a small window) or send every frame.
