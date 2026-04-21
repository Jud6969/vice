# Leaving/Reaching Altitude Conditional Commands

**Issue:** [mmp/vice#438](https://github.com/mmp/vice/issues/438)
**Branch:** `leaving-reaching-commands` (branched from `upstream/master@65adaa0c`)
**Date:** 2026-04-20

## Summary

Add controller-issued conditional commands that defer an action until the aircraft's altitude crosses a specified trigger:

- `LV{alt}/{inner}` — "leaving {alt}, do {inner}". Example: `LV30/H010` → "leaving 3,000, fly heading 010".
- `RC{alt}/{inner}` — "reaching {alt}, do {inner}". Example: `RC100/DAAC` → "reaching 10,000, direct AAC".

This is the controller-issued counterpart to issue #48 (which added the scenario-time, waypoint-gated version). The keyboard syntax follows the existing `A{fix}/{inner}` precedent.

## Motivation

Real-world ATC routinely uses phraseology like "leaving three thousand, turn left heading zero one zero" to chain a lateral maneuver to an altitude event. Vice has no way to express this today; controllers must watch the altitude themselves and issue the heading change manually.

## Design decisions

| Decision | Choice |
|---|---|
| Inner command set | Closed set of typed actions (H/L/R/LD/RD/D/S/M). Matches the `A{fix}/...` precedent. |
| `LV` trigger semantics | Fires once altitude is ≥50 ft past trigger in the direction of current vertical motion. Direction-agnostic — works whether climbing or descending through. |
| `RC` trigger semantics | Fires on first contact within 100 ft of target, regardless of vertical rate. |
| Invalid trigger at issue time | Reject with error. Trigger must be reachable (within a 500 ft band of current altitude, or lie between current altitude and assigned target). |
| Multiple pending slots | Single slot per aircraft. A new `LV`/`RC` replaces the prior one. |
| Readback | Full readback: "leaving three thousand, fly heading zero one zero". |
| Trigger firing | Silent execution. No radio transmission when the action fires. |
| STT voice support | Included v1. Voice grammar registers patterns for each trigger × supported inner combination. |
| Handoff / frequency change | Pending slot persists across handoffs. Matches `RR{alt}` behavior. |

## Architecture

### Data model

One new field on `Nav`, a `ConditionalAction` interface with one concrete type per supported inner command, and a `ConditionalCommandIntent` for the readback.

```go
// nav/nav.go
type ConditionalKind uint8

const (
    ConditionalLeaving ConditionalKind = iota
    ConditionalReaching
)

type ConditionalAction interface {
    Execute(nav *Nav, simTime Time)
    Render(rt *av.RadioTransmission, r *rand.Rand)
}

type ConditionalHeading struct {
    Heading   int
    Turn      av.TurnMethod // TurnClosest/Left/Right
    ByDegrees int           // nonzero for LnnD / RnnD
}
type ConditionalDirectFix struct {
    Fix  string
    Turn av.TurnMethod // TurnClosest / Left / Right
}
type ConditionalSpeed struct { Restriction av.SpeedRestriction }
type ConditionalMach  struct { Mach float32 }

type PendingConditionalCommand struct {
    Kind     ConditionalKind
    Altitude float32           // feet MSL
    Action   ConditionalAction
}

type Nav struct {
    // ... existing fields ...
    PendingConditionalCommand *PendingConditionalCommand
}
```

Slot is cleared when:
- Trigger fires (after the action executes).
- A new `LV`/`RC` command is issued (replaces).

Slot is **not** cleared on new altitude assignment, heading change, speed change, approach clearance, handoff, or frequency change.

### Supported inner commands

| Token | Action type | Example |
|---|---|---|
| `H{hdg}` | `ConditionalHeading{Turn=TurnClosest}` | `LV30/H010` |
| `L{hdg}` / `R{hdg}` | `ConditionalHeading{Turn=Left/Right}` | `LV130/R100` |
| `L{deg}D` / `R{deg}D` | `ConditionalHeading{ByDegrees=N, Turn=Left/Right}` | `LV30/L20D` |
| `D{fix}` | `ConditionalDirectFix{Turn=TurnClosest}` | `RC100/DAAC` |
| `LD{fix}` / `RD{fix}` | `ConditionalDirectFix{Turn=Left/Right}` | `RC100/LDAAC` |
| `S{spd}` | `ConditionalSpeed{...}` | `RC50/S210` |
| `M{mach}` | `ConditionalMach{...}` | `RC350/M78` |

Altitude-changing inner commands (`C`, `CVS`, `DVS`, `ED`, etc.) are explicitly rejected by the parser's default branch. No separate check.

## Command parsing & dispatch

In `sim/control.go`, `runOneControlCommand`:

**Case `'L'`** — add a new branch before the existing `LD<fix>` / `L<deg>D` / `L<hdg>` branches:

```go
if strings.HasPrefix(command, "LV") && len(command) > 2 {
    altStr, inner, ok := strings.Cut(command[2:], "/")
    if !ok || inner == "" { return nil, ErrInvalidCommandSyntax }
    alt, err := parseConditionalAltitude(altStr)
    if err != nil { return nil, err }
    action, err := parseConditionalAction(inner)
    if err != nil { return nil, err }
    return s.AssignConditional(tcw, callsign, ConditionalLeaving, alt, action)
}
```

**Case `'R'`** — analogous branch for `RC`, placed before the existing `RR` branch to avoid accidental fallthrough.

**Altitude encoding** — reuse the `RR{alt}` convention:

```go
func parseConditionalAltitude(s string) (float32, error) {
    n, err := strconv.Atoi(s)
    if err != nil { return 0, err }
    if n > 600 && n%100 == 0 { n /= 100 }
    return float32(n * 100), nil
}
```

**Inner parser** — `parseConditionalAction(inner string) (ConditionalAction, error)`:
Switch on `inner[0]` ∈ {H, L, R, D, S, M}; each branch validates its sub-grammar and returns the corresponding concrete `ConditionalAction`. Default branch returns `ErrInvalidCommandSyntax`, which naturally rejects altitude-changing inner commands.

**Sim entry point:**

```go
func (s *Sim) AssignConditional(tcw TCW, callsign av.ADSBCallsign,
    kind ConditionalKind, altitude float32, action ConditionalAction) (av.CommandIntent, error) {

    s.mu.Lock(s.lg); defer s.mu.Unlock(s.lg)

    return s.dispatchControlledAircraftCommand(tcw, callsign,
        func(tcw TCW, ac *Aircraft) av.CommandIntent {
            if !triggerReachable(ac, kind, altitude) {
                return nil // caller treats nil-intent as "unable"
            }
            ac.Nav.PendingConditionalCommand = &PendingConditionalCommand{
                Kind: kind, Altitude: altitude, Action: action,
            }
            return av.ConditionalCommandIntent{
                Kind: kind, Altitude: altitude, Action: action,
            }
        })
}
```

**Reachability validation:**

```go
func triggerReachable(ac *Aircraft, kind ConditionalKind, trigger float32) bool {
    cur := ac.Altitude()
    target := ac.Nav.Altitude.Assigned
    switch kind {
    case ConditionalLeaving:
        if math.Abs(cur-trigger) <= 500 { return true }
        if target == nil { return false }
        return between(trigger, cur, *target)
    case ConditionalReaching:
        if target == nil { return math.Abs(cur-trigger) <= 500 }
        return between(trigger, cur, *target)
    }
    return false
}
```

The 500 ft slack on `LV` handles "aircraft is at 3,050 climbing, controller says leaving 3,000" — shouldn't reject.

## Trigger evaluation & firing

In `sim.updateState` (adjacent to the existing `ReportReachingAltitude` check):

```go
if pc := ac.Nav.PendingConditionalCommand; pc != nil && ac.IsAssociated() {
    if conditionalTriggered(ac, pc) {
        action := pc.Action
        ac.Nav.PendingConditionalCommand = nil // clear BEFORE execute to prevent re-entry
        action.Execute(&ac.Nav, s.State.SimTime)
    }
}
```

**Trigger predicate:**

```go
func conditionalTriggered(ac *Aircraft, pc *PendingConditionalCommand) bool {
    alt := ac.Altitude()
    diff := alt - pc.Altitude
    switch pc.Kind {
    case ConditionalLeaving:
        // Fires once altitude is >50 ft past trigger in the direction of current vertical motion.
        return math.Abs(diff) > 50 &&
               sameSign(diff, ac.Nav.FlightState.AltitudeRate)
    case ConditionalReaching:
        // First contact within 100 ft, regardless of vertical rate.
        return math.Abs(diff) <= 100
    }
    return false
}
```

The 50 ft `LV` threshold prevents firing on level-flight altitude noise. The 100 ft `RC` threshold matches the existing `RR{alt}` tolerance.

**Action execution** — each concrete `ConditionalAction.Execute` calls the corresponding existing `Nav` method directly (`AssignHeading`, `DirectFix`, `AssignSpeed`, `AssignMach`). Because execution bypasses `runOneControlCommand`, no readback transmission is generated — silent execution is free.

## Readback rendering

```go
// aviation/intent.go
type ConditionalCommandIntent struct {
    Kind     ConditionalKind
    Altitude float32
    Action   ConditionalAction
}

func (c ConditionalCommandIntent) Render(rt *RadioTransmission, r *rand.Rand) {
    switch c.Kind {
    case ConditionalLeaving:
        rt.Add("[leaving|passing] {alt}, ", c.Altitude)
    case ConditionalReaching:
        rt.Add("[reaching|level at|on reaching] {alt}, ", c.Altitude)
    }
    c.Action.Render(rt, r)
}
```

Each `ConditionalAction.Render` emits only the action fragment (e.g., "fly heading 010", "direct AAC", "reduce speed to 210"), reusing the existing phraseology vocabulary. Concrete implementations draw on patterns from `AltitudeIntent`, `HeadingIntent`, `SpeedIntent`, `DirectFixIntent`.

Example readbacks:

| Command | Rendered |
|---|---|
| `LV30/H010` | "leaving three thousand, fly heading zero one zero" |
| `LV130/R100` | "passing one three thousand, right heading one zero zero" |
| `RC100/DAAC` | "reaching one zero thousand, direct alpha alpha charlie" |
| `RC50/S210` | "reaching five thousand, slowing to two one zero" |

No `PendingTransmission*` type is added — trigger firing is silent by design.

## STT grammar

Voice support in `stt/handlers.go` — register one pattern per `(trigger × inner)` combination. The LV/RC trigger prefix is bolted onto each inner command's grammar fragment.

**Trigger phrases** (alternation):

- `LV`: "leaving {alt}" / "passing {alt}"
- `RC`: "reaching {alt}" / "level at {alt}" / "on reaching {alt}"

**Inner phrases** — the established vocabulary for each supported inner command (from existing STT handlers):

- `H{hdg}`: "fly heading {hdg}"
- `L{hdg}` / `R{hdg}`: "turn left|right heading {hdg}"
- `L{deg}D` / `R{deg}D`: "turn left|right {deg} degrees"
- `D{fix}`: "direct {fix}" / "proceed direct {fix}"
- `LD{fix}` / `RD{fix}`: "turn left|right direct {fix}"
- `S{spd}`: "maintain {spd}" / "reduce speed to {spd}" / "speed {spd}"
- `M{mach}`: "maintain mach {mach}" / "mach {mach}"

**Implementation approach** — loop programmatically over the inner set, registering one `stt` command per pair:

```go
for _, inner := range innerPatterns {
    registerSTTCommand(
        fmt.Sprintf("leaving|passing {altitude}, %s", inner.Grammar),
        func(alt int, args ...any) string {
            return fmt.Sprintf("LV%d/%s", alt, inner.ToCommand(args))
        },
        WithName("conditional_leaving_" + inner.Name),
        WithPriority(11),
    )
    // analogous for reaching
}
```

Exact framework fit (named rules vs. inline expansion) to be confirmed during implementation — the STT framework may need a small accommodation. The fallback of N inline registrations is acceptable.

**Priority tuning:** set distinct priorities so "reaching {alt}" (for the new RC command) doesn't fuzzy-match with "report reaching {alt}" (the existing RR command). The existing `say<->stop` precedent from commit `3caf4fac` shows the pattern.

## Testing

### Unit tests (`nav` package)

- Trigger predicate truth table for each `ConditionalKind` (climbing through, descending through, level at, level below, within-tolerance noise).
- Supersession behavior (new slot replaces prior).
- Persistence across synthetic handoff state transitions.
- `Execute` correctness per concrete action type — assert mutation matches direct nav-method call.

### Sim-layer tests (`sim/control_test.go`)

- Parse-and-install happy path, one row per `(trigger, inner)` combination.
- Parser rejections: altitude-changing inner, malformed altitude, empty inner, unknown inner prefix, missing slash.
- Unreachable-trigger rejections for each kind.
- Readback render round-trip for each action type.

### End-to-end tests (`sim/e2e_test.go`)

- `LV` scenario: aircraft climbing through trigger altitude; assert heading changes at the correct tick with no extra radio transmission at fire time.
- `RC` scenario: aircraft reaching target altitude; assert direct-fix installed, slot cleared, silent fire.

### STT tests (`stt/handlers_test.go`)

- One happy-path per registered voice pattern.
- Adversarial fuzzy-match guards — specifically verify "reaching {alt}" does not fire the `RR` command path and vice versa.

### Regression hygiene

- `go test ./sim/... ./nav/... ./stt/... ./aviation/...` must pass at every intermediate commit.
- No existing tests modified; this is pure addition.

## Out of scope

- Queuing multiple pending LV/RC actions per aircraft (single-slot only).
- Altitude-changing inner commands.
- Conditional commands triggered by events other than altitude (speed, time, position) — those exist as separate grammars (`A{fix}/...`, speed-until).
- Compound inner commands like speed-until (`S250/UFIX1/210`) nested inside LV/RC.
