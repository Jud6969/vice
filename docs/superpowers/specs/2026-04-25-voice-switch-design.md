# Voice Switch Pane — Design

Branch target: `pilot-freq-handoff-realism` (built on its `av.Controller.Frequency` model).
Date: 2026-04-25

## Motivation

The `pilot-freq-handoff-realism` branch made frequencies first-class numeric values (`av.Frequency`) attached to controllers. Today the client decides which radio transmissions a user "hears" implicitly via `c.State.UserControlsPosition(event.ToController)` — there is no user-facing control over which frequencies are active.

This design adds a **Voice Switch** pane: a toggleable window that lists the user's tuned frequencies with per-row receive (RX) and transmit (TX) checkboxes. The voice switch is conceptually independent of the TCW/consolidation system — it auto-seeds from the user's controlled positions on first connect and then becomes user-managed.

## Scope

**In:**
- New pane `panes.VoiceSwitchPane`, mirroring `FlightStripPane` lifecycle.
- Auto-seed at first connect from positions the user's TCW currently controls.
- Reconcile each frame against current consolidation: gained positions append a row (RX+TX on); lost positions flip RX+TX off (row preserved).
- Manual frequency entry via a type-in box; validates against scenario controllers.
- Per-row RX/TX checkboxes; remove (`x`) button on non-owned rows only.
- Hover tooltip listing the controller(s) on each frequency.
- RX off → suppress message in messages pane and audio alert; nothing else changes.
- TX off (owned positions) → silently no-op the user's outgoing command before RPC.

**Out (deferred):**
- **Cross-coupling.** TX-on for non-owned frequencies stores state but does not grant transmit authority. The TX checkbox is intentionally clickable and persists state as a placeholder; the gating logic for non-owned TX lands in a follow-up that needs server-side authority coordination.
- **Cross-session persistence of tuned frequencies.** Manual tunings reset every connect; only the window-shown flag and font size persist.
- **Per-TCW shared state for relief mode.** Each client runs its own voice switch.
- **Network/RPC propagation.** All voice-switch state is client-local; sim and server are untouched.

## Data model

```go
// panes/voiceswitch.go
type VoiceSwitchPane struct {
    FontSize int
    font     *renderer.Font

    rows     []voiceSwitchRow // ordered top-to-bottom
    seeded   bool             // false → run auto-seed on next reconcile
    addInput string           // text in the type-in freq box
}

type voiceSwitchRow struct {
    Freq  av.Frequency
    RX    bool
    TX    bool
    Owned bool // true if any currently-controlled position uses this freq
}
```

**Config (`cmd/vice/config.go`):** `Config.ShowVoiceSwitch bool`, default `true`. Parallel to `ShowFlightStrips`.

**Persisted across sessions:** `ShowVoiceSwitch`, `VoiceSwitchPane.FontSize`. Nothing else — the row list is session-scoped.

## Lifecycle

`reconcile(c *client.ControlClient)` runs at the top of `DrawWindow` each frame.

### First call (when `!seeded && c.State.UserTCW != ""`)

1. For each `pos` in `c.State.GetPositionsForTCW(c.State.UserTCW)`:
   - `freq := c.State.Controllers[pos].Frequency` (skip if zero or controller missing).
   - If `rows` does not yet contain `freq`, append `{Freq: freq, RX: true, TX: true, Owned: true}`.
2. Set `seeded = true`.

If the user joins with no controlled positions yet, `seeded` stays `false` and the seed runs on a later frame once a TCW is assigned. This avoids re-seeding on every empty-state frame.

### Subsequent calls

1. Compute `currentlyOwned := { c.State.Controllers[pos].Frequency for pos in GetPositionsForTCW(UserTCW) }`.
2. For each existing row:
   - `row.Owned && !currentlyOwned[row.Freq]` → set `Owned=false, RX=false, TX=false`. Row stays.
   - `!row.Owned && currentlyOwned[row.Freq]` → set `Owned=true, RX=true, TX=true`.
3. For each freq in `currentlyOwned` not present in `rows` → append `{Freq, RX:true, TX:true, Owned:true}`.

### Manual add (user types into the input box)

- Parse via `av.NewFrequency(parsed)`.
- Reject if no `c.State.Controllers[*].Frequency == parsed` (validation against scenario).
- Reject if the freq is already a row.
- Otherwise append `{Freq, RX:true, TX:false, Owned:false}`. TX defaults off (cross-coupling not wired).

### Manual remove (`x` button)

- Only rendered on non-owned rows (`!row.Owned`).
- Click → drop the row.

### `ResetSim`

Clear `rows`, set `seeded = false`, clear `addInput`. Next frame re-seeds for the new sim.

## RX gating

Helper on the pane:

```go
func (vs *VoiceSwitchPane) IsRX(pos sim.ControlPosition, ss *sim.CommonState) bool
```

Resolves `pos → freq` via `ss.Controllers[pos].Frequency`, then returns the matching row's `RX`. Returns `ss.UserControlsPosition(pos)` as a fallback when `pos` does not resolve to a frequency (sentinels like `_TOWER`, virtual/external controllers without a numeric freq).

**Wiring — `panes/messages.go`:**

Replace the existing line at `messages.go:166`:

```go
toUs := c.State.UserControlsPosition(event.ToController)
```

with:

```go
toUs := voiceSwitch.IsRX(event.ToController, &c.State)
```

The existing `priv := c.State.TCWIsPrivileged(c.State.UserTCW)` and `if !toUs && !priv { break }` lines stay unchanged. Privileged TCWs (supervisors) keep their existing override; voice switch does not gate them. The same gate already covers both message rendering and the audio alert (`ContactTransmissionsAlert` / `ReadbackTransmissionsAlert`), so no separate audio change is needed.

## TX gating

Helper on the pane:

```go
func (vs *VoiceSwitchPane) CanTransmitOnPrimary(ss *sim.CommonState) bool
```

Looks up `primary := ss.PrimaryPositionForTCW(ss.UserTCW)`, resolves `freq := ss.Controllers[primary].Frequency`, finds the matching row, and returns `row.TX` (or `true` if the freq is unresolvable, to avoid silently breaking commands when the model can't tell).

**Wiring — call sites:**

Two client-side paths originate user commands:

1. **STARS typed-command processor** — `stars/cmdtools.go` and adjacent dispatch sites.
2. **STT pipeline** — `stt/...` end-of-pipeline handoff to the client RPC layer.

Both invoke `voiceSwitch.CanTransmitOnPrimary(&c.State)` immediately before sending the command RPC. If `false` → silently drop (no error message, no state change, no RPC).

`*VoiceSwitchPane` is plumbed into both call sites from `cmd/vice/ui.go` the same way `FlightStripPane` already is.

## UI layout

Window mounted in `cmd/vice/ui.go` next to the flight strip window:

```go
config.VoiceSwitchPane.DrawWindow(&ui.showVoiceSwitch, controlClient, p, config.UnpinnedWindows, lg)
```

Settings UI gets a "Voice Switch" entry alongside "Flight Strips" (collapsing header with font size selector).

**Per-row layout** (one row per frequency, frequency-only display):

```
[RX]  [TX]  124.350   [x]
[RX]  [TX]  121.900
[RX]  [TX]  127.750   [x]
─────────────────────────
[ tune freq → 12_____ ]
```

- `[x]` only rendered when `!row.Owned`.
- Hover any row → tooltip lists `{Callsign} — {RadioName}` for every controller whose `Frequency == row.Freq` (handles the satellite-freq case).
- Type-in box at the bottom, fixed width; Enter submits via the manual-add flow above. Invalid freq → input clears with no row added (silent rejection consistent with the rest of the pane).

## Multi-controller behavior

| Scenario | Behavior |
|---|---|
| Each controller's auto-seed | Per-client; walks each client's own `UserTCW` positions. No cross-client coordination. |
| Controller A tunes Controller B's freq | A starts hearing pilot transmissions on B's freq (eavesdrop). B is unaffected; both hear the traffic. Non-owned for A → no TX gate (matches "cross-coupling deferred"). |
| Consolidation handoff B → A | Both clients observe the consolidation change in their own `c.State`. A's reconcile adds the row (RX+TX on). B's reconcile flips the row to RX+TX off. No RPC needed. |
| Relief mode (two clients on one TCW) | Each client has its own voice switch state. They can be inconsistent; users coordinate manually. Per-TCW shared state is out of scope. |
| Mid-handoff timing | Brief window where consolidation has changed but the pilot's `ControllerFrequency` hasn't caught up: the gainer might miss one transmission addressed to the freshly-on row, or the loser might miss the last transmission addressed to the freshly-off row. Acceptable; no special handling. |

## Touch points

| File | Change |
|---|---|
| `panes/voiceswitch.go` (new) | `VoiceSwitchPane` type, `Activate`, `ResetSim`, `DrawUI`, `DrawWindow`, `reconcile`, `IsRX`, `CanTransmitOnPrimary`, manual add/remove handlers, hover tooltip rendering. |
| `panes/messages.go` (~line 166) | Replace `UserControlsPosition` call with `voiceSwitch.IsRX(...)`. Add `*VoiceSwitchPane` parameter to `MessagesPane.DrawWindow` (or wherever `processEvents` is reachable). |
| `stars/cmdtools.go` (and adjacent dispatch sites) | Insert `if !voiceSwitch.CanTransmitOnPrimary(&c.State) { return }` immediately before each command-issuing RPC. |
| `stt/` pipeline tail (provider/handlers, wherever the client-side dispatch hands off to the RPC) | Same TX gate inserted at the final dispatch point. |
| `cmd/vice/config.go` | `ShowVoiceSwitch bool` (default `true`); `VoiceSwitchPane *panes.VoiceSwitchPane` field; `NewVoiceSwitchPane()` in default config; `Activate()` call mirroring `FlightStripPane`. |
| `cmd/vice/ui.go` | Show/hide handling parallel to `showFlightStrips`; `DrawWindow` invocation; collapsing header for settings. Plumb `*VoiceSwitchPane` into messages/STARS/STT call sites. |
| `cmd/vice/main.go` | `config.VoiceSwitchPane.ResetSim(...)` call alongside the existing `FlightStripPane.ResetSim(...)`. Save/restore the `ShowVoiceSwitch` flag. |

No changes in `sim/`, `server/`, `client/`, or `aviation/`.

## Testing

### Unit tests (`panes/voiceswitch_test.go`)

| Area | Test |
|---|---|
| Auto-seed | Empty pane + one TCW with two positions on distinct freqs → two rows, both RX+TX on, both Owned. `seeded == true`. |
| Auto-seed dedup | TCW with two positions sharing one freq → single row. |
| Auto-seed deferred | `seeded == false` while `UserTCW == ""`; flips to `true` after a TCW is assigned and reconcile runs. |
| Reconcile gain | Existing row with RX off, controller's freq becomes owned by user → row flips to Owned, RX+TX on. |
| Reconcile loss | Owned row, position handed away → row stays, Owned=false, RX=false, TX=false. |
| Manual add valid | Type a freq matching a scenario controller → row appended, RX on, TX off, Owned false. |
| Manual add invalid | Type a freq not in scenario → no row, input clears. |
| Manual add duplicate | Type a freq already a row → no change. |
| Manual remove | `x` on non-owned row → row removed. `x` not rendered for owned rows. |
| ResetSim | Rows cleared, `seeded == false`, `addInput == ""`. |

### Integration tests

| Area | Test |
|---|---|
| RX off suppresses message | Synthesize `RadioTransmissionEvent` for `event.ToController = pos` where `voiceSwitch.IsRX(pos)` returns `false`; assert message not appended to messages pane and audio alert not played. |
| RX on shows message | Same but with RX on; assert message appended. |
| RX fallback for sentinel `ToController` | `event.ToController = "_TOWER"` (no entry in `Controllers`) → `IsRX` falls back to `UserControlsPosition`. |
| Privileged override | Privileged TCW + RX off → message still shown. |
| TX off no-ops | With user's primary-position freq RX+TX off, invoke a STARS command → no RPC issued. |
| TX on issues RPC | Same primary, TX on → RPC issued normally. |

### Manual verification

- Window appears in default layout, can be hidden and re-shown.
- On connect, the rows match the controllers for your assigned position(s).
- Toggle RX off on your own freq → pilot transmissions to you stop appearing in the messages pane and stop alerting.
- Toggle TX off on your own freq → STARS commands silently no-op.
- Type in another controller's freq → row appears with RX on, TX off; their pilot transmissions start showing in your messages pane.
- Hand a position to another controller (or have one handed to you) and observe row flips on both sides.
- Reconnect → manual tunings are gone; auto-seed runs again.

## Error surface

| Condition | Handling |
|---|---|
| Manual add: malformed freq input | Input clears, no row, no log entry (silent). |
| Manual add: freq not in scenario | Same as above. |
| Manual add: duplicate | Same as above. |
| RX query for unresolvable position | Falls back to `UserControlsPosition`. |
| TX query for unresolvable primary | Returns `true` (don't silently break command flow). |
| RX/TX toggle with no rows yet (pre-seed) | No-op; pane just doesn't render rows. |
