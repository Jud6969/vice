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
