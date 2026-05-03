// sim/practice_test.go
package sim

import (
	"strings"
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/rand"
	"github.com/vmihailenco/msgpack/v5"
)

// TestAircraft_PracticeFieldsRoundTrip verifies that the four IFR
// practice-approach fields round-trip through msgpack serialization
// (the project's real serialization mechanism — sim.Time only implements
// msgpack, not gob, because its underlying time.Time is held in an
// unexported field).
func TestAircraft_PracticeFieldsRoundTrip(t *testing.T) {
	ac := &Aircraft{
		ADSBCallsign:               av.ADSBCallsign("AAL123"),
		MissedApproachesRemaining:  3,
		PracticeApproachID:         "I22L",
		PracticeApproachController: "1A",
		PendingPracticeRequest:     true,
	}

	buf, err := msgpack.Marshal(ac)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got Aircraft
	if err := msgpack.Unmarshal(buf, &got); err != nil {
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
}

func TestPickPracticeApproach_PicksMatchingActiveRunway(t *testing.T) {
	approaches := map[string]*av.Approach{
		"I22L": {Id: "I22L", Runway: "22L"},
		"I22R": {Id: "I22R", Runway: "22R"},
		"R4":   {Id: "R4", Runway: "4"},
	}
	active := []string{"22L", "22R"}
	r := rand.Make()

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
	r := rand.Make()

	if got := pickPracticeApproach(approaches, active, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestPickPracticeApproach_EmptyApproachesReturnsEmpty(t *testing.T) {
	r := rand.Make()
	if got := pickPracticeApproach(nil, []string{"22L"}, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

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

// TestEnqueueControllerContact_QueuesPracticeRequestForPracticeAircraft
// verifies that handing a practice-approach arrival to a controller queues
// BOTH the standard arrival check-in and a follow-on practice-approach
// request. Uses NewVisualScenario for the Sim/Aircraft scaffolding.
func TestEnqueueControllerContact_QueuesPracticeRequestForPracticeAircraft(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 2
	ac.PendingPracticeRequest = true

	tcp := TCP(ac.ControllerFrequency)
	vs.Sim.enqueueControllerContact(ac, tcp, ac.ControllerFrequency)

	// After the contact + practice request are queued, the queue should hold
	// at least one PendingTransmissionPracticeApproachReq entry.
	var sawPractice bool
	for _, q := range vs.Sim.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == ac.ADSBCallsign && pc.Type == PendingTransmissionPracticeApproachReq {
				sawPractice = true
				if pc.PracticeApproachID != "I13L" {
					t.Errorf("PracticeApproachID: want I13L, got %q", pc.PracticeApproachID)
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
	if ac.PendingPracticeRequest {
		t.Errorf("expected PendingPracticeRequest cleared after queue, still true")
	}
}

// TestClearedApproach_StashesPracticeController verifies that issuing a
// C<approach> clearance to a practice aircraft stashes the issuing
// controller's TCP onto ac.PracticeApproachController. That's the
// controller the aircraft will be handed back to on miss.
func TestClearedApproach_StashesPracticeController(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 1
	ac.ControllerFrequency = ControlPosition("1A")

	if _, err := vs.Sim.ClearedApproach(vs.tcw, ac.ADSBCallsign, "I13L", false); err != nil {
		t.Fatalf("ClearedApproach: %v", err)
	}

	if ac.PracticeApproachController != "1A" {
		t.Errorf("PracticeApproachController: want %q, got %q", "1A", ac.PracticeApproachController)
	}
}

// TestClearedApproach_NonPracticeAircraftLeavesControllerEmpty verifies
// that aircraft without PracticeApproachID get an empty
// PracticeApproachController after clearance — i.e., the stash logic is
// gated on practice aircraft.
func TestClearedApproach_NonPracticeAircraftLeavesControllerEmpty(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.ControllerFrequency = ControlPosition("1A")

	if _, err := vs.Sim.ClearedApproach(vs.tcw, ac.ADSBCallsign, "I13L", false); err != nil {
		t.Fatalf("ClearedApproach: %v", err)
	}

	if ac.PracticeApproachController != "" {
		t.Errorf("PracticeApproachController for non-practice aircraft: want empty, got %q",
			ac.PracticeApproachController)
	}
}
