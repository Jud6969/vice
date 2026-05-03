// sim/practice.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/nav"
	"github.com/mmp/vice/rand"
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
	return matches[r.Intn(len(matches))]
}

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

// buildPracticeApproachRequest produces the radio transmission for a
// practice-approach pilot request. fullStop=true switches the phrasing
// from "for the practice" (low approach) to "...this will be a full stop".
//
// The text is built from plain literal characters (no {snippet} placeholders
// and no [option|option] brackets) so it renders identically through
// RadioTransmission.Written and .Spoken without needing an *rand.Rand.
// The callsign and ATC-style prefix are added later by the popReadyContact
// pipeline (see sim/radio.go), matching the convention used by every other
// MakeContactTransmission caller in the package.
func buildPracticeApproachRequest(callsign av.ADSBCallsign, ap *av.Approach, fullStop bool) *av.RadioTransmission {
	if ap == nil {
		return nil
	}
	_ = callsign // reserved for future per-callsign phraseology variants
	var text string
	if fullStop {
		text = "request the " + ap.FullName + ", this will be a full stop"
	} else {
		text = "request the " + ap.FullName + " for the practice"
	}
	return av.MakeContactTransmission(text)
}

// lookupApproach finds the approach struct on the aircraft's arrival airport
// matching the given Id. Returns nil if not found.
//
// Uses s.State.Airports (map[string]*av.Airport whose Approaches is
// map[string]*av.Approach) — the right pointer-keyed map for this purpose.
// av.DB.Airports holds FAAAirport values whose Approaches is a value map,
// which is not what callers expect here.
func (s *Sim) lookupApproach(ac *Aircraft, id string) *av.Approach {
	if airport, ok := s.State.Airports[ac.FlightPlan.ArrivalAirport]; ok && airport != nil {
		if ap, ok := airport.Approaches[id]; ok {
			return ap
		}
	}
	return nil
}

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

	// Re-establish "expecting" the approach so the next C<approach> succeeds.
	// nav.GoAroundWithProcedure has reset nav.Approach to zero; we restore the
	// pointer + Id so nav.ClearedApproach finds an Assigned approach and the
	// id-consistency check passes. Cleared stays false until the controller
	// re-issues clearance.
	ac.Nav.Approach.Assigned = approach
	ac.Nav.Approach.AssignedId = ac.PracticeApproachID
	ac.Nav.Approach.Cleared = false
	ac.Nav.Approach.InterceptState = nav.NotIntercepting
	// PracticeApproachID stays - pilot still wants the same approach next time.

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

	// Mark the post-miss transmission as owed; level-off detection in Task 10
	// will queue the actual PendingContact when the aircraft is wings-level
	// on the missed-approach altitude.
	ac.PendingPracticeRequest = true
}

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
