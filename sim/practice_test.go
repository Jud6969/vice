// sim/practice_test.go
package sim

import (
	"strings"
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/nav"
	"github.com/mmp/vice/rand"
	"github.com/vmihailenco/msgpack/v5"
)

func TestIsAirlineCallsign(t *testing.T) {
	if av.DB == nil || len(av.DB.Callsigns) == 0 {
		av.InitDB()
	}
	cases := []struct {
		callsign string
		want     bool
	}{
		{"AAL123", true},
		{"JBU456", true},
		{"DAL2391", true},
		{"N123AB", false},
		{"N12345", false},
		{"GULF1", false}, // not in Callsigns DB
		{"", false},
	}
	for _, c := range cases {
		t.Run(c.callsign, func(t *testing.T) {
			if got := isAirlineCallsign(av.ADSBCallsign(c.callsign)); got != c.want {
				t.Errorf("isAirlineCallsign(%q) = %v, want %v", c.callsign, got, c.want)
			}
		})
	}
}

func TestCallsignEligibleForPractice_EmptyAllowlistFallsBackToAirlineCheck(t *testing.T) {
	if av.DB == nil || len(av.DB.Callsigns) == 0 {
		av.InitDB()
	}
	// Empty allowlist: airline => not eligible; N-prefix tail => eligible.
	if callsignEligibleForPractice(av.ADSBCallsign("AAL123"), nil) {
		t.Errorf("AAL123 should not be eligible (airline) under empty allowlist")
	}
	if !callsignEligibleForPractice(av.ADSBCallsign("N123AB"), nil) {
		t.Errorf("N123AB should be eligible (GA tail) under empty allowlist")
	}
}

func TestCallsignEligibleForPractice_AllowlistRestrictsToPrefixes(t *testing.T) {
	allowlist := []string{"ERU", "LFA", "BPX"}
	cases := []struct {
		callsign string
		want     bool
	}{
		{"ERU456", true},  // matches allowlist
		{"LFA12", true},   // matches allowlist
		{"BPX99", true},   // matches allowlist
		{"eru456", true},  // case-insensitive
		{"AAL123", false}, // airline, not in allowlist (allowlist supersedes airline check)
		{"N123AB", false}, // GA tail, not in allowlist
	}
	for _, c := range cases {
		t.Run(c.callsign, func(t *testing.T) {
			if got := callsignEligibleForPractice(av.ADSBCallsign(c.callsign), allowlist); got != c.want {
				t.Errorf("callsignEligibleForPractice(%q, %v) = %v, want %v",
					c.callsign, allowlist, got, c.want)
			}
		})
	}
}

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

// TestGoAround_PracticeAircraftTakesLoopBranch verifies that when goAround
// is invoked on a practice aircraft (MissedApproachesRemaining > 0), it
// routes to practiceMissedApproach: counter is decremented, WentAround
// stays false, the approach clearance is reset (Cleared=false,
// InterceptState=NotIntercepting) but the Assigned/AssignedId pair is
// re-established so a new C<approach> from the controller succeeds without
// the user having to re-issue E<approach> first, and PendingPracticeRequest
// is armed for the post-miss transmission.
func TestGoAround_PracticeAircraftTakesLoopBranch(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 2
	ac.PracticeApproachController = "1A"
	ac.ControllerFrequency = ControlPosition("TWR")
	// Pretend the aircraft is on an approach. NewVisualScenario already
	// sets AssignedId and Assigned; just flip the cleared/intercept fields.
	ac.Nav.Approach.Cleared = true
	ac.Nav.Approach.InterceptState = nav.OnApproachCourse
	// Capture the pre-miss approach pointer; it should be restored by
	// practiceMissedApproach so the next ClearedApproach succeeds.
	preApproach := ac.Nav.Approach.Assigned

	vs.Sim.goAround(ac)

	if ac.MissedApproachesRemaining != 1 {
		t.Errorf("MissedApproachesRemaining: want 1 (decremented), got %d", ac.MissedApproachesRemaining)
	}
	if ac.WentAround {
		t.Errorf("WentAround should not be set for practice aircraft (departure flag)")
	}
	if ac.Nav.Approach.Cleared {
		t.Errorf("Approach.Cleared should be reset to false after practice miss")
	}
	if ac.Nav.Approach.AssignedId != ac.PracticeApproachID {
		t.Errorf("Approach.AssignedId should be restored to PracticeApproachID (%q); got %q",
			ac.PracticeApproachID, ac.Nav.Approach.AssignedId)
	}
	if ac.Nav.Approach.Assigned != preApproach {
		t.Errorf("Approach.Assigned should be restored to the pre-miss approach pointer; got %p (want %p)",
			ac.Nav.Approach.Assigned, preApproach)
	}
	if ac.Nav.Approach.InterceptState != nav.NotIntercepting {
		t.Errorf("Approach.InterceptState should be NotIntercepting after miss; got %v",
			ac.Nav.Approach.InterceptState)
	}
	if ac.PracticeApproachID != "I13L" {
		t.Errorf("PracticeApproachID should persist across loop; got %q", ac.PracticeApproachID)
	}
	if !ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should be set so the post-miss transmission fires")
	}
}

// TestGoAround_PracticeAircraftWithCounterZeroFallsThrough verifies that
// MissedApproachesRemaining == 0 does NOT enter the practice branch - the
// existing goAround() path runs instead. We assert the practice branch was
// NOT taken by checking the counter wasn't decremented to -1, which is the
// most robust signal independent of the existing path's other side effects.
func TestGoAround_PracticeAircraftWithCounterZeroFallsThrough(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 0
	ac.Nav.Approach.Cleared = true

	vs.Sim.goAround(ac)

	if ac.MissedApproachesRemaining != 0 {
		t.Errorf("counter should remain 0 (practice branch not taken); got %d",
			ac.MissedApproachesRemaining)
	}
	if !ac.WentAround {
		t.Errorf("WentAround should be true for non-practice goAround path")
	}
}

// TestProcessPendingPracticeRequests_FiresOnLevelOff verifies that the
// per-tick scan queues a PendingTransmissionPracticeApproachReq once the
// aircraft has stabilized at the missed-approach altitude (|AltitudeRate|
// under 100 fpm) and clears PendingPracticeRequest so the request fires
// only once per miss.
func TestProcessPendingPracticeRequests_FiresOnLevelOff(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 1
	ac.PracticeApproachController = "1A"
	ac.PendingPracticeRequest = true
	ac.ControllerFrequency = ControlPosition("1A")
	// Level-off: AltitudeRate within tolerance of zero.
	ac.Nav.FlightState.AltitudeRate = 0

	vs.Sim.processPendingPracticeRequests()

	if ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should be cleared once the request is queued")
	}
	var sawPractice bool
	for _, q := range vs.Sim.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == ac.ADSBCallsign && pc.Type == PendingTransmissionPracticeApproachReq {
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

// TestProcessPendingPracticeRequests_DoesNotFireWhenStillClimbing verifies
// that the scan leaves PendingPracticeRequest set while the aircraft is
// still climbing out from the miss (|AltitudeRate| >= 100 fpm).
func TestProcessPendingPracticeRequests_DoesNotFireWhenStillClimbing(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 1
	ac.PracticeApproachController = "1A"
	ac.PendingPracticeRequest = true
	// Still climbing: AltitudeRate above tolerance.
	ac.Nav.FlightState.AltitudeRate = 1500

	vs.Sim.processPendingPracticeRequests()

	if !ac.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest should remain set while still climbing")
	}
	for _, q := range vs.Sim.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == ac.ADSBCallsign && pc.Type == PendingTransmissionPracticeApproachReq {
				t.Errorf("no PendingTransmissionPracticeApproachReq should be queued while climbing")
			}
		}
	}
}

// TestLandHandler_PracticeAircraftGoesAroundInsteadOfLanding verifies that
// when a practice aircraft passes the runway-threshold "Land" waypoint and
// MissedApproachesRemaining > 0, the Land-waypoint handler routes to
// goAround() (which in turn enters practiceMissedApproach) rather than
// recording a landing and deleting the aircraft.
func TestLandHandler_PracticeAircraftGoesAroundInsteadOfLanding(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 1
	ac.PracticeApproachController = "1A"
	ac.Nav.Approach.Cleared = true
	ac.Nav.Approach.InterceptState = nav.OnApproachCourse

	// Simulate the aircraft passing the Land waypoint by directly calling
	// the handler the per-tick loop uses.
	wp := av.Waypoint{Flags: av.WaypointFlagLand}
	vs.Sim.handleLandWaypoint(ac, wp)

	if _, ok := vs.Sim.Aircraft[ac.ADSBCallsign]; !ok {
		t.Errorf("aircraft was deleted; expected go-around path instead")
	}
	if ac.MissedApproachesRemaining != 0 {
		t.Errorf("counter should have decremented to 0; got %d", ac.MissedApproachesRemaining)
	}
}
