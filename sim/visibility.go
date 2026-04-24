// sim/visibility.go
// Copyright(c) 2022-2025 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/util"
)

// updateVisibility runs once per sim tick and maintains two server-owned
// per-aircraft flags that used to live on the stars-side TrackState:
//
//   - FirstRadarTrackTime: stamped the first tick the aircraft is
//     radar-visible. This mirrors what stars.updateVisibleTracks did
//     per-client/per-frame. Server-side we stamp on the first live tick
//     since isRadarVisible-culled aircraft are excluded from Tracks.
//   - EnteredOurAirspace: flips true the first time the aircraft is
//     inside any airspace volume owned by the TCW that owns its
//     ControllerFrequency. Once true it stays true.
//
// Both are exposed on sim.Track via (*Sim).GetStateUpdate.
//
// Caller must hold s.mu.
func (s *Sim) updateVisibility() {
	now := s.State.SimTime
	for _, ac := range s.Aircraft {
		if !s.isRadarVisible(ac) {
			continue
		}

		if ac.FirstRadarTrackTime.IsZero() {
			ac.FirstRadarTrackTime = now
		}

		if ac.EnteredOurAirspace {
			continue
		}

		// Resolve the TCW owning this aircraft's controller-frequency
		// position, then walk that TCW's airspace volumes. This mirrors
		// the client-side ctx.Client.AirspaceForTCW(ctx.UserTCW) walk
		// but keyed on the aircraft's current controller rather than
		// the viewer. For tracks owned by a human-allocatable TCW this
		// is what the client-side check was really answering since
		// WarnOutsideAirspace bails when the viewer does not own the
		// flight plan.
		tcw := s.State.TCWForPosition(ac.ControllerFrequency)
		if tcw == "" {
			continue
		}
		var vols []av.ControllerAirspaceVolume
		for _, pos := range s.State.GetPositionsForTCW(tcw) {
			for _, avol := range util.SortedMap(s.State.Airspace[pos]) {
				vols = append(vols, avol...)
			}
		}
		if len(vols) == 0 {
			continue
		}
		inside, _ := av.InAirspace(ac.Position(), ac.Altitude(), vols)
		if inside {
			ac.EnteredOurAirspace = true
		}
	}
}
