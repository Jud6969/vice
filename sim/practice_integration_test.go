// sim/practice_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/nav"
)

// TestPracticeApproach_TwoMissesThenLand exercises the full practice-loop:
// a practice aircraft with MissedApproachesRemaining=2 is cleared for the
// approach, reaches the runway threshold (which routes to goAround on the
// first two passes), levels off, and re-fires the post-miss request - twice.
// On the third pass the counter has reached zero and the aircraft lands and
// is removed from the sim. Covers Tasks 6-10 as an end-to-end regression.
func TestPracticeApproach_TwoMissesThenLand(t *testing.T) {
	airportLoc := math.Point2LL{0, 0}
	setupTestRunway(t, "KJFK", av.Runway{Id: "13L", Heading: 130, Threshold: airportLoc, Elevation: 13})

	vs := NewVisualScenario(t, airportLoc, "13L", math.Point2LL{0, 5.0 / 60}, 180)
	ac := vs.AC

	// Stage the practice fields. NewVisualScenario already configured an ILS
	// for runway 13L (AssignedId="I13L", Assigned non-nil) and put the
	// aircraft on the test controller's frequency.
	ac.PracticeApproachID = "I13L"
	ac.MissedApproachesRemaining = 2
	ac.PracticeApproachController = TCP(ac.ControllerFrequency)
	ac.PendingPracticeRequest = true

	// Initial-contact transmission: enqueue the standard arrival check-in
	// plus the practice-approach request that rides with it. fullStop=false
	// because the counter is still > 0.
	tcp := TCP(ac.ControllerFrequency)
	vs.Sim.enqueueControllerContact(ac, tcp, ac.ControllerFrequency)
	if !hasPracticeRequest(vs.Sim, ac.ADSBCallsign, false) {
		t.Fatalf("expected initial-contact practice request with fullStop=false")
	}

	// In production, by the time the aircraft reaches the threshold its
	// InterceptState is OnApproachCourse (set during the nav update loop as
	// it flies the approach course). The test bypasses the nav loop, so we
	// stamp it here once. practiceMissedApproach resets InterceptState back
	// to NotIntercepting on each miss, so we re-stamp it before each
	// ClearedApproach below. The Assigned/AssignedId pointers, by contrast,
	// are now restored by practiceMissedApproach itself (production fix), so
	// no per-iteration re-staging of those is needed.
	ac.Nav.Approach.InterceptState = nav.OnApproachCourse

	// Loop: simulate two missed approaches.
	for i := 0; i < 2; i++ {
		// Re-stamp InterceptState to OnApproachCourse (the prior miss reset
		// it to NotIntercepting). See comment above for why this is a test
		// artifact, not a production requirement.
		ac.Nav.Approach.InterceptState = nav.OnApproachCourse

		if _, err := vs.Sim.ClearedApproach(vs.tcw, ac.ADSBCallsign, "I13L", false); err != nil {
			t.Fatalf("iter %d ClearedApproach: %v", i, err)
		}

		// Simulate reaching the runway threshold. handleLandWaypoint sees
		// MissedApproachesRemaining > 0 and routes to goAround, which
		// invokes practiceMissedApproach.
		passLandWaypoint(t, vs.Sim, ac)

		// The practice branch ran: counter has decremented and
		// PendingPracticeRequest is set so the post-miss transmission
		// fires on level-off.
		expected := 2 - (i + 1)
		if ac.MissedApproachesRemaining != expected {
			t.Errorf("after miss %d: counter want %d, got %d", i+1, expected, ac.MissedApproachesRemaining)
		}
		if !ac.PendingPracticeRequest {
			t.Errorf("after miss %d: PendingPracticeRequest should be set", i+1)
		}

		// Simulate level-off and re-run the per-tick scan.
		setLevelAtMissAltitude(t, ac)
		vs.Sim.processPendingPracticeRequests()

		// After miss 1 (counter 1 -> 0 next pass) the request still has
		// fullStop=false. After miss 2 (counter 0) it flips to fullStop=true.
		fullStop := ac.MissedApproachesRemaining == 0
		if !hasPracticeRequest(vs.Sim, ac.ADSBCallsign, fullStop) {
			t.Errorf("after miss %d: expected practice request with fullStop=%v", i+1, fullStop)
		}
		if ac.PendingPracticeRequest {
			t.Errorf("after miss %d: PendingPracticeRequest should be cleared once queued", i+1)
		}
	}

	// Third pass: counter is 0, aircraft should land normally and be deleted.
	// Re-stamp InterceptState (test artifact - see loop comment above).
	ac.Nav.Approach.InterceptState = nav.OnApproachCourse
	if _, err := vs.Sim.ClearedApproach(vs.tcw, ac.ADSBCallsign, "I13L", false); err != nil {
		t.Fatalf("final ClearedApproach: %v", err)
	}
	// deleteAircraft (called by handleLandWaypoint) touches s.STARSComputer;
	// NewVisualScenario doesn't allocate one, so install a minimal stub.
	vs.Sim.STARSComputer = makeSTARSComputer("TEST")
	passLandWaypoint(t, vs.Sim, ac)
	if _, ok := vs.Sim.Aircraft[ac.ADSBCallsign]; ok {
		t.Errorf("expected aircraft to be deleted on final landing, still present")
	}
}

// hasPracticeRequest reports whether a PendingTransmissionPracticeApproachReq
// for the given callsign with the given FullStop flag is currently queued.
func hasPracticeRequest(s *Sim, callsign av.ADSBCallsign, fullStop bool) bool {
	for _, q := range s.PendingContacts {
		for _, pc := range q {
			if pc.ADSBCallsign == callsign &&
				pc.Type == PendingTransmissionPracticeApproachReq &&
				pc.PracticeApproachFullStop == fullStop {
				return true
			}
		}
	}
	return false
}

// passLandWaypoint exercises the same Land-flag handler the per-tick loop
// runs when an aircraft passes the runway-threshold waypoint.
func passLandWaypoint(t *testing.T, s *Sim, ac *Aircraft) {
	t.Helper()
	wp := av.Waypoint{Flags: av.WaypointFlagLand}
	s.handleLandWaypoint(ac, wp)
}

// setLevelAtMissAltitude simulates the aircraft having stabilized on the
// missed-approach altitude. isLevelOnMissSegment uses |AltitudeRate| < 100
// fpm as the level-off signal.
func setLevelAtMissAltitude(t *testing.T, ac *Aircraft) {
	t.Helper()
	ac.Nav.FlightState.AltitudeRate = 0
}
