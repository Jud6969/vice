# IFR Practice Approaches Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add scenario-author-driven IFR practice-approach traffic. Aircraft spawn with a counter of remaining missed approaches, fly the published miss after each approach, get vectored back around by the controller, and land normally on their final approach. The pilot announces a preferred approach on initial contact and after every miss.

**Architecture:** Four new fields on `sim.Aircraft`, one new optional struct on `aviation.InboundFlow`, one new pending-transmission tag, one decision point at the top of `sim.goAround()`, one branch in the `Land()` waypoint handler in `sim/sim.go`. No new RPCs, no new event types, no scratchpad/FDB indicators. Reuses the existing `PendingContact` machinery for pilot transmissions and the existing `HandoffTrack` RPC for tower→approach hand-back on miss.

**Tech Stack:** Go (server-side `sim/`, `aviation/`, `nav/`), gob serialization (no schema migration needed).

**Spec:** `docs/superpowers/specs/2026-05-03-ifr-practice-approaches-design.md`.

---

## File Map

**Modified files:**
- `sim/aircraft.go` — add four practice-approach fields to `Aircraft`. Add `pickPracticeApproach` helper.
- `sim/radio.go` — add `PendingTransmissionPracticeApproachReq` constant. Add `PracticeApproachID` and `PracticeApproachFullStop` to `PendingContact`. Branch in the `case PendingTransmissionArrival:` block of `processPendingContact` (or its equivalent) to chain the practice request after the check-in. New case `PendingTransmissionPracticeApproachReq:` that builds the request transmission.
- `sim/spawn_arrivals.go` — call `pickPracticeApproach` and seed practice fields on spawn.
- `sim/goaround.go` — top-of-function branch into `practiceMissedApproach()`.
- `sim/sim.go` — in the `passedWaypoint.Land()` block (line 1076), if `ac.MissedApproachesRemaining > 0`, call `goAround()` instead of the existing landing/go-around branch.
- `sim/approach.go` — in `ClearedApproach`, stash the issuing controller's TCP onto `ac.PracticeApproachController` for practice aircraft.
- `aviation/aviation.go` — add `PracticeApproachConfig` struct and `PracticeApproaches *PracticeApproachConfig` field on `InboundFlow`. Add `Validate(e *util.ErrorLogger)` on `PracticeApproachConfig`.
- `server/scenario.go` — call `PracticeApproaches.Validate(...)` from the existing scenario-load validation path.
- `nav/nav.go` — add a helper that signals "level on the missed-approach segment" (see Task 9).

**New files:**
- `sim/practice.go` — `practiceMissedApproach()`, `pickPracticeApproach()`, `handBackToApproachController()`, `levelOnMissSegment()`.
- `sim/practice_test.go` — unit tests for the helpers above and the spawn-side wiring.
- `sim/practice_integration_test.go` — end-to-end test for the full N-miss-then-land flow.

---

## Task 1: Add practice-approach fields to `Aircraft` struct

**Files:**
- Modify: `sim/aircraft.go:46-148`
- Test: `sim/practice_test.go` (new)

- [ ] **Step 1: Write failing round-trip test**

Create `sim/practice_test.go`:

```go
// sim/practice_test.go
package sim

import (
	"bytes"
	"encoding/gob"
	"testing"

	av "github.com/mmp/vice/aviation"
)

func TestAircraft_PracticeFieldsRoundTrip(t *testing.T) {
	ac := &Aircraft{
		ADSBCallsign:               "AAL123",
		MissedApproachesRemaining:  3,
		PracticeApproachID:         "I22L",
		PracticeApproachController: "1A",
		PendingPracticeRequest:     true,
	}

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(ac); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got Aircraft
	if err := gob.NewDecoder(&buf).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.MissedApproachesRemaining != 3 {
		t.Errorf("MissedApproachesRemaining: want 3, got %d", got.MissedApproachesRemaining)
	}
	if got.PracticeApproachID != "I22L" {
		t.Errorf("PracticeApproachID: want %q, got %q", "I22L", got.PracticeApproachID)
	}
	if got.PracticeApproachController != "1A" {
		t.Errorf("PracticeApproachController: want %q, got %q", "1A", got.PracticeApproachController)
	}
	if !got.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest: want true, got false")
	}
	_ = av.ADSBCallsign("") // keep import even if unused
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags vulkan ./sim -run TestAircraft_PracticeFieldsRoundTrip -v`
Expected: FAIL — `unknown field MissedApproachesRemaining in struct literal`.

- [ ] **Step 3: Add fields to the `Aircraft` struct**

In `sim/aircraft.go`, just before the closing brace of `type Aircraft struct` (currently line 148, after `TouchAndGosRemaining int`), add:

```go
	// IFR practice approaches.
	// MissedApproachesRemaining > 0 means this aircraft will go missed instead of
	// landing on its next N approaches; it lands normally on approach N+1.
	// Decremented inside practiceMissedApproach() (sim/practice.go).
	MissedApproachesRemaining int
	// PracticeApproachID is the av.Approach.Id the AI requests on initial contact
	// and after every miss. Empty for non-practice aircraft.
	PracticeApproachID string
	// PracticeApproachController is the TCP of the approach controller to hand
	// the aircraft back to on miss. Refreshed on every C<approach>.
	PracticeApproachController TCP
	// PendingPracticeRequest is set true when the AI owes a practice-approach
	// request transmission (initial contact or post-miss level-off). Cleared
	// when the transmission is queued.
	PendingPracticeRequest bool
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags vulkan ./sim -run TestAircraft_PracticeFieldsRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Run full sim build and tests for the package**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK, no new test failures (the existing `client` STT-DLL load-failure on Windows is pre-existing and unrelated).

- [ ] **Step 6: Commit**

```bash
git add sim/aircraft.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: add IFR practice-approach fields to Aircraft

MissedApproachesRemaining counts how many more times the aircraft will
go missed before landing. PracticeApproachID is the approach the AI
requests on initial contact and after every miss; PracticeApproachController
remembers the approach controller to hand back to on miss. PendingPracticeRequest
gates the post-miss transmission firing.

All four fields are zero-valued on non-practice aircraft, gob-compatible
forward and back.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: Add `PracticeApproachConfig` and `InboundFlow.PracticeApproaches`

**Files:**
- Modify: `aviation/aviation.go:95-98` (InboundFlow struct)
- Test: `aviation/aviation_test.go` (extend if exists; create if not)

- [ ] **Step 1: Write failing test for `Validate`**

In `aviation/aviation_test.go` (create if absent — `package aviation`), add:

```go
package aviation

import (
	"strings"
	"testing"

	"github.com/mmp/vice/util"
)

func TestPracticeApproachConfig_Validate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     PracticeApproachConfig
		wantErr string
	}{
		{"valid", PracticeApproachConfig{Probability: 0.5, MinMissedApproaches: 1, MaxMissedApproaches: 3}, ""},
		{"prob negative", PracticeApproachConfig{Probability: -0.1, MinMissedApproaches: 1, MaxMissedApproaches: 1}, "probability"},
		{"prob over 1", PracticeApproachConfig{Probability: 1.5, MinMissedApproaches: 1, MaxMissedApproaches: 1}, "probability"},
		{"min > max", PracticeApproachConfig{Probability: 0.5, MinMissedApproaches: 5, MaxMissedApproaches: 3}, "min"},
		{"min negative", PracticeApproachConfig{Probability: 0.5, MinMissedApproaches: -1, MaxMissedApproaches: 3}, "min"},
		{"max negative", PracticeApproachConfig{Probability: 0.5, MinMissedApproaches: 0, MaxMissedApproaches: -1}, "min"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := &util.ErrorLogger{}
			c.cfg.Validate(e)
			err := e.String()
			if c.wantErr == "" && err != "" {
				t.Errorf("unexpected error: %s", err)
			}
			if c.wantErr != "" && !strings.Contains(strings.ToLower(err), c.wantErr) {
				t.Errorf("want error containing %q, got %q", c.wantErr, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags vulkan ./aviation -run TestPracticeApproachConfig_Validate -v`
Expected: FAIL — `undefined: PracticeApproachConfig`.

- [ ] **Step 3: Add the struct, the Validate method, and the InboundFlow field**

In `aviation/aviation.go`, just before the existing `type InboundFlow struct {` (line 95), add:

```go
// PracticeApproachConfig opts an inbound flow into producing IFR
// practice-approach traffic. Each spawned aircraft from the flow has
// the configured Probability of being a practice aircraft; if so, it
// is born with a random MissedApproachesRemaining in [Min, Max] and a
// preferred approach picked from the scenario's active arrival runways.
type PracticeApproachConfig struct {
	Probability         float32 `json:"probability"`           // [0, 1]
	MinMissedApproaches int     `json:"min_missed_approaches"` // inclusive lower bound
	MaxMissedApproaches int     `json:"max_missed_approaches"` // inclusive upper bound
}

// Validate appends a load error to e for each invalid field.
func (c PracticeApproachConfig) Validate(e *util.ErrorLogger) {
	if c.Probability < 0 || c.Probability > 1 {
		e.ErrorString("practice_approaches: probability %f out of range [0, 1]", c.Probability)
	}
	if c.MinMissedApproaches < 0 || c.MaxMissedApproaches < 0 {
		e.ErrorString("practice_approaches: min/max missed approaches must be >= 0 (got %d, %d)",
			c.MinMissedApproaches, c.MaxMissedApproaches)
	}
	if c.MinMissedApproaches > c.MaxMissedApproaches {
		e.ErrorString("practice_approaches: min_missed_approaches (%d) > max_missed_approaches (%d)",
			c.MinMissedApproaches, c.MaxMissedApproaches)
	}
}
```

Then change `InboundFlow` (line 95-98) to:

```go
type InboundFlow struct {
	Arrivals    []Arrival    `json:"arrivals"`
	Overflights []Overflight `json:"overflights"`

	// PracticeApproaches, if non-nil, opts this flow into producing
	// IFR practice-approach traffic. See PracticeApproachConfig.
	PracticeApproaches *PracticeApproachConfig `json:"practice_approaches,omitempty"`
}
```

If `util` is not already imported at the top of `aviation/aviation.go`, add it. (Grep `^import` block; the util package is `"github.com/mmp/vice/util"`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags vulkan ./aviation -run TestPracticeApproachConfig_Validate -v`
Expected: PASS for all sub-cases.

- [ ] **Step 5: Build the tree**

Run: `go build -tags vulkan ./aviation/... ./sim/... ./server/...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add aviation/aviation.go aviation/aviation_test.go
git commit -m "$(cat <<'EOF'
aviation: add PracticeApproachConfig on InboundFlow

Optional per-flow knob: probability that a spawned aircraft is practice
traffic, plus inclusive min/max bounds for the random missed-approach count.
Validate() rejects out-of-range probability, negative bounds, and inverted
min/max. Default (nil) disables practice traffic for the flow.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Wire `PracticeApproachConfig.Validate` into scenario load

**Files:**
- Modify: `server/scenario.go` (around the InboundFlows iteration in PostDeserialize / load validation)

- [ ] **Step 1: Locate the InboundFlow iteration in scenario validation**

Run:
```bash
grep -n "InboundFlows" server/scenario.go
```

Find a loop that iterates `sg.InboundFlows` during deserialization/validation. There's an existing iteration around line 172 (`if flow, ok := sg.InboundFlows[flowName]; ...`) and around line 515. The validation hook lives in `(sg *ScenarioGroup) PostDeserialize` or a similar load-time method — grep for `func.*ScenarioGroup.*PostDeserialize` to confirm.

- [ ] **Step 2: Add the validation call**

Inside the existing inbound-flow validation block (the one called at scenario load that already iterates `sg.InboundFlows`), add:

```go
for flowName, flow := range sg.InboundFlows {
	// ... existing checks ...

	if flow.PracticeApproaches != nil {
		flow.PracticeApproaches.Validate(e)
		// (e is the *util.ErrorLogger threaded through this function. If the
		// surrounding code uses a different name like "errs", use that.)
	}
}
```

If the surrounding loop is over a slice/array rather than a map, adapt the iteration; the call inside the loop body is the same.

- [ ] **Step 3: Build the tree**

Run: `go build -tags vulkan ./server/...`
Expected: exit 0.

- [ ] **Step 4: Run server tests**

Run: `go test -tags vulkan ./server/... -count=1 -short`
Expected: existing tests still pass.

- [ ] **Step 5: Commit**

```bash
git add server/scenario.go
git commit -m "$(cat <<'EOF'
server: validate PracticeApproachConfig at scenario load

Calls PracticeApproachConfig.Validate from the existing inbound-flow
validation pass so bad probability/bounds fail the scenario load with
a clear message instead of producing weird runtime behavior.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: `pickPracticeApproach` helper + spawn-side wiring

**Files:**
- Create: `sim/practice.go`
- Modify: `sim/practice_test.go`
- Modify: `sim/spawn_arrivals.go`

- [ ] **Step 1: Write failing tests for `pickPracticeApproach`**

In `sim/practice_test.go`, append:

```go
import (
	// keep existing imports
	"math/rand/v2"
)

func TestPickPracticeApproach_PicksMatchingActiveRunway(t *testing.T) {
	approaches := map[string]*av.Approach{
		"I22L": {Id: "I22L", Runway: "22L"},
		"I22R": {Id: "I22R", Runway: "22R"},
		"R4":   {Id: "R4", Runway: "4"},
	}
	active := []string{"22L", "22R"}
	r := rand.New(rand.NewPCG(1, 2))

	got := pickPracticeApproach(approaches, active, r)
	if got != "I22L" && got != "I22R" {
		t.Errorf("expected one of {I22L, I22R}, got %q", got)
	}
}

func TestPickPracticeApproach_NoMatchReturnsEmpty(t *testing.T) {
	approaches := map[string]*av.Approach{
		"I22L": {Id: "I22L", Runway: "22L"},
	}
	active := []string{"31R"}
	r := rand.New(rand.NewPCG(1, 2))

	if got := pickPracticeApproach(approaches, active, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestPickPracticeApproach_EmptyApproachesReturnsEmpty(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	if got := pickPracticeApproach(nil, []string{"22L"}, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags vulkan ./sim -run TestPickPracticeApproach -v`
Expected: FAIL — `undefined: pickPracticeApproach`.

- [ ] **Step 3: Implement `pickPracticeApproach` in a new file**

Create `sim/practice.go`:

```go
// sim/practice.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"math/rand/v2"

	av "github.com/mmp/vice/aviation"
)

// pickPracticeApproach returns the Id of a randomly-selected approach
// whose runway matches one of the active arrival runways. Returns "" if
// no approach in the airport's approach map matches any active runway.
func pickPracticeApproach(approaches map[string]*av.Approach, activeRunways []string, r *rand.Rand) string {
	if len(approaches) == 0 || len(activeRunways) == 0 {
		return ""
	}
	active := make(map[string]struct{}, len(activeRunways))
	for _, rwy := range activeRunways {
		active[rwy] = struct{}{}
	}
	var matches []string
	for id, ap := range approaches {
		if _, ok := active[ap.Runway]; ok {
			matches = append(matches, id)
		}
	}
	if len(matches) == 0 {
		return ""
	}
	return matches[r.IntN(len(matches))]
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags vulkan ./sim -run TestPickPracticeApproach -v`
Expected: PASS.

- [ ] **Step 5: Wire spawn-side**

In `sim/spawn_arrivals.go`, find the function that initializes a freshly-spawned arrival aircraft. Reading the code around line 93 (where `s.State.InboundFlows[group].Arrivals` is read) up to wherever `InitializeArrival` is called and the `*Aircraft` is added to `s.Aircraft`, locate the spot **after** the flight plan is set on the aircraft and **before** the aircraft is registered. (Grep `InitializeArrival(` to find the call site.)

After the call to `InitializeArrival`, before `s.Aircraft[ac.ADSBCallsign] = ac` (or its equivalent), add:

```go
// IFR practice-approach setup: roll the dice, seed counter and approach.
if flow := s.State.InboundFlows[group]; flow.PracticeApproaches != nil {
	cfg := flow.PracticeApproaches
	if s.Rand.Float32() < cfg.Probability && ac.FlightPlan.Rules == av.FlightRulesIFR {
		n := cfg.MinMissedApproaches
		if spread := cfg.MaxMissedApproaches - cfg.MinMissedApproaches; spread > 0 {
			n += s.Rand.IntN(spread + 1)
		}
		if airport, ok := av.DB.Airports[ac.FlightPlan.ArrivalAirport]; ok {
			activeRunways := s.activeArrivalRunwaysForAirport(ac.FlightPlan.ArrivalAirport)
			if id := pickPracticeApproach(airport.Approaches, activeRunways, s.Rand.Rand); id != "" {
				ac.MissedApproachesRemaining = n
				ac.PracticeApproachID = id
			}
		}
	}
}
```

`s.Rand` is the existing sim RNG. Verify the exact methods (`Float32`, `IntN`, raw `Rand` accessor) by grepping `s.Rand.` in `sim/`. Use the same idiom the surrounding code uses.

If `s.activeArrivalRunwaysForAirport(...)` does not exist, add this helper either in `sim/practice.go` or alongside the call site (grep `ArrivalRunways` to find the existing data: `s.State.ArrivalRunways` is a slice of `ArrivalRunway` with `Airport` and `Runway` fields):

```go
// activeArrivalRunwaysForAirport returns the runway IDs (e.g. "22L") that
// are active for the given airport in the current scenario.
func (s *Sim) activeArrivalRunwaysForAirport(airport string) []string {
	var rwys []string
	for _, ar := range s.State.ArrivalRunways {
		if ar.Airport == airport {
			rwys = append(rwys, string(ar.Runway))
		}
	}
	return rwys
}
```

- [ ] **Step 6: Build and run sim tests**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK; tests pass.

- [ ] **Step 7: Commit**

```bash
git add sim/practice.go sim/practice_test.go sim/spawn_arrivals.go
git commit -m "$(cat <<'EOF'
sim: seed practice-approach state on spawn

pickPracticeApproach picks a random approach whose runway matches one
of the scenario's active arrival runways for the airport. On every
arrival spawn, if the inbound flow has PracticeApproaches configured,
roll the dice; if it hits, set MissedApproachesRemaining and
PracticeApproachID on the aircraft. VFR aircraft and aircraft for
airports with no matching approach are skipped.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Pilot transmission text builder + `PendingTransmissionPracticeApproachReq` constant

**Files:**
- Modify: `sim/radio.go` (add constant; add fields to `PendingContact`; add a builder)
- Modify: `sim/practice_test.go`

- [ ] **Step 1: Write failing test for the text builder**

In `sim/practice_test.go`, append:

```go
func TestBuildPracticeApproachRequest_LowApproach(t *testing.T) {
	rt := buildPracticeApproachRequest("AAL123", &av.Approach{Id: "I22L", FullName: "ILS Runway 22 Left"}, false)
	if rt == nil {
		t.Fatalf("expected non-nil RadioTransmission")
	}
	written := rt.Written(nil)
	if !strings.Contains(strings.ToLower(written), "ils runway 22 left") {
		t.Errorf("expected approach name in transmission; got %q", written)
	}
	if !strings.Contains(strings.ToLower(written), "for the practice") {
		t.Errorf("expected 'for the practice' phrase; got %q", written)
	}
	if strings.Contains(strings.ToLower(written), "full stop") {
		t.Errorf("low-approach variant should not say 'full stop'; got %q", written)
	}
}

func TestBuildPracticeApproachRequest_FullStop(t *testing.T) {
	rt := buildPracticeApproachRequest("AAL123", &av.Approach{Id: "I22L", FullName: "ILS Runway 22 Left"}, true)
	written := rt.Written(nil)
	if !strings.Contains(strings.ToLower(written), "full stop") {
		t.Errorf("full-stop variant should say 'full stop'; got %q", written)
	}
}
```

Add `"strings"` to the test file imports if not already present.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags vulkan ./sim -run TestBuildPracticeApproachRequest -v`
Expected: FAIL — `undefined: buildPracticeApproachRequest`.

- [ ] **Step 3: Add the constant, the PendingContact fields, and the builder**

In `sim/radio.go`, in the `const ( PendingTransmissionDeparture ... )` block (lines 44-58), add at the end before the closing `)`:

```go
	PendingTransmissionPracticeApproachReq                                     // IFR practice-approach request
```

In the `type PendingContact struct { ... }` block (lines 70-79), add at the end before the closing `}`:

```go
	// IFR practice-approach extras (only meaningful for PendingTransmissionPracticeApproachReq).
	PracticeApproachID       string // av.Approach.Id
	PracticeApproachFullStop bool   // true for the final approach
```

In `sim/practice.go`, append the text builder:

```go
// buildPracticeApproachRequest produces the radio transmission for a
// practice-approach pilot request. FullStop=true switches the phrasing
// from "for the practice" (low approach) to "...this will be a full stop".
func buildPracticeApproachRequest(callsign av.ADSBCallsign, ap *av.Approach, fullStop bool) *av.RadioTransmission {
	if ap == nil {
		return nil
	}
	var written, spoken string
	if fullStop {
		written = fmt.Sprintf("%s, request the %s, this will be a full stop", callsign, ap.FullName)
		spoken = fmt.Sprintf("%s, request the %s, this will be a full stop", callsign, ap.FullName)
	} else {
		written = fmt.Sprintf("%s, request the %s for the practice", callsign, ap.FullName)
		spoken = fmt.Sprintf("%s, request the %s for the practice", callsign, ap.FullName)
	}
	return &av.RadioTransmission{
		WrittenText: written,
		SpokenText:  spoken,
		Type:        av.RadioTransmissionContact,
	}
}
```

Add `"fmt"` to the `sim/practice.go` imports.

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags vulkan ./sim -run TestBuildPracticeApproachRequest -v`
Expected: PASS.

- [ ] **Step 5: Build the tree**

Run: `go build -tags vulkan ./sim/...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add sim/radio.go sim/practice.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: PendingTransmissionPracticeApproachReq + text builder

Adds the new pending-transmission tag and two extras on PendingContact
(approach Id, full-stop flag). buildPracticeApproachRequest produces
"...request the [approach] for the practice" or "...this will be a
full stop" depending on the flag.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Initial-contact transmission firing

**Files:**
- Modify: `sim/radio.go` (the `case PendingTransmissionArrival:` block around line 386 + the queue-on-handoff path around line 195/202)

- [ ] **Step 1: Write failing test**

In `sim/practice_test.go`, append:

```go
func TestEnqueueControllerContact_QueuesPracticeRequestForPracticeAircraft(t *testing.T) {
	s := newTestSimWithSinglePosition(t)            // helper: see existing tests for pattern
	ac := newTestArrivalAircraft(t, s, "AAL123")    // helper
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 2
	s.Aircraft[ac.ADSBCallsign] = ac

	s.enqueueControllerContact(ac, "1A", ac.ControllerFrequency)

	// After the contact + practice request are queued, the queue should hold
	// at least one PendingTransmissionPracticeApproachReq entry.
	var sawPractice bool
	for _, q := range s.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == "AAL123" && pc.Type == PendingTransmissionPracticeApproachReq {
				sawPractice = true
				if pc.PracticeApproachID != "I22L" {
					t.Errorf("PracticeApproachID: want I22L, got %q", pc.PracticeApproachID)
				}
				if pc.PracticeApproachFullStop {
					t.Errorf("PracticeApproachFullStop: want false (counter > 0), got true")
				}
			}
		}
	}
	if !sawPractice {
		t.Errorf("expected a PendingTransmissionPracticeApproachReq queued; got none")
	}
}
```

If `newTestSimWithSinglePosition` and `newTestArrivalAircraft` helpers don't exist, look at `sim/visual_approach_test.go` (around line 2201) for the existing pattern of building a test Sim + Aircraft and copy/adapt.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags vulkan ./sim -run TestEnqueueControllerContact_QueuesPracticeRequestForPracticeAircraft -v`
Expected: FAIL — no practice request queued.

- [ ] **Step 3: Modify `enqueueControllerContact` to chain the practice request**

In `sim/radio.go`, in `enqueueControllerContact` (line 195), after the existing `s.addPendingContact(PendingContact{...})` call (around line 202–211), add:

```go
	// For IFR practice-approach traffic, queue the pilot request immediately
	// after the check-in. Same TCP, same ReadyTime + a small spread so the
	// transmissions don't collide on the audio bus.
	if ac.PracticeApproachID != "" {
		readyDelay := s.Rand.DurationRange(2*time.Second, 4*time.Second)
		s.addPendingContact(PendingContact{
			ADSBCallsign:             ac.ADSBCallsign,
			TCP:                      tcp,
			ReadyTime:                s.State.SimTime.Add(switchDelay + listenDelay + readyDelay),
			Type:                     PendingTransmissionPracticeApproachReq,
			PracticeApproachID:       ac.PracticeApproachID,
			PracticeApproachFullStop: ac.MissedApproachesRemaining == 0,
		})
		ac.PendingPracticeRequest = false // we've queued it; no other firing point should fire one too
	}
```

`switchDelay` and `listenDelay` are local variables already defined earlier in the function (lines 197-198).

- [ ] **Step 4: Add the rendering case**

In the same file, find the `switch pc.Type {` block (around line 386: `case PendingTransmissionArrival:`). Add a new case at the end of that switch:

```go
	case PendingTransmissionPracticeApproachReq:
		ap := lookupApproach(ac, pc.PracticeApproachID)
		rt = buildPracticeApproachRequest(ac.ADSBCallsign, ap, pc.PracticeApproachFullStop)
```

Add a small helper at the bottom of `sim/practice.go`:

```go
// lookupApproach finds the approach struct on the aircraft's arrival airport
// matching the given Id. Returns nil if not found.
func lookupApproach(ac *Aircraft, id string) *av.Approach {
	if airport, ok := av.DB.Airports[ac.FlightPlan.ArrivalAirport]; ok {
		if ap, ok := airport.Approaches[id]; ok {
			return ap
		}
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -tags vulkan ./sim -run TestEnqueueControllerContact_QueuesPracticeRequestForPracticeAircraft -v`
Expected: PASS.

- [ ] **Step 6: Build the tree**

Run: `go build -tags vulkan ./sim/...`
Expected: exit 0.

- [ ] **Step 7: Commit**

```bash
git add sim/radio.go sim/practice.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: chain practice-approach request after arrival check-in

When enqueueControllerContact runs for a practice aircraft, queue a
PendingTransmissionPracticeApproachReq right after the existing arrival
check-in (small spread so they don't collide on the audio bus). The
new switch case in popReadyContact rendering builds the transmission
text via buildPracticeApproachRequest.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Stash `PracticeApproachController` in `ClearedApproach`

**Files:**
- Modify: `sim/approach.go:251-263`
- Modify: `sim/practice_test.go`

- [ ] **Step 1: Write failing test**

In `sim/practice_test.go`, append:

```go
func TestClearedApproach_StashesPracticeController(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL123")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 1
	ac.ControllerFrequency = ControlPosition("1A")
	s.Aircraft[ac.ADSBCallsign] = ac

	// Issue a clearance for the configured approach.
	if _, err := s.ClearedApproach(testTCW, "AAL123", "I22L", false); err != nil {
		t.Fatalf("ClearedApproach: %v", err)
	}

	if ac.PracticeApproachController != "1A" {
		t.Errorf("PracticeApproachController: want %q, got %q", "1A", ac.PracticeApproachController)
	}
}

func TestClearedApproach_NonPracticeAircraftLeavesControllerEmpty(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL124")
	ac.ControllerFrequency = ControlPosition("1A")
	s.Aircraft[ac.ADSBCallsign] = ac

	if _, err := s.ClearedApproach(testTCW, "AAL124", "I22L", false); err != nil {
		t.Fatalf("ClearedApproach: %v", err)
	}

	if ac.PracticeApproachController != "" {
		t.Errorf("PracticeApproachController for non-practice aircraft: want empty, got %q",
			ac.PracticeApproachController)
	}
}
```

`testTCW` is the TCW of the test sim; mirror whatever the existing tests use.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags vulkan ./sim -run TestClearedApproach_StashesPracticeController -v`
Expected: FAIL — `PracticeApproachController` left empty.

- [ ] **Step 3: Modify `ClearedApproach`**

In `sim/approach.go`, change `ClearedApproach` (line 251-263) to:

```go
func (s *Sim) ClearedApproach(tcw TCW, callsign av.ADSBCallsign, approach string, straightIn bool) (av.CommandIntent, error) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	return s.dispatchControlledAircraftCommand(tcw, callsign,
		func(tcw TCW, ac *Aircraft) av.CommandIntent {
			if ac.PracticeApproachID != "" {
				ac.PracticeApproachController = TCP(ac.ControllerFrequency)
			}
			if straightIn {
				return ac.ClearedStraightInApproach(approach, s.State.SimTime, s.lg)
			} else {
				return ac.ClearedApproach(approach, s.State.SimTime, s.lg)
			}
		})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -tags vulkan ./sim -run TestClearedApproach -v`
Expected: PASS.

- [ ] **Step 5: Build and run all sim tests**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK, tests pass.

- [ ] **Step 6: Commit**

```bash
git add sim/approach.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: stash approach controller TCP on practice aircraft at clearance

When the user issues C<approach> on a practice aircraft, stash the
issuing controller's TCP onto ac.PracticeApproachController. That's the
controller the aircraft will be handed back to when tower hands it off
on miss. Refreshed every clearance so it stays current across loops.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: `practiceMissedApproach()` body + `goAround()` branch

**Files:**
- Modify: `sim/goaround.go:34-72`
- Modify: `sim/practice.go`
- Modify: `sim/practice_test.go`

- [ ] **Step 1: Write failing test**

In `sim/practice_test.go`, append:

```go
func TestGoAround_PracticeAircraftTakesLoopBranch(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL123")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 2
	ac.PracticeApproachController = "1A"
	ac.ControllerFrequency = ControlPosition("TWR")
	// Pretend the aircraft is on an approach.
	ac.Nav.Approach.Cleared = true
	ac.Nav.Approach.AssignedId = "I22L"
	ac.Nav.Approach.Assigned = &av.Approach{Id: "I22L", Runway: "22L"}
	ac.Nav.Approach.InterceptState = nav.OnApproachCourse
	s.Aircraft[ac.ADSBCallsign] = ac

	s.goAround(ac)

	if ac.MissedApproachesRemaining != 1 {
		t.Errorf("MissedApproachesRemaining: want 1 (decremented), got %d", ac.MissedApproachesRemaining)
	}
	if ac.WentAround {
		t.Errorf("WentAround should not be set for practice aircraft (departure flag)")
	}
	if ac.Nav.Approach.Cleared {
		t.Errorf("Approach.Cleared should be reset to false after practice miss")
	}
	if ac.Nav.Approach.AssignedId != "" {
		t.Errorf("Approach.AssignedId should be cleared; got %q", ac.Nav.Approach.AssignedId)
	}
	if ac.Nav.Approach.Assigned != nil {
		t.Errorf("Approach.Assigned should be nil")
	}
	if ac.PracticeApproachID != "I22L" {
		t.Errorf("PracticeApproachID should persist across loop; got %q", ac.PracticeApproachID)
	}
	if !ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should be set so the post-miss transmission fires")
	}
}

func TestGoAround_PracticeAircraftWithCounterZeroFallsThrough(t *testing.T) {
	// MissedApproachesRemaining == 0 should NOT enter the practice branch -
	// it should run the existing goAround() and treat as departure.
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL124")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 0
	ac.Nav.Approach.Cleared = true
	ac.Nav.Approach.Assigned = &av.Approach{Id: "I22L", Runway: "22L"}
	s.Aircraft[ac.ADSBCallsign] = ac

	s.goAround(ac)

	if !ac.WentAround {
		t.Errorf("WentAround should be true for non-practice goAround path")
	}
}
```

Add `"github.com/mmp/vice/nav"` to the test imports.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags vulkan ./sim -run TestGoAround_PracticeAircraft -v`
Expected: FAIL — practice branch doesn't exist yet.

- [ ] **Step 3: Add `practiceMissedApproach` to `sim/practice.go`**

In `sim/practice.go`, append:

```go
// practiceMissedApproach is the practice-loop branch of goAround. The
// aircraft flies the published miss (or fallback heading/altitude),
// gets handed back to the original approach controller, and rearms
// for another approach clearance.
func (s *Sim) practiceMissedApproach(ac *Aircraft) {
	ac.MissedApproachesRemaining--

	// Reuse the existing go-around heading/altitude assignment. v1 does not
	// model a published-miss waypoint segment - fall back to the same
	// behavior the existing goAround() uses for non-practice aircraft.
	proc := s.getGoAroundProcedureForAircraft(ac)
	approach := ac.Nav.Approach.Assigned
	wp := av.Waypoint{
		Location:       approach.OppositeThreshold,
		Flags:          av.WaypointFlagFlyOver | av.WaypointFlagHasAltRestriction,
		Heading:        int16(proc.Heading),
		AltRestriction: av.MakeAtAltitudeRestriction(float32(proc.Altitude)),
	}
	ac.Nav.GoAroundWithProcedure(float32(proc.Altitude), wp)

	// Reset approach clearance state so a new C<approach> can be issued.
	ac.Nav.Approach.Cleared = false
	ac.Nav.Approach.InterceptState = nav.NotIntercepting
	ac.Nav.Approach.AssignedId = ""
	ac.Nav.Approach.Assigned = nil
	// PracticeApproachID stays - pilot still wants the same approach.

	// Tower no longer owns this aircraft.
	ac.GotContactTower = false
	// SpacingGoAroundDeclined resets so the next final-approach pass re-rolls.
	ac.SpacingGoAroundDeclined = false

	// Hand back to the original approach controller. If the stash is empty
	// (aircraft was never cleared - shouldn't happen in practice), fall back
	// to the airspace's go-around controller (existing helper).
	target := ac.PracticeApproachController
	if target == "" {
		target = s.getGoAroundController(ac)
	}
	if target != "" {
		_ = s.handBackToApproachController(ac, target)
	}

	// Mark the post-miss transmission as owed; level-off detection in Task 9
	// will queue the actual PendingContact when the aircraft is wings-level
	// on the missed-approach altitude.
	ac.PendingPracticeRequest = true
}

// handBackToApproachController issues an in-process handoff from the
// aircraft's current controller to the named TCP. Uses the same field
// the existing HandoffTrack RPC writes to (NASFlightPlan.HandoffController);
// if the target controller has signed off, the handoff sits as a pending
// inbound until someone takes it - same as any other stale handoff.
func (s *Sim) handBackToApproachController(ac *Aircraft, toTCP TCP) error {
	if ac.NASFlightPlan == nil {
		return nil
	}
	ac.NASFlightPlan.HandoffController = toTCP
	return nil
}
```

- [ ] **Step 4: Add the branch at the top of `goAround`**

In `sim/goaround.go`, change `goAround` (line 34) to:

```go
func (s *Sim) goAround(ac *Aircraft) {
	if ac.MissedApproachesRemaining > 0 {
		s.practiceMissedApproach(ac)
		return
	}

	// Capture approach info before anything clears it.
	approach := ac.Nav.Approach.Assigned
	if approach == nil {
		s.lg.Warn("goAround called without assigned approach",
			slog.String("callsign", string(ac.ADSBCallsign)))
		return
	}
	// ...rest of existing function unchanged...
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -tags vulkan ./sim -run TestGoAround_PracticeAircraft -v`
Expected: PASS for both subtests.

- [ ] **Step 6: Run full sim tests**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK, tests pass.

- [ ] **Step 7: Commit**

```bash
git add sim/goaround.go sim/practice.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: practiceMissedApproach loop branch in goAround

Top-of-function check in goAround: if MissedApproachesRemaining > 0,
take the practice branch instead of the existing depart-as-go-around
path. practiceMissedApproach decrements the counter, executes the
existing fallback heading/altitude assignment for the miss, clears
Approach.Cleared/InterceptState/AssignedId/Assigned so a new clearance
can be issued, and hands the aircraft back to the stashed approach
controller. WentAround stays false - practice aircraft remain in
arrival-land throughout.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: Hook landing trigger to call `goAround` for practice aircraft

**Files:**
- Modify: `sim/sim.go:1076-1107` (the `passedWaypoint.Land()` block)
- Modify: `sim/practice_test.go`

- [ ] **Step 1: Write failing test**

In `sim/practice_test.go`, append:

```go
func TestLandHandler_PracticeAircraftGoesAroundInsteadOfLanding(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL125")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 1
	ac.Nav.Approach.Cleared = true
	ac.Nav.Approach.Assigned = &av.Approach{Id: "I22L", Runway: "22L"}
	s.Aircraft[ac.ADSBCallsign] = ac

	// Simulate the aircraft passing the Land waypoint by directly
	// calling the same handler the per-tick loop calls.
	wp := av.Waypoint{Flags: av.WaypointFlagLand}
	s.handlePassedWaypoint(ac, wp) // helper extracted in Step 3

	if _, ok := s.Aircraft[ac.ADSBCallsign]; !ok {
		t.Errorf("aircraft was deleted; expected go-around path instead")
	}
	if ac.MissedApproachesRemaining != 0 {
		t.Errorf("counter should have decremented to 0; got %d", ac.MissedApproachesRemaining)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags vulkan ./sim -run TestLandHandler_PracticeAircraftGoesAroundInsteadOfLanding -v`
Expected: FAIL — aircraft was deleted (existing landing path).

- [ ] **Step 3: Add the practice branch in the `Land()` block**

In `sim/sim.go`, find the `if passedWaypoint.Land() {` block at line 1076 and modify the body. The current body (line 1076-1107) is:

```go
if passedWaypoint.Land() {
	alt := passedWaypoint.AltitudeRestriction()
	lowEnough := alt == nil || ac.Altitude() <= alt.TargetAltitude(ac.Altitude())+200
	if lowEnough {
		// ...record landing, deleteAircraft...
		s.deleteAircraft(ac)
	} else {
		s.goAround(ac)
	}
}
```

Change to:

```go
if passedWaypoint.Land() {
	if ac.MissedApproachesRemaining > 0 {
		// IFR practice aircraft: fly the miss instead of landing.
		s.goAround(ac)
	} else {
		alt := passedWaypoint.AltitudeRestriction()
		lowEnough := alt == nil || ac.Altitude() <= alt.TargetAltitude(ac.Altitude())+200
		if lowEnough {
			// ...record landing, deleteAircraft (existing code unchanged)...
			s.deleteAircraft(ac)
		} else {
			s.goAround(ac)
		}
	}
}
```

(Preserve the existing landing-recording code inside the `lowEnough` branch verbatim.)

- [ ] **Step 4: (Optional) Extract a helper to make the test addressable**

If the test in Step 1 fails to compile because `s.handlePassedWaypoint` doesn't exist, either:
- Inline the test by exercising `s.RunSimStep()` for one tick on a positioned aircraft so the existing per-tick loop runs the Land() handler. (Recommended — exercises the real path.)
- Or extract the inner handler into a private `handlePassedWaypoint(ac, wp)` method and call from both the existing loop and the test.

The first option is preferred — only extract a helper if the loop path is too entangled to test in isolation.

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -tags vulkan ./sim -run TestLandHandler_PracticeAircraftGoesAroundInsteadOfLanding -v`
Expected: PASS.

- [ ] **Step 6: Build and run sim tests**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK, tests pass.

- [ ] **Step 7: Commit**

```bash
git add sim/sim.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: route practice aircraft to goAround at the runway threshold

In the Land() waypoint handler, if MissedApproachesRemaining > 0, take
the goAround() path instead of recording a landing. The existing
landing-recording and aircraft-delete logic is unchanged for normal
arrivals (counter == 0), and the existing not-low-enough branch still
calls goAround() too.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Post-miss transmission firing (level-off detection)

**Files:**
- Modify: `sim/practice.go` (add `processPendingPracticeRequests`)
- Modify: `sim/sim.go` (call from per-tick update loop)
- Modify: `sim/practice_test.go`

- [ ] **Step 1: Write failing test**

In `sim/practice_test.go`, append:

```go
func TestProcessPendingPracticeRequests_FiresOnLevelOff(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL126")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 1
	ac.PracticeApproachController = "1A"
	ac.PendingPracticeRequest = true
	ac.ControllerFrequency = ControlPosition("1A")
	// Simulate level on the miss: ac.Altitude() within tolerance of target,
	// |VerticalSpeed| under threshold.
	setLevelAtMissAltitude(t, ac) // helper - sets nav state to "level"
	s.Aircraft[ac.ADSBCallsign] = ac

	s.processPendingPracticeRequests()

	if ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should be cleared once the request is queued")
	}
	var sawPractice bool
	for _, q := range s.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == "AAL126" && pc.Type == PendingTransmissionPracticeApproachReq {
				sawPractice = true
				if pc.PracticeApproachFullStop {
					t.Errorf("FullStop should be false when MissedApproachesRemaining > 0")
				}
			}
		}
	}
	if !sawPractice {
		t.Errorf("expected post-miss practice request to be queued")
	}
}

func TestProcessPendingPracticeRequests_DoesNotFireWhenStillClimbing(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL127")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 1
	ac.PendingPracticeRequest = true
	setClimbingOnMiss(t, ac) // helper - aircraft still climbing
	s.Aircraft[ac.ADSBCallsign] = ac

	s.processPendingPracticeRequests()

	if !ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should remain set while still climbing")
	}
}
```

The helpers `setLevelAtMissAltitude` and `setClimbingOnMiss` set `ac.Nav.FlightState.AltitudeRate` directly: `0` for level, `1500` for climbing. That's the only field `isLevelOnMissSegment` reads, so the helpers can be one-liners.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test -tags vulkan ./sim -run TestProcessPendingPracticeRequests -v`
Expected: FAIL — `undefined: processPendingPracticeRequests`.

- [ ] **Step 3: Add `processPendingPracticeRequests` to `sim/practice.go`**

```go
// processPendingPracticeRequests scans aircraft with PendingPracticeRequest
// set and queues the post-miss request transmission once the aircraft is
// stabilized on the missed-approach altitude. Called once per sim tick.
func (s *Sim) processPendingPracticeRequests() {
	for _, ac := range s.Aircraft {
		if !ac.PendingPracticeRequest {
			continue
		}
		if !s.isLevelOnMissSegment(ac) {
			continue
		}

		tcp := ac.PracticeApproachController
		if tcp == "" {
			tcp = TCP(ac.ControllerFrequency)
		}
		if tcp == "" {
			continue // no one to talk to; try again next tick
		}

		readyDelay := s.Rand.DurationRange(2*time.Second, 5*time.Second)
		s.addPendingContact(PendingContact{
			ADSBCallsign:             ac.ADSBCallsign,
			TCP:                      tcp,
			ReadyTime:                s.State.SimTime.Add(readyDelay),
			Type:                     PendingTransmissionPracticeApproachReq,
			PracticeApproachID:       ac.PracticeApproachID,
			PracticeApproachFullStop: ac.MissedApproachesRemaining == 0,
		})
		ac.PendingPracticeRequest = false
	}
}

// isLevelOnMissSegment reports whether the aircraft has stopped climbing
// out from the missed approach. We use AltitudeRate (already on
// nav.FlightState, ft/min, positive = climb) as the level-off signal:
// once |rate| < 100 fpm, the aircraft has reached its assigned altitude
// and is in steady cruise on the miss heading.
func (s *Sim) isLevelOnMissSegment(ac *Aircraft) bool {
	const rateTol = 100.0 // feet per minute
	rate := ac.Nav.FlightState.AltitudeRate
	if rate < 0 {
		rate = -rate
	}
	return rate < rateTol
}
```

Add `"time"` to the `sim/practice.go` imports if not already present.

- [ ] **Step 4: Call from the per-tick loop**

In `sim/sim.go`, find the per-tick update function (grep for `processPendingContacts` or similar — there's a per-tick handler that processes deferred operations). Add a call near the existing pending-contact processing:

```go
s.processPendingPracticeRequests()
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test -tags vulkan ./sim -run TestProcessPendingPracticeRequests -v`
Expected: PASS for both sub-cases.

- [ ] **Step 6: Build and run sim tests**

Run: `go build -tags vulkan ./sim/... && go test -tags vulkan ./sim/... -count=1 -short`
Expected: build OK, tests pass.

- [ ] **Step 7: Commit**

```bash
git add sim/practice.go sim/sim.go sim/practice_test.go
git commit -m "$(cat <<'EOF'
sim: post-miss practice-approach transmission on level-off

Once practiceMissedApproach has set PendingPracticeRequest, the per-tick
loop scans for stabilized aircraft (altitude within 100 ft of target,
|VS| under 200 fpm) and queues the practice request transmission. The
FullStop flag is set when MissedApproachesRemaining is 0 - meaning the
aircraft is now requesting its final approach. The flag clears once
the request is queued so the transmission only fires once per miss.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: End-to-end integration test

**Files:**
- Create: `sim/practice_integration_test.go`

- [ ] **Step 1: Write the integration test**

```go
// sim/practice_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
)

func TestPracticeApproach_TwoMissesThenLand(t *testing.T) {
	s := newTestSimWithSinglePosition(t)
	ac := newTestArrivalAircraft(t, s, "AAL999")
	ac.PracticeApproachID = "I22L"
	ac.MissedApproachesRemaining = 2
	ac.PracticeApproachController = "1A"
	ac.ControllerFrequency = ControlPosition("1A")
	s.Aircraft[ac.ADSBCallsign] = ac

	// Initial contact transmission queue check.
	s.enqueueControllerContact(ac, "1A", ac.ControllerFrequency)
	if !hasPracticeRequest(s, "AAL999", false /*fullStop*/) {
		t.Fatalf("expected initial-contact practice request with fullStop=false")
	}

	// Loop: simulate 2 missed approaches.
	for i := 0; i < 2; i++ {
		// Cleared for the approach (each clearance refreshes the stash).
		if _, err := s.ClearedApproach(testTCW, "AAL999", "I22L", false); err != nil {
			t.Fatalf("iter %d ClearedApproach: %v", i, err)
		}

		// Simulate reaching the runway threshold.
		passLandWaypoint(t, s, ac)

		// The goAround branch ran; counter has decremented; PendingPracticeRequest set.
		expected := 2 - (i + 1)
		if ac.MissedApproachesRemaining != expected {
			t.Errorf("after miss %d: counter want %d, got %d", i+1, expected, ac.MissedApproachesRemaining)
		}
		if !ac.PendingPracticeRequest {
			t.Errorf("after miss %d: PendingPracticeRequest should be set", i+1)
		}

		// Simulate level-off and re-fire the post-miss transmission.
		setLevelAtMissAltitude(t, ac)
		s.processPendingPracticeRequests()

		fullStop := ac.MissedApproachesRemaining == 0
		if !hasPracticeRequest(s, "AAL999", fullStop) {
			t.Errorf("after miss %d: expected practice request with fullStop=%v", i+1, fullStop)
		}
	}

	// Third pass: counter is 0, aircraft should land normally.
	if _, err := s.ClearedApproach(testTCW, "AAL999", "I22L", false); err != nil {
		t.Fatalf("final ClearedApproach: %v", err)
	}
	passLandWaypoint(t, s, ac)
	if _, ok := s.Aircraft[ac.ADSBCallsign]; ok {
		t.Errorf("expected aircraft to be deleted on final landing, still present")
	}
}

func hasPracticeRequest(s *Sim, callsign av.ADSBCallsign, fullStop bool) bool {
	for _, q := range s.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == callsign && pc.Type == PendingTransmissionPracticeApproachReq &&
				pc.PracticeApproachFullStop == fullStop {
				return true
			}
		}
	}
	return false
}

// passLandWaypoint exercises the same Land()-handler path the per-tick
// loop runs. If sim.go was refactored to expose handlePassedWaypoint
// (Task 9 step 4 alternative), call that. Otherwise, exercise via RunSimStep
// after positioning the aircraft on the threshold.
func passLandWaypoint(t *testing.T, s *Sim, ac *Aircraft) {
	t.Helper()
	wp := av.Waypoint{Flags: av.WaypointFlagLand}
	s.handlePassedWaypoint(ac, wp) // adapt to whichever name was used in Task 9
}
```

Reuse the helpers added in earlier tasks (`newTestSimWithSinglePosition`, `newTestArrivalAircraft`, `setLevelAtMissAltitude`).

- [ ] **Step 2: Run the integration test**

Run: `go test -tags vulkan ./sim -run TestPracticeApproach_TwoMissesThenLand -v`
Expected: PASS.

- [ ] **Step 3: Run the full sim test suite**

Run: `go test -tags vulkan ./sim/... -count=1`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git add sim/practice_integration_test.go
git commit -m "$(cat <<'EOF'
sim: integration test for IFR practice-approach loop

End-to-end: spawn a practice aircraft with MissedApproachesRemaining=2,
clear-and-miss twice, verify counter decrements, PendingPracticeRequest
fires on each level-off with the right FullStop flag, and on the third
pass the aircraft lands and is deleted. Covers the spec's Task-10
manual matrix as an automated regression.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 12: Manual end-to-end verification

**Files:** none (this is a hand-off task that requires running the binary).

- [ ] **Step 1: Build the binary**

Run: `go build -tags vulkan -o vice.exe ./cmd/vice`
Expected: exit 0; `vice.exe` produced.

- [ ] **Step 2: Add practice config to a local scenario**

Pick a scenario JSON the user already runs (e.g., one with KFRG arrivals). Edit the inbound-flow group to add:

```json
"practice_approaches": {
  "probability": 1.0,
  "min_missed_approaches": 1,
  "max_missed_approaches": 2
}
```

(Probability 1.0 forces every spawn to be a practice aircraft for testing.)

- [ ] **Step 3: Run the binary against the modified scenario**

Run vice as usual, sign onto the approach controller position, and watch the next inbound aircraft.

- [ ] **Step 4: Walk through the full matrix**

Verify on the running sim:

1. **Initial contact.** The pilot says "with you, [altitude], request the [approach] for the practice." (Or "this will be a full stop" if `min_missed_approaches=0`.)
2. **First clearance.** Issue `C<approach>`. Aircraft flies the approach down to the threshold.
3. **First miss.** Aircraft does *not* land — climbs back out and gets handed back to your position. Pilot says "request the [approach] for the practice" again on level-off.
4. **Re-clearance.** Issue `C<approach>` again. Loop repeats per the random count.
5. **Final approach.** On the last iteration the pilot's request is "...this will be a full stop." Issue `C<approach>`, aircraft lands and is deleted.
6. **Cosmetic.** Scratchpad and FDB are unchanged from a normal arrival (per spec).

- [ ] **Step 5: Report any deviations**

If any step fails (pilot says wrong thing, aircraft departs instead of looping back, hand-back goes to wrong controller, scratchpad shows unexpected text), file follow-up tickets with the specific failure. Do **not** patch the failure as part of this plan — open a new plan for any regression.

- [ ] **Step 6: Restore the scenario**

Revert the temporary `practice_approaches` block in the scenario JSON so day-to-day usage isn't affected. (Or, if you want practice traffic ongoing, dial `probability` down to a sensible value like `0.1`.)

---

## Self-Review Checklist

After implementing all tasks above, the agent should verify:

1. `grep -rn "MissedApproachesRemaining\|PracticeApproachID\|PracticeApproachController\|PendingPracticeRequest\|PendingTransmissionPracticeApproachReq\|PracticeApproachConfig\|practiceMissedApproach\|pickPracticeApproach\|buildPracticeApproachRequest\|processPendingPracticeRequests\|handBackToApproachController\|isLevelOnMissSegment" --include="*.go"` returns matches in the expected files only.
2. `go build -tags vulkan ./...` succeeds (modulo the pre-existing `cmd/wxingest` Linux-only `syscall.Statfs` issue, which is unrelated).
3. `go test -tags vulkan ./sim/... ./aviation/... ./server/... -count=1 -short` passes.
4. The integration test `TestPracticeApproach_TwoMissesThenLand` passes.
5. Manual matrix in Task 12 succeeds.
