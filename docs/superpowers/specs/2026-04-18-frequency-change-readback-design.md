# Frequency Change & Contact Tower Revamp — Design

Branch target: `tower-handoff-readback-variations`
Date: 2026-04-18

## Motivation

The current `FC` (frequency change) and `TO` (contact tower) commands take no frequency argument and dispatch implicitly to the tracking controller or tower. The STT pipeline accepts "contact approach 127.75" but discards the spoken frequency. Pilot readbacks are stylistically flat and never reflect whether the handoff crosses facility boundaries.

This revamp makes frequency an explicit argument, routes the handoff to the controller matching that frequency, adds STT grammar that preserves the frequency, and makes pilot readback format depend on whether the hand-off crosses facility boundaries.

## Commands

| Command | Argument | Behavior |
|---|---|---|
| `FC {freq}` | required | Frequency change. Resolves `{freq}` to a controller across all scenario-loaded controllers. |
| `TO` | (bare) | Contact tower. Allowed iff the aircraft's arrival airport has exactly one tower controller. |
| `TO {freq}` | — | Contact tower with explicit frequency. Required when arrival airport has multiple towers. |

Bare `FC` (today's implicit form) is removed.

### Frequency argument format

Typed and spoken both accepted:
- **Typed:** 5 or 6 digits (e.g. `127750`, `12775`). 5-digit form appends an implicit trailing zero.
- **Spoken:** `NNN point NN` (e.g. "one two seven point seven five") or `NNN point NNN`. Also accepted as 5- or 6-digit integer without the "point" token.
- Valid VHF aeronautical band: `118000`–`137000` kHz. Out of range → `UnknownFrequencyIntent` (see §Resolution).

All forms resolve to an `aviation.Frequency` (int, kHz ×1000).

## Tower resolution

Towers are identified by callsign convention: `^<AIRPORT>(_[A-Z]+)?_TWR$` matched against `FacilityConfig.ControlPositions` (plus neighbor controllers). No new JSON field is added.

For a given aircraft, the "arrival airport" is `ac.FlightPlan.ArrivalAirport`. Tower-count for that airport drives bare-`TO` legality:

| Tower count | Bare `TO` | `TO {freq}` |
|---|---|---|
| 0 | command error | command error |
| 1 | allowed (position-only intent) | allowed (must match the one tower's freq) |
| ≥2 | command error (ambiguous) | allowed (must match one of the towers' freqs) |

## Frequency resolution (shared by FC and TO)

New helper on `Sim`:

```go
func (s *Sim) resolveControllerByFrequency(ac *Aircraft, freq Frequency, positionHint string) (*Controller, error)
```

### Lookup pool

All controllers known to the scenario — `s.State.Controllers`. This includes:
- Local facility positions (from `FacilityConfig.ControlPositions`)
- Neighbor controllers loaded via `loadNeighborControllers`
- Virtual ARTCC positions

Filter to `Frequency == freq`.

### Outcomes

| Match count | Command layer | Aircraft intent | Aircraft state |
|---|---|---|---|
| 0 | **success** (not an error) | `UnknownFrequencyIntent{freq}` | unchanged (no `GotContactTower`, no freq change) |
| 1 | success | `ContactIntent` / `ContactTowerIntent` | updated |
| ≥2 | success after layered tiebreaker | same as above; warning logged if still ambiguous | updated |

Bare `TO` with 0 or ≥2 tower matches is a **command error** (not a `UnknownFrequencyIntent`): the controller must fix their input. Only explicit `{freq}` that misses the pool produces the pilot-side "say again?" behavior.

`TO {freq}` resolving to a non-tower controller → command error (scenario/authoring mistake).

### Layered tiebreaker

Applied only when multiple controllers share the frequency. Each layer filters candidates; if the filter empties the set, skip it instead of erroring.

1. **D1 — Name hint:** if `positionHint != ""` (set by STT, empty for typed), keep only candidates whose `RadioName` or `Callsign` contains the hint tokens (case-insensitive, whitespace-normalized).
2. **D2 — Facility adjacency:** prefer candidates whose `Facility` equals the aircraft's current controller's `Facility`, then those in its `HandoffIDs` neighbor list.
3. **D3 — Phase of flight:** for cleared-for-approach aircraft or those within ~20 nm of arrival airport, prefer candidates whose callsign matches `<ArrivalAirport>_*`. For enroute aircraft, prefer `ERAMFacility == true`.
4. **Deterministic fallback:** sort remaining by `Callsign` ascending, take first. Log a warning with the full candidate list (scenario authoring signal).

Typed `FC {freq}` uses the same resolution — no stricter ambiguity error. The human controller typed a specific number; the heuristic picks the most plausible target and logs if the choice was non-unique.

## Intent data model

`aviation/intent.go`:

```go
type ContactIntent struct {
    Type         ContactIntentType
    ToController *Controller
    Frequency    Frequency
    IsDeparture  bool
    SameFacility bool  // NEW
}

type ContactTowerIntent struct {
    ToController *Controller  // NEW — nil when position-only
    Frequency    Frequency    // NEW — 0 when position-only
    PositionOnly bool         // NEW
}

type UnknownFrequencyIntent struct {  // NEW
    Frequency Frequency
}
```

`SameFacility` is stamped at intent construction time:

```go
same := fromController != nil && toController != nil &&
        fromController.Facility == toController.Facility
```

## Typed-command vs STT readback discipline

When a command is invoked via the typed STARS path, readback is **always** position + frequency regardless of actual facility match. Implementation:

```go
intent.SameFacility = !fromTypedCommand && actualSame
```

When invoked via STT, `SameFacility` reflects reality.

Bare `TO` (STT only) produces `ContactTowerIntent{PositionOnly: true}` → position-only readback (the sole case where the pilot omits frequency entirely).

## STT grammar

File: `stt/handlers.go`. New typed parser `{frequency}` in `stt/typeparsers.go` replaces the current discard-only `{contact_frequency}`.

### `{frequency}` parser

Scans up to 10 tokens. Accepts:
- `NNN point NN` (5 spoken digits, trailing zero implicit)
- `NNN point NNN` (6 spoken digits, explicit)
- `NNNNN` (5 contiguous digits, no "point", trailing zero implicit)
- `NNNNNN` (6 contiguous digits, no "point")

Returns `Frequency` (int). Rejects values outside 118000–137000.

### Pattern table

| Priority | Pattern | Emits | Notes |
|---|---|---|---|
| 20 | `contact {position_text} tower {frequency}` | `TO {freq}` | position_text → hint |
| 19 | `over to {position_text} tower {frequency}` | `TO {freq}` | |
| 18 | `contact {position_text} tower` | `TO` (bare) | requires single tower at arrival airport |
| 16 | `contact tower {frequency}` | `TO {freq}` | |
| 15 | `contact tower` | `TO` (bare) | |
| 12 | `contact {position_text} {frequency}` | `FC {freq}` | position_text → hint |
| 11 | `over to {position_text} {frequency}` | `FC {freq}` | |
| 10 | `contact approach\|departure\|center\|ground\|clearance\|ramp {frequency}` | `FC {freq}` | |
| 9 | `contact {frequency}` | `FC {freq}` | no hint |

Removed: today's priority-3 `contact approach|departure|center` without freq (FC now requires freq). Removed: today's priority-4 `contact {contact_frequency}` (replaced by priority-9 with real parser).

### Position hint plumbing

The STT layer passes `position_text` to the command as a separate field. Options considered:
- Append as third token in the command string (e.g. `FC 127750 "orlando approach"`) — messy for typed users.
- Add optional `Hint string` field to the command dispatch context — cleaner.

Chosen: dispatch-context field. Typed commands send empty hint.

## Readback render grammar

`aviation/intent.go`:

**`ContactIntent`**
- `SameFacility == true` → `"[|that's |]{freq}, [good day|seeya|thanks|]"` — frequency-only
- `SameFacility == false` → `"[contact|over to] {actrl} on {freq}, [good day|seeya|thanks|]"` — position + frequency

**`ContactTowerIntent`**
- `PositionOnly == true` → `"[contact|over to] tower"` (unchanged)
- `PositionOnly == false` → `"[contact|over to] {actrl} on {freq}, [good day|seeya|]"`

**`UnknownFrequencyIntent`**
- `"[what was that frequency?|we hear nothing on {freq}, what was the frequency?|say again the frequency?|nothing heard on {freq}, say again?]"`

### Frequency formatter

New method `Frequency.StringSpoken()` or similar:
- Render as `NNN.DDD`
- Trim trailing zeros from the fractional part, keeping at least one fractional digit
- Examples:
  - `127750` → `"127.75"`
  - `118300` → `"118.3"`
  - `134000` → `"134.0"`
  - `127755` → `"127.755"`

The speech-synthesis layer converts this to spoken digits.

## Call-site changes

| File:line | Change |
|---|---|
| `sim/control.go:2056` (`Sim.ContactTower`) | Accepts `freq Frequency` (0 = bare), `target *Controller`. Passes into aircraft-side intent. |
| `sim/control.go:3934` (FC handler) | No longer conditions on approach-cleared. Resolves `{freq}` → controller. If resolved is a tower and `Approach.Cleared` → route through tower path; else set tracking controller explicitly. |
| `sim/control.go:4141` (second ContactTower call site) | Updated signature. |
| `sim/aircraft.go:469` (`Aircraft.ContactTower`) | Accepts target + freq, populates `ContactTowerIntent` fields. Preserves cleared-for-approach gating. |
| `stt/handlers.go:~1260` | Pattern table replaced per §STT grammar. |
| `stt/typeparsers.go:~947` | `contactFrequencyParser` → `frequencyParser` returning `Frequency`. |
| `aviation/intent.go:~791` | `ContactIntent` gains `SameFacility` field (`ToController` and `Frequency` already exist). |
| `aviation/intent.go:~850` | `ContactTowerIntent` gains fields per §Intent data model. |
| `aviation/intent.go` | New `UnknownFrequencyIntent` + render template. |
| `aviation/aviation.go:~74` | `Frequency.StringSpoken()` helper. |
| `sim/errors.go` | New `ErrNoTowerForAirport`, `ErrAmbiguousTower`, `ErrFrequencyNotTower`. |

## Error surface summary

| Condition | Handling |
|---|---|
| Typed `FC` with no arg | Parse error (existing command-parsing layer) |
| Typed `TO` with no arg, airport has 0 or ≥2 towers | Command error |
| `{freq}` not in scenario | `UnknownFrequencyIntent` (success) |
| `TO {freq}` resolves to non-tower | Command error |
| `{freq}` out of VHF range | `UnknownFrequencyIntent` (success) |

## Testing

- Unit: `{frequency}` parser across all four input shapes (edge cases: `118.0`, `136.975`, `12775` → 127750, `127755` → 127755).
- Unit: tower-count logic per airport across representative scenarios (PCT with multi-tower IAD, single-tower DCA/DAA; P50 with PHX_T/PHX_E).
- Unit: layered tiebreaker — craft a scenario with two controllers on the same freq, verify D1/D2/D3 each win in isolation.
- Unit: `Frequency.StringSpoken()` — 127750, 118300, 134000, 127755, 118000.
- Integration: STT phrase → command → intent for each pattern row in the table.
- Integration: bare `TO` at single-tower airport succeeds; at multi-tower errors; at no-tower errors.
- Integration: typed `FC {freq}` always produces full position+freq readback even when same facility.
- Integration: unknown frequency produces `UnknownFrequencyIntent` and aircraft does not commit state.

## Out of scope

- No changes to the 3- or 4-digit shortform frequency (e.g. "seven five" for 127.75) — too ambiguous.
- No explicit `role: "tower"` JSON field; staying with callsign convention.
- No changes to `ContactTrackingController` semantics beyond the call-site plumbing.
- No changes to DCB, STARS display, or UI outside the command and readback paths.
