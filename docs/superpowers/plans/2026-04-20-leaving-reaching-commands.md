# Leaving/Reaching Altitude Conditional Commands Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `LV{alt}/{inner}` (leaving) and `RC{alt}/{inner}` (reaching) controller commands that defer a lateral/speed/mach action until the aircraft crosses a trigger altitude.

**Architecture:** Closed-set typed dispatch mirroring the existing `A{fix}/{inner}` pattern. A new `ConditionalAction` interface in `nav` with one concrete type per supported inner (heading, direct-fix, speed, mach). A single `PendingConditionalCommand` slot on `Nav`; `sim.updateState` fires it silently when the trigger condition is met.

**Tech Stack:** Go 1.x, existing vice sim/nav/aviation/stt packages. `go test ./...` for verification.

**Spec:** `docs/superpowers/specs/2026-04-20-leaving-reaching-commands-design.md`

---

## File map

**New files:**
- `nav/conditional.go` — `ConditionalKind`, `ConditionalAction` interface, concrete action types, `conditionalTriggered` predicate
- `nav/conditional_test.go` — unit tests for actions and trigger predicate

**Modified files:**
- `nav/nav.go` — add `PendingConditionalCommand *PendingConditionalCommand` field to `Nav`
- `aviation/intent.go` — add `ConditionalCommandIntent` near the other special intents
- `sim/control.go` — add `parseConditionalAltitude`, `parseConditionalAction`, `triggerReachable`, `AssignConditional`; add dispatch branches in cases `'L'` and `'R'`
- `sim/control_test.go` — integration tests for dispatch and rejection paths
- `sim/sim.go` — add trigger check in `updateState`
- `sim/e2e_test.go` — end-to-end tick-through scenarios
- `stt/handlers.go` — register voice patterns for both triggers and each inner command
- `stt/handlers_test.go` — STT happy-path and adversarial tests
- `whatsnew.md` — user-visible changelog entry

**Commit cadence:** one commit per task. Every commit must leave `go test ./sim/... ./nav/... ./stt/... ./aviation/...` green.

---

### Task 1: `ConditionalKind`, `ConditionalAction` interface, `PendingConditionalCommand` struct, Nav field

**Files:**
- Create: `nav/conditional.go`
- Create: `nav/conditional_test.go`
- Modify: `nav/nav.go` (add one field inside `Nav`)

- [ ] **Step 1: Write the failing test**

Create `nav/conditional_test.go`:

```go
package nav

import (
	"testing"
)

func TestNavHasPendingConditionalCommandField(t *testing.T) {
	var n Nav
	if n.PendingConditionalCommand != nil {
		t.Fatalf("PendingConditionalCommand should default to nil, got %+v", n.PendingConditionalCommand)
	}
	n.PendingConditionalCommand = &PendingConditionalCommand{
		Kind:     ConditionalLeaving,
		Altitude: 3000,
	}
	if n.PendingConditionalCommand.Kind != ConditionalLeaving {
		t.Fatalf("expected ConditionalLeaving, got %d", n.PendingConditionalCommand.Kind)
	}
	if n.PendingConditionalCommand.Altitude != 3000 {
		t.Fatalf("expected 3000, got %v", n.PendingConditionalCommand.Altitude)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./nav/... -run TestNavHasPendingConditionalCommandField -v`
Expected: FAIL — undefined `PendingConditionalCommand`, `ConditionalLeaving`.

- [ ] **Step 3: Create `nav/conditional.go`**

```go
// nav/conditional.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package nav

import (
	"math/rand/v2"

	av "github.com/mmp/vice/aviation"
)

// ConditionalKind identifies which altitude-event triggers the deferred action.
type ConditionalKind uint8

const (
	// ConditionalLeaving fires once the aircraft's altitude has passed the
	// trigger by more than a small tolerance in the direction of current
	// vertical motion.
	ConditionalLeaving ConditionalKind = iota

	// ConditionalReaching fires on first contact within 100 ft of the trigger
	// altitude, regardless of vertical rate.
	ConditionalReaching
)

// ConditionalAction is the deferred action to execute when a LV/RC trigger
// fires. Concrete types cover the closed set of supported inner commands
// (heading, direct-fix, speed, mach).
type ConditionalAction interface {
	// Execute mutates nav to carry out the deferred action. Called with the
	// PendingConditionalCommand slot already cleared, so re-entry is safe.
	Execute(nav *Nav, simTime Time)

	// Render emits the action-specific readback fragment (e.g., "fly heading
	// 010") used inside ConditionalCommandIntent.
	Render(rt *av.RadioTransmission, r *rand.Rand)
}

// PendingConditionalCommand is the single slot on Nav that stores a
// deferred LV/RC action. A new LV/RC command supersedes any prior slot;
// successful trigger firing clears it.
type PendingConditionalCommand struct {
	Kind     ConditionalKind
	Altitude float32 // feet MSL
	Action   ConditionalAction
}
```

- [ ] **Step 4: Add the field to `Nav` in `nav/nav.go`**

In `nav/nav.go`, add the following field to the `Nav` struct near the existing `ReportReachingAltitude` field:

```go
	// PendingConditionalCommand stores a single deferred LV/RC action
	// (e.g., "leaving 3,000, fly heading 010"). Cleared when the trigger
	// fires or when a new LV/RC command is installed. Not cleared on
	// new altitude/heading/speed assignments or on handoff.
	PendingConditionalCommand *PendingConditionalCommand
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./nav/... -run TestNavHasPendingConditionalCommandField -v`
Expected: PASS.

Also run the full nav suite to verify no regression: `go test ./nav/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add nav/conditional.go nav/conditional_test.go nav/nav.go
git commit -m "nav: add ConditionalAction interface and PendingConditionalCommand slot"
```

---

### Task 2: `ConditionalHeading` with Execute and Render

**Files:**
- Modify: `nav/conditional.go`
- Modify: `nav/conditional_test.go`

- [ ] **Step 1: Write the failing test**

Append to `nav/conditional_test.go`:

```go
import (
	// ...existing imports...
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
	"math/rand/v2"
)

func TestConditionalHeadingExecuteClosest(t *testing.T) {
	// Aircraft flying a heading; conditional heading assigns 010.
	n := makeTestNav(t, 180) // helper: Nav with current heading 180, altitude 2000, etc.
	action := ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	action.Execute(&n, Time{})
	if assigned, ok := n.AssignedHeading(); !ok || assigned != 10 {
		t.Fatalf("expected assigned heading 10, got ok=%v heading=%v", ok, assigned)
	}
}

func TestConditionalHeadingExecuteByDegreesLeft(t *testing.T) {
	n := makeTestNav(t, 180)
	action := ConditionalHeading{ByDegrees: 30, Turn: av.TurnLeft}
	action.Execute(&n, Time{})
	// TurnLeft 30 from 180 -> 150
	if assigned, ok := n.AssignedHeading(); !ok || assigned != 150 {
		t.Fatalf("expected assigned heading 150, got ok=%v heading=%v", ok, assigned)
	}
}

func TestConditionalHeadingRender(t *testing.T) {
	cases := []struct {
		action ConditionalHeading
		want   string // substring in written form
	}{
		{ConditionalHeading{Heading: 10, Turn: av.TurnClosest}, "010"},
		{ConditionalHeading{Heading: 100, Turn: av.TurnRight}, "right"},
		{ConditionalHeading{Heading: 100, Turn: av.TurnLeft}, "left"},
		{ConditionalHeading{ByDegrees: 20, Turn: av.TurnLeft}, "left 20"},
	}
	r := rand.New(rand.NewPCG(1, 2))
	for _, tc := range cases {
		rt := &av.RadioTransmission{}
		tc.action.Render(rt, r)
		written := rt.Written(r)
		if !strings.Contains(strings.ToLower(written), strings.ToLower(tc.want)) {
			t.Errorf("Render(%+v) = %q; want containing %q", tc.action, written, tc.want)
		}
	}
}
```

Add a test helper at the bottom of `nav/conditional_test.go` (or reuse an existing builder if present — search `nav/*_test.go` for `makeTestNav` or `newTestNav`):

```go
func makeTestNav(t *testing.T, heading math.MagneticHeading) Nav {
	t.Helper()
	n := Nav{
		Rand: rand.New(rand.NewPCG(1, 2)),
	}
	n.FlightState.Heading = heading
	n.FlightState.Altitude = 2000
	return n
}
```

If the test builder doesn't compile because `FlightState` lives elsewhere or needs other fields for `AssignHeading` to work, read `nav/commands_test.go` for the existing test-nav-builder pattern and reuse it instead of inventing a new one.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./nav/... -run TestConditionalHeading -v`
Expected: FAIL — `ConditionalHeading` undefined.

- [ ] **Step 3: Implement `ConditionalHeading`**

Append to `nav/conditional.go`:

```go
// ConditionalHeading is a deferred heading assignment. Exactly one of
// Heading or ByDegrees is nonzero:
//   - Heading != 0  → fly (or turn to) the absolute heading.
//   - ByDegrees != 0 → turn N degrees from present heading in the given
//     direction (Turn must be TurnLeft or TurnRight).
type ConditionalHeading struct {
	Heading   int              // 1..360, 0 if unused
	Turn      av.TurnDirection // TurnClosest, TurnLeft, TurnRight
	ByDegrees int              // nonzero for LnnD / RnnD
}

func (c ConditionalHeading) Execute(nav *Nav, simTime Time) {
	if c.ByDegrees != 0 {
		switch c.Turn {
		case av.TurnLeft:
			nav.assignHeading(
				nav.FlightState.Heading.Turn(float32(-c.ByDegrees)),
				av.TurnLeft, simTime, 0)
		case av.TurnRight:
			nav.assignHeading(
				nav.FlightState.Heading.Turn(float32(c.ByDegrees)),
				av.TurnRight, simTime, 0)
		}
		return
	}
	nav.assignHeading(math.MagneticHeading(c.Heading), c.Turn, simTime, 0)
}

func (c ConditionalHeading) Render(rt *av.RadioTransmission, r *rand.Rand) {
	if c.ByDegrees != 0 {
		switch c.Turn {
		case av.TurnLeft:
			rt.Add("[left|turn left] {num} degrees", c.ByDegrees)
		case av.TurnRight:
			rt.Add("[right|turn right] {num} degrees", c.ByDegrees)
		}
		return
	}
	switch c.Turn {
	case av.TurnLeft:
		rt.Add("[left heading|turn left heading] {hdg}", c.Heading)
	case av.TurnRight:
		rt.Add("[right heading|turn right heading] {hdg}", c.Heading)
	default:
		rt.Add("[fly heading|heading] {hdg}", c.Heading)
	}
}
```

Note: `nav.assignHeading` (lowercase, the internal helper at `nav/commands.go:473`) is used directly to avoid the validation in the public `AssignHeading` that we don't need here (validation already happened at command-issue time). `nav.FlightState.Heading.Turn(deg float32)` is the existing heading-math helper.

Verify the helper exists: `grep -n "func (h MagneticHeading) Turn" math/math.go`. If the signature differs, adjust the call.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./nav/... -run TestConditionalHeading -v`
Expected: PASS.

Run: `go test ./nav/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add nav/conditional.go nav/conditional_test.go
git commit -m "nav: add ConditionalHeading action with Execute and Render"
```

---

### Task 3: `ConditionalDirectFix` with Execute and Render

**Files:**
- Modify: `nav/conditional.go`
- Modify: `nav/conditional_test.go`

- [ ] **Step 1: Write the failing test**

Append to `nav/conditional_test.go`:

```go
func TestConditionalDirectFixExecute(t *testing.T) {
	n := makeTestNavWithRoute(t, "AAC") // helper: Nav whose Waypoints contains fix "AAC"
	action := ConditionalDirectFix{Fix: "AAC", Turn: av.TurnClosest}
	action.Execute(&n, Time{})
	// After direct-fix, the first waypoint should be the target fix.
	if len(n.Waypoints) == 0 || n.Waypoints[0].Fix != "AAC" {
		t.Fatalf("expected first waypoint AAC, got %+v", n.Waypoints)
	}
}

func TestConditionalDirectFixRender(t *testing.T) {
	cases := []struct {
		action ConditionalDirectFix
		want   string
	}{
		{ConditionalDirectFix{Fix: "AAC", Turn: av.TurnClosest}, "direct"},
		{ConditionalDirectFix{Fix: "AAC", Turn: av.TurnLeft}, "left"},
		{ConditionalDirectFix{Fix: "AAC", Turn: av.TurnRight}, "right"},
	}
	r := rand.New(rand.NewPCG(1, 2))
	for _, tc := range cases {
		rt := &av.RadioTransmission{}
		tc.action.Render(rt, r)
		written := strings.ToLower(rt.Written(r))
		if !strings.Contains(written, strings.ToLower(tc.want)) {
			t.Errorf("Render(%+v) = %q; want containing %q", tc.action, written, tc.want)
		}
	}
}
```

Add helper `makeTestNavWithRoute` — model it on `makeTestNav` plus whatever `nav/commands_test.go` does to set up a Nav with a named waypoint. If an equivalent helper already exists (e.g., `newNavWithFix`), reuse that.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./nav/... -run TestConditionalDirectFix -v`
Expected: FAIL — undefined `ConditionalDirectFix`.

- [ ] **Step 3: Implement `ConditionalDirectFix`**

Append to `nav/conditional.go`:

```go
// ConditionalDirectFix is a deferred direct-to-fix instruction.
type ConditionalDirectFix struct {
	Fix  string
	Turn av.TurnDirection // TurnClosest, TurnLeft, TurnRight
}

func (c ConditionalDirectFix) Execute(nav *Nav, simTime Time) {
	// Call the internal direct-fix path. The public DirectFix returns an
	// intent we don't need since execution is silent.
	_ = nav.directFix(c.Fix, c.Turn, simTime, 0)
}

func (c ConditionalDirectFix) Render(rt *av.RadioTransmission, r *rand.Rand) {
	switch c.Turn {
	case av.TurnLeft:
		rt.Add("[left direct|turn left direct] {fix}", c.Fix)
	case av.TurnRight:
		rt.Add("[right direct|turn right direct] {fix}", c.Fix)
	default:
		rt.Add("[direct|proceed direct] {fix}", c.Fix)
	}
}
```

If `directFix` (lowercase) doesn't exist as an internal helper, split the public `DirectFix` to carve one out. Verify: `grep -n "func (nav \*Nav) directFix\|func (nav \*Nav) DirectFix" nav/*.go`. The public signature is `DirectFix(fix string, turn av.TurnDirection, simTime Time, delayReduction time.Duration) av.CommandIntent` per `nav/commands.go:647`. If no lowercase internal helper exists, calling the public `DirectFix` and discarding the intent is fine — include a comment explaining that the return value is intentionally discarded because the silent-fire path doesn't read back.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./nav/... -run TestConditionalDirectFix -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add nav/conditional.go nav/conditional_test.go
git commit -m "nav: add ConditionalDirectFix action"
```

---

### Task 4: `ConditionalSpeed` and `ConditionalMach`

**Files:**
- Modify: `nav/conditional.go`
- Modify: `nav/conditional_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `nav/conditional_test.go`:

```go
func TestConditionalSpeedExecute(t *testing.T) {
	n := makeTestNav(t, 180)
	sr := av.MakeExactSpeedRestriction(210)
	action := ConditionalSpeed{Restriction: sr}
	action.Execute(&n, Time{})
	if n.Speed.Assigned == nil {
		t.Fatalf("expected Speed.Assigned set, got nil")
	}
	if got, _ := n.Speed.Assigned.ExactValue(); got != 210 {
		t.Fatalf("expected 210, got %v", got)
	}
}

func TestConditionalMachExecute(t *testing.T) {
	n := makeTestNav(t, 180)
	n.FlightState.Altitude = 30000
	action := ConditionalMach{Mach: 0.78}
	// ConditionalMach.Execute needs a temperature lookup. The production
	// path gets temp from the sim's weather model; for the test we can
	// accept that Execute takes temp from nav.FlightState.Temperature (or
	// whatever the field is). If no such field exists, Execute must be
	// passed temp some other way — adjust the action shape accordingly.
	action.Execute(&n, Time{})
	// Assert the nav state was updated — exact assertion depends on how
	// AssignMach is observable on Nav (probably Speed.Assigned with IsMach
	// set). Inspect nav/commands.go:129 for the surface and assert on it.
}
```

Look at `nav/commands.go:129` for `AssignMach(mach float32, afterAltitude bool, temp av.Temperature) av.CommandIntent` to understand what temperature is expected and which Nav state it mutates. The test assertion should match that surface.

Validate `av.MakeExactSpeedRestriction` exists: `grep -n "func MakeExactSpeedRestriction" aviation/*.go`. If the constructor is named differently (e.g., `NewSpeedRestriction`, `ParseSpeedRestriction`), adjust.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./nav/... -run "TestConditional(Speed|Mach)" -v`
Expected: FAIL — undefined types.

- [ ] **Step 3: Implement `ConditionalSpeed` and `ConditionalMach`**

Append to `nav/conditional.go`:

```go
// ConditionalSpeed is a deferred speed assignment.
type ConditionalSpeed struct {
	Restriction av.SpeedRestriction
}

func (c ConditionalSpeed) Execute(nav *Nav, simTime Time) {
	sr := c.Restriction
	_ = nav.AssignSpeed(&sr, false)
}

func (c ConditionalSpeed) Render(rt *av.RadioTransmission, r *rand.Rand) {
	spd, _ := c.Restriction.ExactValue()
	rt.Add("[reduce speed to|maintain|slowing to] {spd}", spd)
}

// ConditionalMach is a deferred mach-speed assignment.
type ConditionalMach struct {
	Mach float32
}

func (c ConditionalMach) Execute(nav *Nav, simTime Time) {
	// Mach execution requires a temperature. Use the nav's recorded temperature
	// at the flight level; this is an approximation, since the production
	// AssignMach path queries a live weather model, but it's acceptable here
	// because the deferred action fires when we've just reached the target
	// altitude, which is close to the altitude the controller was considering
	// when issuing the command.
	_ = nav.AssignMach(c.Mach, false, nav.FlightState.Temperature)
}

func (c ConditionalMach) Render(rt *av.RadioTransmission, r *rand.Rand) {
	rt.Add("[mach|maintain mach] {mach}", c.Mach)
}
```

If `nav.FlightState.Temperature` doesn't exist, check `nav/nav.go` around the `FlightState` definition for how temperature is exposed. If it's not a field on FlightState, either:
- Add a `Temperature` parameter to `ConditionalAction.Execute(...)` (requires threading through `sim.updateState`), or
- Look the temperature up via the sim's weather model when firing, before calling `Execute`.

The second option is cleaner if `Execute` grows a temperature parameter: change the interface to `Execute(nav *Nav, simTime Time, temp av.Temperature)` and pass a zero temperature for non-mach actions. Verify `av.Temperature` type: `grep -n "type Temperature" aviation/*.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./nav/... -run "TestConditional(Speed|Mach)" -v`
Expected: PASS.

Run: `go test ./nav/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add nav/conditional.go nav/conditional_test.go
git commit -m "nav: add ConditionalSpeed and ConditionalMach actions"
```

---

### Task 5: Trigger predicate `conditionalTriggered`

**Files:**
- Modify: `nav/conditional.go`
- Modify: `nav/conditional_test.go`

- [ ] **Step 1: Write the failing test**

Append to `nav/conditional_test.go`:

```go
func TestConditionalTriggered(t *testing.T) {
	cases := []struct {
		name     string
		kind     ConditionalKind
		trigger  float32
		altitude float32
		rate     float32 // vertical rate (positive = climb)
		want     bool
	}{
		// --- ConditionalLeaving ---
		{"LV climbing well past", ConditionalLeaving, 3000, 3200, +500, true},
		{"LV descending well past", ConditionalLeaving, 3000, 2800, -500, true},
		{"LV level at trigger", ConditionalLeaving, 3000, 3000, 0, false},
		{"LV within tolerance climbing", ConditionalLeaving, 3000, 3020, +500, false}, // <50ft past
		{"LV 60ft past climbing", ConditionalLeaving, 3000, 3060, +500, true},
		{"LV 60ft below climbing (wrong dir)", ConditionalLeaving, 3000, 2940, +500, false},
		// --- ConditionalReaching ---
		{"RC within 100ft", ConditionalReaching, 10000, 9950, +500, true},
		{"RC 50ft past still climbing", ConditionalReaching, 10000, 10050, +500, true},
		{"RC 200ft short climbing", ConditionalReaching, 10000, 9800, +500, false},
		{"RC leveled at target", ConditionalReaching, 10000, 10000, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := makeTestNav(t, 180)
			n.FlightState.Altitude = tc.altitude
			n.FlightState.AltitudeRate = tc.rate
			pc := &PendingConditionalCommand{Kind: tc.kind, Altitude: tc.trigger}
			if got := conditionalTriggered(&n, pc); got != tc.want {
				t.Errorf("want %v got %v (kind=%v trigger=%v alt=%v rate=%v)",
					tc.want, got, tc.kind, tc.trigger, tc.altitude, tc.rate)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./nav/... -run TestConditionalTriggered -v`
Expected: FAIL — undefined `conditionalTriggered`.

- [ ] **Step 3: Implement `conditionalTriggered`**

Append to `nav/conditional.go`:

```go
import "github.com/mmp/vice/math"  // merge into existing imports

// conditionalTriggered reports whether the pending conditional command
// should fire given the aircraft's current vertical state.
//
//   ConditionalLeaving: fires when altitude is >50 ft past trigger in the
//                       direction of current vertical motion.
//   ConditionalReaching: fires when altitude is within 100 ft of trigger.
func conditionalTriggered(nav *Nav, pc *PendingConditionalCommand) bool {
	alt := nav.FlightState.Altitude
	diff := alt - pc.Altitude
	switch pc.Kind {
	case ConditionalLeaving:
		const leavingTol = 50.0
		if math.Abs(diff) <= leavingTol {
			return false
		}
		rate := nav.FlightState.AltitudeRate
		// Same-sign check: diff>0 (above trigger) requires rate>0 (climbing),
		// diff<0 (below) requires rate<0 (descending). Zero rate with altitude
		// drift outside tolerance (unusual but possible) is not a trigger.
		return (diff > 0 && rate > 0) || (diff < 0 && rate < 0)
	case ConditionalReaching:
		const reachingTol = 100.0
		return math.Abs(diff) <= reachingTol
	}
	return false
}
```

Verify `nav.FlightState.AltitudeRate` exists: `grep -n "AltitudeRate" nav/nav.go`. If the field name differs (e.g., `VerticalRate`, `ClimbRate`), adjust.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./nav/... -run TestConditionalTriggered -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Commit**

```bash
git add nav/conditional.go nav/conditional_test.go
git commit -m "nav: add conditionalTriggered predicate"
```

---

### Task 6: `triggerReachable` reachability check in sim

**Files:**
- Modify: `sim/control.go` (add new helper function near other private helpers)
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Add to `sim/control_test.go` (append to the existing file — find a section with unit tests that don't need a full sim, or add a new one):

```go
func TestTriggerReachable(t *testing.T) {
	cases := []struct {
		name     string
		kind     nav.ConditionalKind
		trigger  float32
		current  float32
		assigned *float32
		want     bool
	}{
		// LV: within 500ft slack even if direction is wrong
		{"LV aircraft at 3050 climbing past", nav.ConditionalLeaving, 3000, 3050, floatp(5000), true},
		{"LV aircraft far past", nav.ConditionalLeaving, 3000, 5000, floatp(7000), false},
		{"LV trigger in path", nav.ConditionalLeaving, 3000, 1000, floatp(5000), true},
		{"LV no target, far from trigger", nav.ConditionalLeaving, 3000, 8000, nil, false},
		// RC: trigger must be between current and assigned target
		{"RC target is trigger", nav.ConditionalReaching, 10000, 5000, floatp(10000), true},
		{"RC trigger above target", nav.ConditionalReaching, 12000, 5000, floatp(10000), false},
		{"RC no target but close", nav.ConditionalReaching, 10000, 9900, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := &Aircraft{} // minimal; set up FlightState and Nav.Altitude
			ac.Nav.FlightState.Altitude = tc.current
			ac.Nav.Altitude.Assigned = tc.assigned
			got := triggerReachable(ac, tc.kind, tc.trigger)
			if got != tc.want {
				t.Errorf("want %v got %v", tc.want, got)
			}
		})
	}
}

func floatp(v float32) *float32 { return &v }
```

If a helper `floatp` (or similar) already exists in the test file, reuse it. Search: `grep -n "func floatp\|func fptr" sim/*_test.go`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestTriggerReachable -v`
Expected: FAIL — undefined `triggerReachable`.

- [ ] **Step 3: Implement `triggerReachable`**

Add to `sim/control.go` near the other conditional-command helpers (e.g., just above or below `parseSpeedUntil`):

```go
// triggerReachable reports whether a LV/RC trigger altitude is
// reasonably reachable from the aircraft's current vertical state,
// allowing the controller command to be accepted.
//
// For ConditionalLeaving: accepted if the aircraft is within 500 ft of
// the trigger (so "leaving 3,000" works even for an aircraft at 3,050),
// or if the trigger lies between current altitude and assigned target.
//
// For ConditionalReaching: accepted if the trigger lies between current
// altitude and assigned target, or (if no target assigned) the aircraft
// is within 500 ft of the trigger.
func triggerReachable(ac *Aircraft, kind nav.ConditionalKind, trigger float32) bool {
	cur := ac.Nav.FlightState.Altitude
	target := ac.Nav.Altitude.Assigned
	diff := math.Abs(cur - trigger)
	switch kind {
	case nav.ConditionalLeaving:
		if diff <= 500 {
			return true
		}
		if target == nil {
			return false
		}
		return betweenAlt(trigger, cur, *target)
	case nav.ConditionalReaching:
		if target == nil {
			return diff <= 500
		}
		return betweenAlt(trigger, cur, *target)
	}
	return false
}

// betweenAlt reports whether v lies between a and b (inclusive), in
// either ordering.
func betweenAlt(v, a, b float32) bool {
	lo, hi := a, b
	if lo > hi {
		lo, hi = hi, lo
	}
	return v >= lo && v <= hi
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestTriggerReachable -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: add triggerReachable helper for LV/RC conditional commands"
```

---

### Task 7: `parseConditionalAltitude`

**Files:**
- Modify: `sim/control.go`
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sim/control_test.go`:

```go
func TestParseConditionalAltitude(t *testing.T) {
	cases := []struct {
		in      string
		want    float32
		wantErr bool
	}{
		{"30", 3000, false},       // hundreds-of-feet
		{"130", 13000, false},
		{"100", 10000, false},
		{"1000", 1000, false},     // >600 && %100==0 → already feet
		{"13000", 13000, false},   // ditto
		{"", 0, true},
		{"abc", 0, true},
	}
	for _, tc := range cases {
		got, err := parseConditionalAltitude(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseConditionalAltitude(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.want {
			t.Errorf("parseConditionalAltitude(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestParseConditionalAltitude -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `parseConditionalAltitude`**

Add to `sim/control.go` near `triggerReachable`:

```go
// parseConditionalAltitude parses the altitude-encoding convention used
// by LV/RC (and RR) commands: number × 100, with a carve-out for values
// that look like feet already (>600 and evenly divisible by 100).
func parseConditionalAltitude(s string) (float32, error) {
	if s == "" {
		return 0, ErrInvalidCommandSyntax
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n > 600 && n%100 == 0 {
		return float32(n), nil
	}
	return float32(n * 100), nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestParseConditionalAltitude -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: add parseConditionalAltitude helper"
```

---

### Task 8: `parseConditionalAction` inner-command parser

**Files:**
- Modify: `sim/control.go`
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sim/control_test.go`:

```go
func TestParseConditionalAction(t *testing.T) {
	cases := []struct {
		in        string
		wantType  string // type name of returned ConditionalAction
		wantProps map[string]any
		wantErr   bool
	}{
		{"H010", "ConditionalHeading", map[string]any{"Heading": 10, "Turn": av.TurnClosest}, false},
		{"L100", "ConditionalHeading", map[string]any{"Heading": 100, "Turn": av.TurnLeft}, false},
		{"R100", "ConditionalHeading", map[string]any{"Heading": 100, "Turn": av.TurnRight}, false},
		{"L20D", "ConditionalHeading", map[string]any{"ByDegrees": 20, "Turn": av.TurnLeft}, false},
		{"R30D", "ConditionalHeading", map[string]any{"ByDegrees": 30, "Turn": av.TurnRight}, false},
		{"DAAC", "ConditionalDirectFix", map[string]any{"Fix": "AAC", "Turn": av.TurnClosest}, false},
		{"LDAAC", "ConditionalDirectFix", map[string]any{"Fix": "AAC", "Turn": av.TurnLeft}, false},
		{"RDAAC", "ConditionalDirectFix", map[string]any{"Fix": "AAC", "Turn": av.TurnRight}, false},
		{"S210", "ConditionalSpeed", nil, false},
		{"M78", "ConditionalMach", map[string]any{"Mach": float32(0.78)}, false},

		// Rejections: altitude-changing inners, unknowns, malformed
		{"C50", "", nil, true},
		{"CVS", "", nil, true},
		{"DVS", "", nil, true},
		{"X010", "", nil, true},
		{"", "", nil, true},
		{"H", "", nil, true},
		{"HXYZ", "", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := parseConditionalAction(tc.in)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseConditionalAction(%q) err=%v wantErr=%v", tc.in, err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			typeName := reflect.TypeOf(got).Name()
			if typeName != tc.wantType {
				t.Fatalf("parseConditionalAction(%q) type = %s, want %s", tc.in, typeName, tc.wantType)
			}
			// Property check via reflection
			v := reflect.ValueOf(got)
			for k, want := range tc.wantProps {
				field := v.FieldByName(k)
				if !field.IsValid() {
					t.Errorf("no field %s on %s", k, typeName)
					continue
				}
				if !reflect.DeepEqual(field.Interface(), want) {
					t.Errorf("%s.%s = %v, want %v", typeName, k, field.Interface(), want)
				}
			}
		})
	}
}
```

Add `reflect` to the test file's imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestParseConditionalAction -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `parseConditionalAction`**

Add to `sim/control.go` near the other conditional helpers:

```go
// parseConditionalAction parses an inner command string (the right-hand
// side of LV/RC) into a typed ConditionalAction. Accepts only lateral and
// speed/mach actions; altitude-changing and unknown inners return
// ErrInvalidCommandSyntax.
//
// Grammar:
//   H{hdg}             → ConditionalHeading (closest turn)
//   L{hdg} | R{hdg}    → ConditionalHeading (left/right turn to heading)
//   L{deg}D | R{deg}D  → ConditionalHeading (turn N degrees)
//   D{fix}             → ConditionalDirectFix (closest)
//   LD{fix} | RD{fix}  → ConditionalDirectFix (left/right)
//   S{spd}             → ConditionalSpeed
//   M{mach}            → ConditionalMach (2-digit mach, e.g. M78 → 0.78)
func parseConditionalAction(s string) (nav.ConditionalAction, error) {
	if len(s) < 2 {
		return nil, ErrInvalidCommandSyntax
	}
	switch s[0] {
	case 'H':
		hdg, err := strconv.Atoi(s[1:])
		if err != nil {
			return nil, ErrInvalidCommandSyntax
		}
		return nav.ConditionalHeading{Heading: hdg, Turn: av.TurnClosest}, nil

	case 'L', 'R':
		turn := av.TurnLeft
		if s[0] == 'R' {
			turn = av.TurnRight
		}
		// LD{fix} / RD{fix}
		if len(s) >= 5 && s[1] == 'D' {
			return nav.ConditionalDirectFix{Fix: strings.ToUpper(s[2:]), Turn: turn}, nil
		}
		// LnnD / RnnD
		if l := len(s); l > 2 && s[l-1] == 'D' {
			deg, err := strconv.Atoi(s[1 : l-1])
			if err != nil {
				return nil, ErrInvalidCommandSyntax
			}
			return nav.ConditionalHeading{ByDegrees: deg, Turn: turn}, nil
		}
		// L{hdg} / R{hdg}
		hdg, err := strconv.Atoi(s[1:])
		if err != nil {
			return nil, ErrInvalidCommandSyntax
		}
		return nav.ConditionalHeading{Heading: hdg, Turn: turn}, nil

	case 'D':
		if len(s) < 4 {
			return nil, ErrInvalidCommandSyntax
		}
		return nav.ConditionalDirectFix{Fix: strings.ToUpper(s[1:]), Turn: av.TurnClosest}, nil

	case 'S':
		sr, err := av.ParseSpeedRestriction(s[1:])
		if err != nil {
			return nil, ErrInvalidCommandSyntax
		}
		return nav.ConditionalSpeed{Restriction: *sr}, nil

	case 'M':
		if len(s) != 3 {
			return nil, ErrInvalidCommandSyntax
		}
		mach, err := strconv.ParseFloat(s[1:], 32)
		if err != nil {
			return nil, ErrInvalidCommandSyntax
		}
		return nav.ConditionalMach{Mach: float32(mach) / 100.0}, nil
	}
	return nil, ErrInvalidCommandSyntax
}
```

Verify `av.ParseSpeedRestriction` exists and returns `*av.SpeedRestriction`: `grep -n "func ParseSpeedRestriction" aviation/*.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestParseConditionalAction -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: add parseConditionalAction for LV/RC inner commands"
```

---

### Task 9: `ConditionalCommandIntent` in aviation

**Files:**
- Modify: `aviation/intent.go`
- Create or modify: `aviation/intent_test.go` (if a test file already exists for intents, use it)

- [ ] **Step 1: Write the failing test**

Find or create a test file for intent rendering. Search: `ls aviation/*_test.go`. If an existing test file covers intents (e.g., `intent_test.go`), append there; otherwise create `aviation/intent_test.go`.

```go
// aviation/intent_test.go (append or create)
func TestConditionalCommandIntentRender(t *testing.T) {
	// Use a simple stub action for testing the intent wrapper.
	stub := stubConditionalAction{text: "fly heading 010"}
	cases := []struct {
		name string
		kind ConditionalKind
		alt  float32
		want []string // substrings expected in the rendered output
	}{
		{"leaving", ConditionalLeaving, 3000, []string{"leaving", "3", "fly heading 010"}},
		{"reaching", ConditionalReaching, 10000, []string{"reaching", "10", "fly heading 010"}},
	}
	r := rand.New(rand.NewPCG(1, 2))
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			intent := ConditionalCommandIntent{Kind: tc.kind, Altitude: tc.alt, Action: stub}
			rt := &RadioTransmission{}
			intent.Render(rt, r)
			written := strings.ToLower(rt.Written(r))
			for _, w := range tc.want {
				if !strings.Contains(written, strings.ToLower(w)) {
					t.Errorf("Render missing %q in %q", w, written)
				}
			}
		})
	}
}

type stubConditionalAction struct{ text string }

func (s stubConditionalAction) Render(rt *RadioTransmission, r *rand.Rand) {
	rt.Add(s.text)
}
// Execute not needed for this test — but if the interface requires it,
// make stub satisfy the full ConditionalAction interface. Note that
// aviation must not import nav (cycle risk); if ConditionalAction is
// defined in nav, the intent must reference the interface via a
// package-neutral declaration — see Step 3.
```

Note the import-cycle concern — `aviation` is a lower-level package than `nav` and cannot import from it. This means `ConditionalCommandIntent.Action` must NOT be typed as `nav.ConditionalAction`. Two options:

(a) Declare a separate minimal interface in `aviation` — e.g., `type ConditionalActionRender interface { Render(*RadioTransmission, *rand.Rand) }`. The `nav.ConditionalAction` interface embeds both `Execute` and `Render`, so any `nav` action automatically satisfies the `aviation` render-only interface. Use this option.

(b) Move the whole action type hierarchy down into `aviation` — more invasive; declines.

The test's `stubConditionalAction` above implements only `Render`, which fits option (a).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./aviation/... -run TestConditionalCommandIntentRender -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `ConditionalCommandIntent`**

Add to `aviation/intent.go`, near the other special intents (e.g., after `ContactTowerIntent`):

```go
// ConditionalKind in aviation mirrors the nav-package enum for use by
// ConditionalCommandIntent. Values must match nav.ConditionalKind.
type ConditionalKind uint8

const (
	ConditionalLeaving ConditionalKind = iota
	ConditionalReaching
)

// ConditionalActionRender is the subset of nav.ConditionalAction that
// the aviation-layer readback needs. Defined here to avoid an import
// cycle (nav imports aviation, not the other way around).
type ConditionalActionRender interface {
	Render(rt *RadioTransmission, r *rand.Rand)
}

// ConditionalCommandIntent is the readback for a "leaving/reaching {alt},
// do X" command. It composes with the inner action's own Render so
// phraseology for H/L/R/D/S/M stays consistent with non-conditional
// variants.
type ConditionalCommandIntent struct {
	Kind     ConditionalKind
	Altitude float32
	Action   ConditionalActionRender
}

func (c ConditionalCommandIntent) Render(rt *RadioTransmission, r *rand.Rand) {
	switch c.Kind {
	case ConditionalLeaving:
		rt.Add("[leaving|passing] {alt}, ", c.Altitude)
	case ConditionalReaching:
		rt.Add("[reaching|level at|on reaching] {alt}, ", c.Altitude)
	}
	if c.Action != nil {
		c.Action.Render(rt, r)
	}
}
```

In `nav/conditional.go`, change `ConditionalKind` and the constants to **alias** the aviation ones to guarantee the values match:

```go
type ConditionalKind = av.ConditionalKind

const (
	ConditionalLeaving  = av.ConditionalLeaving
	ConditionalReaching = av.ConditionalReaching
)
```

Remove the old `ConditionalKind`, `ConditionalLeaving`, `ConditionalReaching` declarations from `nav/conditional.go`. The type alias (`=`) makes `nav.ConditionalKind` the same type as `av.ConditionalKind`, so existing code using either spelling compiles.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./aviation/... -run TestConditionalCommandIntentRender -v`
Expected: PASS.

Run: `go test ./nav/...`
Expected: PASS (all prior tests still pass with aliased enum).

- [ ] **Step 5: Commit**

```bash
git add aviation/intent.go aviation/intent_test.go nav/conditional.go
git commit -m "aviation: add ConditionalCommandIntent; nav: alias enum to aviation"
```

---

### Task 10: `AssignConditional` sim method

**Files:**
- Modify: `sim/control.go`
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sim/control_test.go`. Use the existing sim-test builder (search for how other sim-level commands like `ReportReaching` are tested — look at `sim/control_test.go` for a `setupTestSim` or `newTestSim` helper, and the pattern for `TestReportReaching` or similar). If no such pattern exists, build a minimal one using `NewSim` or the in-file test harness.

```go
func TestAssignConditionalInstallsSlot(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 2000 /*alt*/, 7000 /*assigned*/)
	action := nav.ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	intent, err := s.AssignConditional(tcw, callsign, nav.ConditionalLeaving, 3000, action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if intent == nil {
		t.Fatalf("expected non-nil intent")
	}
	ac := s.lookupAircraft(callsign) // whatever the test helper is
	if ac.Nav.PendingConditionalCommand == nil {
		t.Fatalf("expected PendingConditionalCommand installed")
	}
	if ac.Nav.PendingConditionalCommand.Altitude != 3000 {
		t.Fatalf("wrong altitude: %v", ac.Nav.PendingConditionalCommand.Altitude)
	}
}

func TestAssignConditionalRejectsUnreachable(t *testing.T) {
	// Aircraft at 5000, no assigned altitude change; trigger 3000 → unreachable.
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 5000, 5000)
	action := nav.ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	_, err := s.AssignConditional(tcw, callsign, nav.ConditionalLeaving, 3000, action)
	if err == nil {
		t.Fatalf("expected error for unreachable trigger, got nil")
	}
}

func TestAssignConditionalSupersedes(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 2000, 7000)
	first := nav.ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	second := nav.ConditionalDirectFix{Fix: "AAC", Turn: av.TurnClosest}
	_, _ = s.AssignConditional(tcw, callsign, nav.ConditionalLeaving, 3000, first)
	_, _ = s.AssignConditional(tcw, callsign, nav.ConditionalReaching, 6000, second)
	ac := s.lookupAircraft(callsign)
	pc := ac.Nav.PendingConditionalCommand
	if pc == nil || pc.Kind != nav.ConditionalReaching || pc.Altitude != 6000 {
		t.Fatalf("expected superseded slot: reaching 6000, got %+v", pc)
	}
}
```

If the helpers `setupTestSimWithAircraftAt` and `lookupAircraft` don't exist, adapt to whatever the existing test file uses — look at the nearest `TestAssign*` function in `sim/control_test.go` for the pattern.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestAssignConditional -v`
Expected: FAIL — undefined method.

- [ ] **Step 3: Implement `AssignConditional`**

Add to `sim/control.go`, near `ReportReaching` (which is at roughly line 320 per commit `347d0085`):

```go
// AssignConditional installs a deferred LV/RC action on the aircraft's
// nav state. The action fires silently when sim.updateState observes
// the altitude trigger. Returns an error if the trigger is not
// reachable from the aircraft's current vertical state.
func (s *Sim) AssignConditional(tcw TCW, callsign av.ADSBCallsign,
	kind nav.ConditionalKind, altitude float32, action nav.ConditionalAction) (av.CommandIntent, error) {

	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	return s.dispatchControlledAircraftCommand(tcw, callsign,
		func(tcw TCW, ac *Aircraft) av.CommandIntent {
			if !triggerReachable(ac, kind, altitude) {
				return av.MakeUnableIntent("unable. %s is out of our climb/descent path.",
					av.FormatAltitude(altitude))
			}
			ac.Nav.PendingConditionalCommand = &nav.PendingConditionalCommand{
				Kind:     kind,
				Altitude: altitude,
				Action:   action,
			}
			return av.ConditionalCommandIntent{
				Kind:     kind,
				Altitude: altitude,
				Action:   action,
			}
		})
}
```

`av.FormatAltitude` should already exist (used throughout the codebase). `av.MakeUnableIntent` exists in aviation (used at `nav/commands.go:41`).

Note: `dispatchControlledAircraftCommand` expects the callback to return a `CommandIntent`, not an error. Reachability rejection becomes an unable-intent here (same convention as "that altitude is above our ceiling" in nav/commands.go:41). The outer `(intent, error)` return of the method path is for lookup errors (no such aircraft), not logical "unable" cases.

Review this decision against the Q4 design intent — "Reject with error." An unable-intent is how the rest of the sim signals unable; tests should assert on the intent type and message rather than `err != nil`. Adjust `TestAssignConditionalRejectsUnreachable` accordingly:

```go
func TestAssignConditionalRejectsUnreachable(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 5000, 5000)
	action := nav.ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	intent, err := s.AssignConditional(tcw, callsign, nav.ConditionalLeaving, 3000, action)
	if err != nil {
		t.Fatalf("dispatch error: %v", err)
	}
	if _, ok := intent.(av.UnableIntent); !ok {
		t.Fatalf("expected UnableIntent for unreachable trigger, got %T", intent)
	}
	ac := s.lookupAircraft(callsign)
	if ac.Nav.PendingConditionalCommand != nil {
		t.Fatalf("expected no slot installed for unable")
	}
}
```

Check the actual unable-intent type name: `grep -n "type UnableIntent\|type.*Unable" aviation/*.go`.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestAssignConditional -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: add AssignConditional method for LV/RC commands"
```

---

### Task 11: Dispatch branch for `LV` in case 'L'

**Files:**
- Modify: `sim/control.go`
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sim/control_test.go`:

```go
func TestRunControlCommandLV(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 2000, 7000)
	intent, err := s.runOneControlCommand(tcw, callsign, "LV30/H010", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := intent.(av.ConditionalCommandIntent); !ok {
		t.Fatalf("expected ConditionalCommandIntent, got %T", intent)
	}
	ac := s.lookupAircraft(callsign)
	if ac.Nav.PendingConditionalCommand == nil {
		t.Fatalf("slot not installed")
	}
	if ac.Nav.PendingConditionalCommand.Altitude != 3000 {
		t.Fatalf("wrong altitude %v", ac.Nav.PendingConditionalCommand.Altitude)
	}
}

func TestRunControlCommandLVRejectsMalformed(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 2000, 7000)
	cases := []string{
		"LV30H010",      // missing slash
		"LV/H010",       // empty altitude
		"LV30/",         // empty inner
		"LVABC/H010",    // non-numeric altitude
		"LV30/C50",      // altitude-changing inner
		"LV30/X010",     // unknown inner
	}
	for _, cmd := range cases {
		t.Run(cmd, func(t *testing.T) {
			_, err := s.runOneControlCommand(tcw, callsign, cmd, 0)
			if err == nil {
				t.Fatalf("expected error for %q, got nil", cmd)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestRunControlCommandLV -v`
Expected: FAIL (LV command not yet handled — falls through to existing heading parse, which will fail weirdly).

- [ ] **Step 3: Add the LV branch in case 'L'**

In `sim/control.go`, in `runOneControlCommand`, locate `case 'L':` (around line 4096 per the pre-branch state; confirm with `grep -n "case 'L':" sim/control.go`). Insert the following branch BEFORE any existing branches in `case 'L'`:

```go
case 'L':
	if strings.HasPrefix(command, "LV") && len(command) > 2 {
		altStr, inner, ok := strings.Cut(command[2:], "/")
		if !ok || altStr == "" || inner == "" {
			return nil, ErrInvalidCommandSyntax
		}
		alt, err := parseConditionalAltitude(altStr)
		if err != nil {
			return nil, err
		}
		action, err := parseConditionalAction(inner)
		if err != nil {
			return nil, err
		}
		return s.AssignConditional(tcw, callsign, nav.ConditionalLeaving, alt, action)
	}
	// ...existing case 'L' body unchanged...
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestRunControlCommandLV -v`
Expected: PASS.

Run the full sim suite to ensure no regression: `go test ./sim/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: dispatch LV{alt}/{inner} as conditional-leaving command"
```

---

### Task 12: Dispatch branch for `RC` in case 'R'

**Files:**
- Modify: `sim/control.go`
- Modify: `sim/control_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sim/control_test.go`:

```go
func TestRunControlCommandRC(t *testing.T) {
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 5000, 10000)
	intent, err := s.runOneControlCommand(tcw, callsign, "RC100/DAAC", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := intent.(av.ConditionalCommandIntent); !ok {
		t.Fatalf("expected ConditionalCommandIntent, got %T", intent)
	}
	ac := s.lookupAircraft(callsign)
	if ac.Nav.PendingConditionalCommand == nil {
		t.Fatalf("slot not installed")
	}
	if ac.Nav.PendingConditionalCommand.Altitude != 10000 {
		t.Fatalf("wrong altitude %v", ac.Nav.PendingConditionalCommand.Altitude)
	}
}

func TestRunControlCommandRCDoesNotConflictWithRR(t *testing.T) {
	// Ensure RC100 is not parsed as RR (report reaching).
	s, callsign, tcw := setupTestSimWithAircraftAt(t, 5000, 10000)
	intent, err := s.runOneControlCommand(tcw, callsign, "RC100/H010", 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, ok := intent.(av.ConditionalCommandIntent); !ok {
		t.Fatalf("expected ConditionalCommandIntent, got %T", intent)
	}
	ac := s.lookupAircraft(callsign)
	// RR would have set ReportReachingAltitude, not PendingConditionalCommand.
	if ac.Nav.ReportReachingAltitude != nil {
		t.Fatalf("RR altitude should not be set, got %v", *ac.Nav.ReportReachingAltitude)
	}
	if ac.Nav.PendingConditionalCommand == nil {
		t.Fatalf("conditional slot not installed")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run TestRunControlCommandRC -v`
Expected: FAIL.

- [ ] **Step 3: Add the RC branch in case 'R'**

In `sim/control.go`, in `runOneControlCommand`, locate `case 'R':` (around line 4139 in the pre-branch state; confirm with `grep -n "case 'R':" sim/control.go`). Insert a new branch BEFORE the existing `RR` branch:

```go
case 'R':
	if command == "RON" {
		return s.ResumeOwnNavigation(tcw, callsign)
	} else if command == "RST" {
		return s.RadarServicesTerminated(tcw, callsign)
	} else if strings.HasPrefix(command, "RC") && len(command) > 2 && strings.Contains(command, "/") {
		altStr, inner, ok := strings.Cut(command[2:], "/")
		if !ok || altStr == "" || inner == "" {
			return nil, ErrInvalidCommandSyntax
		}
		alt, err := parseConditionalAltitude(altStr)
		if err != nil {
			return nil, err
		}
		action, err := parseConditionalAction(inner)
		if err != nil {
			return nil, err
		}
		return s.AssignConditional(tcw, callsign, nav.ConditionalReaching, alt, action)
	} else if strings.HasPrefix(command, "RR") && len(command) > 2 && util.IsAllNumbers(command[2:]) {
		// ...existing RR branch body unchanged...
	}
	// ...remainder of case 'R' unchanged...
```

The `strings.Contains(command, "/")` guard on RC disambiguates from any future `RC<number>` pattern; the slash is mandatory for the conditional syntax.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run TestRunControlCommandRC -v`
Expected: PASS.

Run: `go test ./sim/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: dispatch RC{alt}/{inner} as conditional-reaching command"
```

---

### Task 13: Trigger firing in `sim.updateState`

**Files:**
- Modify: `sim/sim.go`
- Modify: `sim/e2e_test.go` (or create `sim/conditional_e2e_test.go`)

- [ ] **Step 1: Write the failing test**

Create `sim/conditional_e2e_test.go`. Model after the existing e2e tests in `sim/e2e_test.go` — read that file for the pattern (sim setup, tick loop, assertions).

```go
package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/nav"
)

func TestLVHeadingE2E(t *testing.T) {
	s, callsign, tcw := setupE2ESimClimbing(t, 2000 /*from*/, 7000 /*to*/)
	// Leaving 3,000 → turn left 010
	_, err := s.runOneControlCommand(tcw, callsign, "LV30/L010", 0)
	if err != nil {
		t.Fatalf("command error: %v", err)
	}
	// Tick until aircraft is >50 ft past 3,000 climbing.
	s.tickUntil(t, func(ac *Aircraft) bool {
		return ac.Nav.FlightState.Altitude > 3100
	}, 300 /*tick budget*/)

	ac := s.lookupAircraft(callsign)
	if ac.Nav.PendingConditionalCommand != nil {
		t.Fatalf("slot not cleared after trigger fire")
	}
	if hdg, ok := ac.Nav.AssignedHeading(); !ok || hdg != 10 {
		t.Fatalf("expected assigned heading 10, got ok=%v hdg=%v", ok, hdg)
	}
}

func TestRCDirectFixE2E(t *testing.T) {
	s, callsign, tcw := setupE2ESimClimbingWithFix(t, 7000, 10000, "AAC")
	_, err := s.runOneControlCommand(tcw, callsign, "RC100/DAAC", 0)
	if err != nil {
		t.Fatalf("command error: %v", err)
	}
	s.tickUntil(t, func(ac *Aircraft) bool {
		return ac.Nav.FlightState.Altitude >= 9900
	}, 600)
	ac := s.lookupAircraft(callsign)
	if ac.Nav.PendingConditionalCommand != nil {
		t.Fatalf("slot not cleared")
	}
	if len(ac.Nav.Waypoints) == 0 || ac.Nav.Waypoints[0].Fix != "AAC" {
		t.Fatalf("expected direct AAC, got waypoints %+v", ac.Nav.Waypoints)
	}
}
```

If helpers like `setupE2ESimClimbing`, `tickUntil`, `lookupAircraft` don't exist, create them (likely small wrappers around existing test utilities). Look at `sim/altimeter_integration_test.go` which was added for a similar end-to-end verification — it's the closest precedent.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim/... -run "TestLVHeadingE2E|TestRCDirectFixE2E" -v`
Expected: FAIL — the command installs the slot, but no trigger-firing code exists yet, so the slot stays and heading/waypoint isn't updated.

- [ ] **Step 3: Add trigger-firing code to `sim.updateState`**

In `sim/sim.go`, in `Sim.updateState`, near the existing `ReportReachingAltitude` check (added in commit `347d0085`), insert:

```go
// "Leaving/reaching {alt}, do X" — when the aircraft crosses the
// trigger altitude, silently execute the deferred action. The slot
// is cleared BEFORE Execute runs so a mis-parsed inner command that
// installs another conditional doesn't loop.
if pc := ac.Nav.PendingConditionalCommand; pc != nil && ac.IsAssociated() {
	if nav.ConditionalTriggered(&ac.Nav, pc) {
		action := pc.Action
		ac.Nav.PendingConditionalCommand = nil
		action.Execute(&ac.Nav, s.State.SimTime)
	}
}
```

Note: `conditionalTriggered` from Task 5 was private (lowercase). Export it as `ConditionalTriggered` (capitalize the first letter in `nav/conditional.go`) so `sim` can call it. Update the Task 5 test to use the uppercase name.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./sim/... -run "TestLVHeadingE2E|TestRCDirectFixE2E" -v`
Expected: PASS.

Run all tests: `go test ./sim/... ./nav/... ./aviation/... ./stt/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/sim.go sim/conditional_e2e_test.go nav/conditional.go nav/conditional_test.go
git commit -m "sim: fire LV/RC conditional commands when altitude trigger is met"
```

---

### Task 14: STT grammar for `LV` trigger

**Files:**
- Modify: `stt/handlers.go`
- Modify: `stt/handlers_test.go`

- [ ] **Step 1: Read the STT framework first**

Run: `grep -n "func registerSTTCommand\|type CommandOption" stt/registry.go` — get the exact signature.

Examine an existing multi-slot command for the argument-binding pattern (e.g., how the `report reaching {altitude}` handler at commit `347d0085` binds `alt` to the handler parameter). Also examine a command that includes a turn direction (e.g., "turn left heading {hdg}") for how multi-token matches produce the command string.

- [ ] **Step 2: Write the failing test**

Append to `stt/handlers_test.go`. Look at existing tests (e.g., for "report reaching {altitude}") for the assertion pattern — probably `parseAndMatch(input)` returns the command string.

```go
func TestSTTLeavingPatterns(t *testing.T) {
	cases := []struct {
		spoken string
		want   string // expected command string
	}{
		{"leaving three thousand fly heading zero one zero",       "LV30/H010"},
		{"passing one three thousand right heading one zero zero", "LV130/R100"},
		{"leaving five thousand turn left heading two seven zero", "LV50/L270"},
		{"leaving three thousand turn left twenty degrees",        "LV30/L20D"},
		{"leaving three thousand direct alpha alpha charlie",      "LV30/DAAC"},
		{"leaving five thousand reduce speed to two one zero",     "LV50/S210"},
	}
	for _, tc := range cases {
		t.Run(tc.spoken, func(t *testing.T) {
			got := matchSTT(tc.spoken) // existing test helper (or equivalent)
			if got != tc.want {
				t.Errorf("matchSTT(%q) = %q, want %q", tc.spoken, got, tc.want)
			}
		})
	}
}
```

If there's no existing `matchSTT` helper, search `stt/*_test.go` for how other tests exercise the grammar — the API is probably a method on a registry object.

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./stt/... -run TestSTTLeavingPatterns -v`
Expected: FAIL — no matching grammar registered.

- [ ] **Step 4: Register the LV grammar**

In `stt/handlers.go`, inside the existing `registerAllCommands` function, add a section for conditional-leaving patterns. Because the inner-command grammar is already defined elsewhere, we register one `registerSTTCommand` per (trigger × inner) combination. Keep each inner's phraseology consistent with the non-conditional version of the same command:

```go
// "Leaving/passing {alt}, {inner}" — conditional LV commands.

// LV/H{hdg}: "leaving three thousand, fly heading 010"
registerSTTCommand(
	"leaving|passing {altitude}, fly heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("LV%d/H%03d", alt, hdg) },
	WithName("conditional_lv_heading"),
	WithPriority(11),
)

// LV/L{hdg} and LV/R{hdg}
registerSTTCommand(
	"leaving|passing {altitude}, turn left heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("LV%d/L%03d", alt, hdg) },
	WithName("conditional_lv_turn_left_heading"),
	WithPriority(11),
)
registerSTTCommand(
	"leaving|passing {altitude}, turn right heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("LV%d/R%03d", alt, hdg) },
	WithName("conditional_lv_turn_right_heading"),
	WithPriority(11),
)

// LV/L{deg}D and LV/R{deg}D
registerSTTCommand(
	"leaving|passing {altitude}, turn left {num:1-180} degrees",
	func(alt int, deg int) string { return fmt.Sprintf("LV%d/L%dD", alt, deg) },
	WithName("conditional_lv_turn_left_degrees"),
	WithPriority(11),
)
registerSTTCommand(
	"leaving|passing {altitude}, turn right {num:1-180} degrees",
	func(alt int, deg int) string { return fmt.Sprintf("LV%d/R%dD", alt, deg) },
	WithName("conditional_lv_turn_right_degrees"),
	WithPriority(11),
)

// LV/D{fix}, LV/LD{fix}, LV/RD{fix}
registerSTTCommand(
	"leaving|passing {altitude}, [proceed] direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("LV%d/D%s", alt, fix) },
	WithName("conditional_lv_direct"),
	WithPriority(11),
)
registerSTTCommand(
	"leaving|passing {altitude}, turn left direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("LV%d/LD%s", alt, fix) },
	WithName("conditional_lv_left_direct"),
	WithPriority(11),
)
registerSTTCommand(
	"leaving|passing {altitude}, turn right direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("LV%d/RD%s", alt, fix) },
	WithName("conditional_lv_right_direct"),
	WithPriority(11),
)

// LV/S{spd}
registerSTTCommand(
	"leaving|passing {altitude}, [reduce speed to|maintain|slow to] {speed}",
	func(alt int, spd int) string { return fmt.Sprintf("LV%d/S%d", alt, spd) },
	WithName("conditional_lv_speed"),
	WithPriority(11),
)

// LV/M{mach}
registerSTTCommand(
	"leaving|passing {altitude}, [maintain] mach {num:50-99}",
	func(alt int, mach int) string { return fmt.Sprintf("LV%d/M%d", alt, mach) },
	WithName("conditional_lv_mach"),
	WithPriority(11),
)
```

Verify the template syntax matches the existing STT framework — read `stt/handlers.go` around the commit `347d0085` additions for precedent. The brackets `[...]` for optional tokens, pipes `|` for alternation, and `{name:range}` for constrained numbers are all inferred from the `stop_altitude_squawk_with_delta` command added in that commit. If the framework's token syntax differs, adjust.

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./stt/... -run TestSTTLeavingPatterns -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stt/handlers.go stt/handlers_test.go
git commit -m "stt: add LV conditional-leaving voice patterns"
```

---

### Task 15: STT grammar for `RC` trigger

**Files:**
- Modify: `stt/handlers.go`
- Modify: `stt/handlers_test.go`

- [ ] **Step 1: Write the failing test**

Append to `stt/handlers_test.go`:

```go
func TestSTTReachingPatterns(t *testing.T) {
	cases := []struct {
		spoken string
		want   string
	}{
		{"reaching one zero thousand fly heading zero one zero",       "RC100/H010"},
		{"level at one zero thousand direct alpha alpha charlie",      "RC100/DAAC"},
		{"on reaching five thousand reduce speed to two one zero",     "RC50/S210"},
		{"reaching three five zero mach seven eight",                  "RC350/M78"},
	}
	for _, tc := range cases {
		t.Run(tc.spoken, func(t *testing.T) {
			got := matchSTT(tc.spoken)
			if got != tc.want {
				t.Errorf("matchSTT(%q) = %q, want %q", tc.spoken, got, tc.want)
			}
		})
	}
}

func TestSTTReachingDoesNotMatchReportReaching(t *testing.T) {
	// "report reaching {alt}" must still route to the RR command, not RC.
	got := matchSTT("report reaching one zero thousand")
	if got != "RR100" {
		t.Errorf("expected RR100 for report reaching, got %q", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./stt/... -run TestSTTReaching -v`
Expected: FAIL.

- [ ] **Step 3: Register the RC grammar**

In `stt/handlers.go`, add a parallel section to Task 14 but with the reaching triggers. Key differences:

- Triggers: `"reaching|level at|on reaching {altitude}"`.
- **Avoid `"report reaching"` overlap**: the existing `report_reaching` handler at commit `347d0085` has priority 10. Set RC handlers to priority 11 (higher priority, but `report reaching` explicitly starts with `report`, so the prefix difference should be disambiguating). Add a test (already in Step 1) confirming the existing `report reaching` grammar still wins for its phrasing.

```go
// "Reaching/level at/on reaching {alt}, {inner}" — conditional RC commands.

registerSTTCommand(
	"reaching|level at|on reaching {altitude}, fly heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("RC%d/H%03d", alt, hdg) },
	WithName("conditional_rc_heading"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn left heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("RC%d/L%03d", alt, hdg) },
	WithName("conditional_rc_turn_left_heading"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn right heading {heading}",
	func(alt int, hdg int) string { return fmt.Sprintf("RC%d/R%03d", alt, hdg) },
	WithName("conditional_rc_turn_right_heading"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn left {num:1-180} degrees",
	func(alt int, deg int) string { return fmt.Sprintf("RC%d/L%dD", alt, deg) },
	WithName("conditional_rc_turn_left_degrees"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn right {num:1-180} degrees",
	func(alt int, deg int) string { return fmt.Sprintf("RC%d/R%dD", alt, deg) },
	WithName("conditional_rc_turn_right_degrees"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, [proceed] direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("RC%d/D%s", alt, fix) },
	WithName("conditional_rc_direct"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn left direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("RC%d/LD%s", alt, fix) },
	WithName("conditional_rc_left_direct"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, turn right direct {fix}",
	func(alt int, fix string) string { return fmt.Sprintf("RC%d/RD%s", alt, fix) },
	WithName("conditional_rc_right_direct"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, [reduce speed to|maintain|slow to] {speed}",
	func(alt int, spd int) string { return fmt.Sprintf("RC%d/S%d", alt, spd) },
	WithName("conditional_rc_speed"),
	WithPriority(11),
)
registerSTTCommand(
	"reaching|level at|on reaching {altitude}, [maintain] mach {num:50-99}",
	func(alt int, mach int) string { return fmt.Sprintf("RC%d/M%d", alt, mach) },
	WithName("conditional_rc_mach"),
	WithPriority(11),
)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./stt/... -run "TestSTTReaching|TestSTTLeaving" -v`
Expected: PASS.

Full test run: `go test ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stt/handlers.go stt/handlers_test.go
git commit -m "stt: add RC conditional-reaching voice patterns"
```

---

### Task 16: whatsnew.md entry

**Files:**
- Modify: `whatsnew.md`

- [ ] **Step 1: Read the existing whatsnew format**

Run: `head -30 whatsnew.md` — see the format for recent entries (bullet list, terse, user-facing).

- [ ] **Step 2: Add the entry**

Add a single bullet near the top of `whatsnew.md` (or in the most-recent-changes section, whatever the existing convention is):

```markdown
- Added "leaving/reaching {altitude}, {action}" controller commands. Examples: `LV30/H010` ("leaving 3,000, fly heading 010"), `RC100/DAAC` ("reaching 10,000, direct AAC"). Supported inner actions: headings, turns by degrees, direct-to-fix, speed, and mach.
```

- [ ] **Step 3: Commit**

```bash
git add whatsnew.md
git commit -m "docs: whatsnew entry for LV/RC conditional commands"
```

---

### Task 17: Final full-suite verification

- [ ] **Step 1: Run everything**

```bash
go test ./...
```

Expected: PASS.

- [ ] **Step 2: Spot-check the feature end-to-end in a running sim (optional but recommended)**

If time permits, launch vice, spawn a scenario with a departure climbing out, and issue `LV30/H010` via keyboard. Verify the readback renders "leaving 3,000, fly heading 010" and that the heading turn happens silently once the aircraft climbs through 3,000. Repeat for `RC100/DAAC` with an arrival descending to 10,000.

- [ ] **Step 3: No commit** — this is verification only.

---

## Spec coverage self-check

Walking the spec section by section:

| Spec section | Covered by |
|---|---|
| Data model (`PendingConditionalCommand`, `ConditionalAction`) | Task 1 |
| `ConditionalHeading` | Task 2 |
| `ConditionalDirectFix` | Task 3 |
| `ConditionalSpeed` + `ConditionalMach` | Task 4 |
| Trigger predicate | Task 5 |
| Reachability rule | Task 6 |
| Altitude encoding | Task 7 |
| Inner-command parser | Task 8 |
| Readback intent | Task 9 |
| `AssignConditional` sim method | Task 10 |
| `LV` dispatch | Task 11 |
| `RC` dispatch | Task 12 |
| Silent firing at trigger | Task 13 |
| STT voice (LV) | Task 14 |
| STT voice (RC) | Task 15 |
| User-visible changelog | Task 16 |
| Final regression check | Task 17 |

No spec items unaccounted for.

## Open verification notes

These are items I've flagged in the plan that require a small amount of framework archaeology at implementation time — they don't change the design but affect the exact code:

1. **STT template syntax** (Task 14 step 4) — the `{num:N-M}` range syntax and `[optional]` token delimiter are inferred from the existing `stop_altitude_squawk_with_delta` handler. If the framework uses different syntax, adjust the templates at that step and add a smaller-scope test to verify.

2. **Temperature access in `ConditionalMach.Execute`** (Task 4 step 3) — if `FlightState.Temperature` doesn't exist, extend `ConditionalAction.Execute` with a `temp av.Temperature` parameter and have sim.updateState look it up via the weather model before calling.

3. **`directFix` internal helper** (Task 3 step 3) — if no lowercase internal helper exists on `Nav`, use the public `DirectFix` and discard the returned intent.

4. **`UnableIntent` naming** (Task 10 step 3) — confirm the exact type name of the unable-intent used by `av.MakeUnableIntent`.
