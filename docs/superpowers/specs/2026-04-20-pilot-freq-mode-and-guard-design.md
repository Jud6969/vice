# Frequency Management Mode & GUARD Command — Design

Branch target: `pilot-freq-handoff-realism`
Date: 2026-04-20

## Motivation

The `pilot-freq-handoff-realism` branch has tightened the frequency-management model: `FC`/`TO` require a frequency, unknown frequencies produce `UnknownFrequencyIntent`, and readback style varies by whether the handoff crosses facility boundaries. This is realistic but inflexible for users who prefer the older forgiving behavior.

Separately, there is no way to force an off-frequency aircraft back onto a usable frequency. In real-world operations this is done by broadcasting on guard (121.5). This design adds a `GUARD` command prefix that simulates that capability without modeling the 121.5 channel itself.

## Features

1. **Frequency Mode toggle.** Cycleable Settings button that switches between "Realistic" (current branch behavior) and "Conventional" (pre-branch forgiving behavior). Default: Conventional.
2. **GUARD command.** Prefix modifier meaning "deliver this instruction via guard broadcast, bypassing the aircraft's current listening state." Usable typed or spoken; trailing commands run through the normal dispatcher. The aircraft never reads back on guard.

The two features are orthogonal: GUARD works identically in both modes.

## Architecture

### Data flow — spoken GUARD, happy path

```
STT transcription
  │  contains "guard" keyword ─► enter guard-pattern pass
  │
  ▼
match pattern → emit   "SWA123 GUARD FC 128375"   (+ trailing ID if "acknowledge with IDENT")
  │
  ▼
STARS input dispatcher (TG mode)
  │  peel off callsign + GUARD prefix + trailing
  ▼
Sim.Guard(tcw, "SWA123", "FC 128375 ID")
  │  verify ac != user's freq
  │  set ac.ControllerFrequency = user's TCP
  │  recursively dispatch trailing "FC 128375 ID" with fromGuard=true
  ▼
State changes (freq switch, IDENT transponder) applied;
no pilot transmission queued
```

### Data flow — typed GUARD

Identical to above from "STARS input dispatcher" downward. Typed input bypasses the STT layer entirely.

## Feature A — Frequency Mode semantics

A single config field `Config.RealisticFrequencyManagement bool` (default `false` = Conventional). Toggling flips exactly three behaviors:

| Behavior | Conventional (default) | Realistic |
|---|---|---|
| Bare `FC` (no freq) | Always hands off to tracking controller. | Only allowed when `Approach.Cleared` (routes to tower); otherwise requires a `{freq}` argument. |
| Unknown `{freq}` — not in scenario / out-of-band | Silently routes to tracking controller (or tower if cleared); no `UnknownFrequencyIntent`. | Emits `UnknownFrequencyIntent` → aircraft says "say again the frequency?" and keeps current state. |
| Readback style | Always `"contact {position} on {freq}, good day"`. `ContactIntent.SameFacility` forced to `false`. | Same-facility → frequency-only (`"{freq}, good day"`); cross-facility → full position+freq. |

Unchanged in both modes:
- STT grammar still parses frequencies where present — Conventional just doesn't fail if they don't match a controller.
- `TO` (bare tower) still requires a single-tower arrival airport.
- `resolveControllerByFrequency` still runs; Conventional tolerates its "no match" outcome instead of surfacing it.
- Typed-command → full-readback discipline (`fromTypedCommand` forces `SameFacility = false`) is still active in Realistic.

**Mode-change UX:** flipping the Settings button takes effect immediately for subsequent typed and spoken commands. No restart, no reconnect. The bool is re-read at each command dispatch, so no in-flight state migration is needed.

## Feature B — GUARD command

### Semantics

`GUARD` is a prefix modifier on any aircraft command. It means "deliver this instruction via guard broadcast, bypassing the aircraft's current listening state." The trailing command (if any) runs through the normal dispatcher with a `fromGuard` flag that suppresses **all** pilot transmissions. State changes (frequency, transponder, heading, etc.) still apply.

### Typed forms (STARS TG mode)

| Input | Effect |
|---|---|
| `SWA123 GUARD` | Bare guard → force `SWA123` onto user's frequency. No readback, no IDENT. |
| `SWA123 GUARD ID` | Force onto user's frequency, then IDENT. |
| `SWA123 GUARD FC 134050` | Redirect `SWA123` to freq 134.050 (resolves via normal `resolveControllerByFrequency`). No readback. |
| `SWA123 GUARD FC 134050 ID` | Redirect + IDENT. |
| `SWA123 GUARD FH 270` | Guard-force to user's freq, then heading 270. |

### Preconditions

- Callsign must exist in `s.Aircraft`. Otherwise `ErrUnknownAircraft`.
- Target frequency must differ from `ac.ControllerFrequency`. Otherwise `ErrAlreadyOnFrequency` ("{callsign} already on this frequency"). The "target" is:
  - Bare `GUARD` → user's frequency (`s.State.Controllers[tcw].Frequency`).
  - `GUARD FC {freq}` → that freq's resolved controller.
  - `GUARD <non-FC command>` → user's frequency (implicit freq switch + trailing command).

### Dispatch — sim-side

```go
// sim/control.go
func (s *Sim) Guard(tcw TCW, callsign av.ADSBCallsign, trailing string) (av.CommandIntent, error) {
    ac, ok := s.Aircraft[callsign]
    if !ok { return nil, ErrUnknownAircraft }

    targetCtrl, _, err := s.resolveGuardTarget(tcw, trailing)
    if err != nil { return nil, err }
    if ac.ControllerFrequency == ControlPosition(targetCtrl.Callsign) {
        return nil, ErrAlreadyOnFrequency
    }
    ac.ControllerFrequency = ControlPosition(targetCtrl.Callsign)

    if trailing == "" { return nil, nil } // silent switch, no intent

    return s.runOneControlCommand(tcw, callsign, trailing, false, /*fromGuard=*/ true)
}
```

`resolveGuardTarget` inspects `trailing`:
- If it begins with `FC<digits>` (post-`mergeFrequencyArgs`) → parse freq, call `resolveControllerByFrequency`.
  - On match → return that controller.
  - On no match, Conventional mode → return `s.State.Controllers[TCP(ac.ControllerFrequency)]` (the current tracking controller — matches non-guard Conventional FC behavior for unknown freq).
  - On no match, Realistic mode → return `ErrUnknownFrequency` (can't force an aircraft onto a freq that doesn't exist in the scenario).
- Otherwise (no FC in trailing) → return `s.State.Controllers[tcw]` (user's frequency).

### The `fromGuard` flag

`runOneControlCommand` gains a `fromGuard bool` parameter plumbed into every intent-emitting branch. When set:
- `enqueuePilotTransmission` calls are skipped.
- Readback rendering is not invoked for the bundled command.
- State mutations (`ac.ControllerFrequency`, transponder mode, heading, speed, altitude, etc.) still apply.

## STT grammar

### Detection trigger

If the raw transcription contains the keyword `guard` (case-insensitive, word-boundary match), the STT dispatcher runs a dedicated guard-pattern pass in addition to the normal pattern table. If `guard` is absent, normal grammar only.

### Guard pattern table (`stt/handlers.go`)

| Priority | Pattern | Emits | Notes |
|---|---|---|---|
| 40 | `{callsign} contact me immediately on {frequency}` | `<callsign> GUARD FC <digits>` | Target = user's freq; spoken freq is accepted but not required to match (treated as confirmation). |
| 40 | `{callsign} switch to my frequency {frequency}` | `<callsign> GUARD FC <digits>` | Same as above. |
| 39 | `{callsign} contact {position_text} on {frequency}` | `<callsign> GUARD FC <digits>:<hint>` | Redirect — `position_text` flows to `resolveControllerByFrequency` as hint. |
| 38 | `{callsign} contact {position_text} {frequency}` | `<callsign> GUARD FC <digits>:<hint>` | Elided "on". |

Priorities 38–40 exceed any non-guard `contact` pattern (max priority 20 today), so they win when guard context is active. Within the guard set, most-specific-first.

Each pattern accepts an optional trailing `acknowledge with ident` clause. When present, the emitted command gets ` ID` appended (so `<callsign> GUARD FC <digits> ID`).

### Callsign extraction

Reuses the existing `{callsign}` token parser (ICAO- and type-prefixed spoken forms, e.g. "southwest one twenty three", "skyhawk four kilo foxtrot"). The `{frequency}` parser (with NAS 25 kHz snap and leading-zero preservation) applies unchanged.

### Behavior when keyword/callsign don't line up

- Guard keyword present, no matching callsign in the pattern → log "guard transmission with no callsign match" and no-op (matches today's out-of-scenario callsign failure).
- Guard keyword absent, pattern looks like a guard pattern → falls through to non-guard patterns (which won't match the richer forms, so also a no-op).

## Settings UI

A single cycleable `imgui.Button` in `cmd/vice/ui.go` inside the "Speech to Text" collapsing header, between the microphone selector and the Whisper model dropdown:

```go
imgui.Text("Mode:")
imgui.SameLine()
modeLabel := "Conventional###freqMode"
if config.RealisticFrequencyManagement {
    modeLabel = "Realistic###freqMode"
}
if imgui.Button(modeLabel) {
    config.RealisticFrequencyManagement = !config.RealisticFrequencyManagement
}
if imgui.IsItemHovered() {
    imgui.SetTooltip("Click to toggle.\n" +
        "Conventional: bare FC works; unknown frequencies still route; readback always full position+freq.\n" +
        "Realistic: frequencies required and strict; readback varies by facility.")
}
```

- `###freqMode` stable-ID suffix keeps imgui state consistent as the visible label flips.
- Button width auto-sizes to the longer label ("Conventional") so clicks don't reflow the row.
- Change takes effect instantly — `config.RealisticFrequencyManagement` is re-read every command dispatch.

## Data model and touch points

| File | Change |
|---|---|
| `cmd/vice/config.go` | `RealisticFrequencyManagement bool` on `Config`. Zero value = Conventional. |
| `sim/errors.go` | `ErrUnknownAircraft`, `ErrAlreadyOnFrequency`, `ErrUnknownFrequency` (if not already defined). |
| `sim/control.go` | New `Sim.Guard(tcw, callsign, trailing)` method. New helper `resolveGuardTarget(tcw, trailing) (*av.Controller, av.Frequency, error)`. |
| `sim/control.go` | `runOneControlCommand` gains `fromGuard bool` parameter, plumbed through every intent-emitting path. When set, skip `enqueuePilotTransmission` and readback rendering. |
| `sim/control.go` `runOneControlCommand` front-matter | Before the case switch, detect leading `GUARD` token. Compute target freq via remaining tokens (FC-prefixed or user's freq), verify precondition, switch aircraft freq, recurse into remaining tokens with `fromGuard = true` (or return if none). |
| `sim/control.go` FC handler (~line 4325) | Branch on `Sim.State.RealisticFrequencyManagement`: Conventional = bare FC always falls back to `ContactTrackingController`; unknown freq routes silently. |
| `aviation/intent.go` `ContactIntent` render | Conventional → force `SameFacility = false` so readback is always position+freq. |
| `stt/handlers.go` (~line 1260) | Four new patterns per §STT grammar. Pre-pass: if transcription contains `guard` keyword, run guard-pattern table with elevated priority. |
| `stt/handlers.go` pattern table (non-guard) | Conventional mode registers legacy "contact approach/departure/center" patterns (no freq); Realistic keeps today's stricter set. |
| `stt/typeparsers.go` | Optional `acknowledge with ident` tail parser (shared helper). |
| `cmd/vice/ui.go` Speech-to-Text section | Mode toggle button per §Settings UI. |

**Mode visibility in sim:** `Config.RealisticFrequencyManagement` propagates into `SimConfig` at scenario start (same path other client-side settings use). `Sim.State.RealisticFrequencyManagement` mirrors it so sim-side branches have a local read.

## Error surface summary

| Condition | Handling |
|---|---|
| Typed `GUARD` with unknown callsign | `ErrUnknownAircraft` |
| `GUARD` target equals current freq | `ErrAlreadyOnFrequency` |
| `GUARD FC {freq}` where `{freq}` has no matching controller, Realistic | `ErrUnknownFrequency` — command rejected, aircraft not transferred. |
| `GUARD FC {freq}` where `{freq}` has no matching controller, Conventional | Silent transfer to the aircraft's current tracking controller (matches non-guard Conventional FC behavior). |
| STT transmission has `guard` + freq but no matching callsign | No-op; log entry |
| Conventional mode + bare `FC` non-cleared | Hands off to tracking controller (success) |
| Realistic mode + bare `FC` non-cleared | `ErrInvalidCommandSyntax` |

## Testing

### Unit tests

| Area | Test |
|---|---|
| Mode: FC bare | Conventional + bare `FC` on non-cleared aircraft → `ContactIntent` to tracking controller. Realistic + same → `ErrInvalidCommandSyntax`. |
| Mode: FC unknown freq | Conventional + `FC 135000` (not in scenario) → `ContactIntent` to tracking controller. Realistic + same → `UnknownFrequencyIntent`. |
| Mode: readback style | Conventional + same-facility handoff → readback contains `"contact"` + position token. Realistic + same → readback is freq-only. |
| Mode: typed still full-readback | Realistic + typed same-facility → full position+freq (typed discipline wins). |
| GUARD: bare | `SWA123 GUARD` → `ac.ControllerFrequency` becomes user's TCP, no intent returned, no transmission queued. |
| GUARD: with FC redirect | `SWA123 GUARD FC 134050` → `ac.ControllerFrequency` becomes the controller at 134.050, no transmission queued. |
| GUARD: with ID | `SWA123 GUARD ID` → aircraft switches + IDENTs (verify transponder state), no verbal readback. |
| GUARD: already on target | `SWA123 GUARD` when `ac.ControllerFrequency` already equals user's → `ErrAlreadyOnFrequency`. |
| GUARD: unknown callsign | `ZZZ999 GUARD` → `ErrUnknownAircraft`. |
| GUARD: trailing non-freq command | `SWA123 GUARD FH 270` → switches to user's freq AND new heading, no verbal readback. |
| STT guard keyword | Transcription without "guard" + "SWA123 contact me immediately on 128.375" → normal `FC` (not GUARD). With "guard" present → emits `GUARD FC 128375`. |
| STT guard redirect | "…this is orlando approach on guard, SWA123 contact orlando approach on 134.05" → `SWA123 GUARD FC 134050:orlando approach`. |
| STT guard + IDENT tail | "…acknowledge with IDENT" suffix appends ` ID`. |
| STT no callsign match | Guard transmission with freq but no callsign → no-op, log entry. |

### Integration tests

- Full config round-trip: write `RealisticFrequencyManagement = true` to JSON, reload, verify flag set. Confirm bool re-reads per command (flip mid-session and verify behavior changes).
- Full STT flow: synthesized transcription → `runOneControlCommand` → aircraft state change, for each guard pattern row.

### Manual verification (post-merge, user-side)

- Toggle the Settings button, observe label flips between "Conventional" and "Realistic" without reflowing layout.
- Speak the two example phrasings and verify target aircraft switches silently.
- `TG SWA123 GUARD` against a tracked aircraft already on your freq → see error message.
- `TG SWA123 GUARD FC 134050` redirects to the right controller.

## Out of scope

- **Real 121.5 guard channel simulation.** No separate radio channel, no "who can hear guard" modeling, no audio artifacts reaching other controllers.
- **Position-described aircraft identification** ("skyhawk 10 miles north of orlando executive"). Callsign required.
- **Multi-aircraft guard broadcasts.** One callsign per transmission.
- **"Attention all aircraft" framing.** The phrase is ignored by the grammar; detection hinges purely on the `guard` keyword plus a callsign and freq.
- **Aircraft refusing or mis-acknowledging guard.** The switch is deterministic; no "lost aircraft never heard it" behavior.
- **Mode transitions mid-command.** Toggling between dispatch and readback doesn't alter an in-flight readback — mode is read once at dispatch.
- **Separate "guard monitoring" UI.** No new pane, no guard-traffic log.
- **Server→client mode broadcast.** Config is client-local; in multiplayer each controller has their own mode.
