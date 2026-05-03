// sim/practice.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	av "github.com/mmp/vice/aviation"
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
