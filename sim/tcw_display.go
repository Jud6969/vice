// sim/tcw_display.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"github.com/mmp/vice/math"
)

// TCWDisplayState is the set of STARS display state that is shared
// across all relief controllers occupying a single TCW. One exists per
// active TCW on the Sim. It is not persisted to disk -- it lives for
// the sim session only.
type TCWDisplayState struct {
	// Annotations holds per-aircraft STARS track annotations keyed by
	// ACID. Entries are created lazily when a controller first sets a
	// field for an ACID, and pruned when the aircraft leaves the sim.
	Annotations map[ACID]TrackAnnotations

	// Monotonic revision, bumped on every mutation. Clients can send
	// last-seen rev to the server for diff detection in future plans.
	Rev uint64
}

// TrackAnnotations is the subset of stars.TrackState that is shared
// across all relief controllers at a TCW. Each field maps 1:1 to its
// counterpart on TrackState.
type TrackAnnotations struct {
	JRingRadius float32
	ConeLength  float32

	LeaderLineDirection     *math.CardinalOrdinalDirection
	FDAMLeaderLineDirection *math.CardinalOrdinalDirection
	UseGlobalLeaderLine     bool

	DisplayFDB bool
	DisplayPTL bool

	DisplayTPASize       *bool
	DisplayATPAMonitor   *bool
	DisplayATPAWarnAlert *bool

	DisplayRequestedAltitude *bool
	DisplayLDBBeaconCode     bool
}

// GetTCWDisplay returns the shared state for the given TCW or nil if
// it has not been created yet. Caller must hold s.mu.
func (s *Sim) GetTCWDisplay(tcw TCW) *TCWDisplayState {
	return s.TCWDisplay[tcw]
}

// EnsureTCWDisplay returns the existing shared state for the TCW or
// lazily creates one if none exists. Caller must hold s.mu.
func (s *Sim) EnsureTCWDisplay(tcw TCW) *TCWDisplayState {
	if s.TCWDisplay == nil {
		s.TCWDisplay = make(map[TCW]*TCWDisplayState)
	}
	if d, ok := s.TCWDisplay[tcw]; ok {
		return d
	}
	d := &TCWDisplayState{
		Annotations: make(map[ACID]TrackAnnotations),
	}
	s.TCWDisplay[tcw] = d
	return d
}

// mutateTrackAnnotation acquires the sim lock, ensures a TCWDisplay +
// per-ACID entry exist, applies `f` to the entry in place, writes it
// back, and bumps Rev. All SetTrack* helpers below share this shape.
func (s *Sim) mutateTrackAnnotation(tcw TCW, acid ACID, f func(*TrackAnnotations)) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	d := s.EnsureTCWDisplay(tcw)
	entry := d.Annotations[acid]
	f(&entry)
	d.Annotations[acid] = entry
	d.Rev++
}

func (s *Sim) SetTrackJRingRadius(tcw TCW, acid ACID, v float32) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.JRingRadius = v })
}

func (s *Sim) SetTrackConeLength(tcw TCW, acid ACID, v float32) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.ConeLength = v })
}

func (s *Sim) SetTrackLeaderLineDirection(tcw TCW, acid ACID, v *math.CardinalOrdinalDirection) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.LeaderLineDirection = v })
}

func (s *Sim) SetTrackFDAMLeaderLineDirection(tcw TCW, acid ACID, v *math.CardinalOrdinalDirection) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.FDAMLeaderLineDirection = v })
}

func (s *Sim) SetTrackUseGlobalLeaderLine(tcw TCW, acid ACID, v bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.UseGlobalLeaderLine = v })
}

func (s *Sim) SetTrackDisplayFDB(tcw TCW, acid ACID, v bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayFDB = v })
}

func (s *Sim) SetTrackDisplayPTL(tcw TCW, acid ACID, v bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayPTL = v })
}

func (s *Sim) SetTrackDisplayTPASize(tcw TCW, acid ACID, v *bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayTPASize = v })
}

func (s *Sim) SetTrackDisplayATPAMonitor(tcw TCW, acid ACID, v *bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayATPAMonitor = v })
}

func (s *Sim) SetTrackDisplayATPAWarnAlert(tcw TCW, acid ACID, v *bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayATPAWarnAlert = v })
}

func (s *Sim) SetTrackDisplayRequestedAltitude(tcw TCW, acid ACID, v *bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayRequestedAltitude = v })
}

func (s *Sim) SetTrackDisplayLDBBeaconCode(tcw TCW, acid ACID, v bool) {
	s.mutateTrackAnnotation(tcw, acid, func(a *TrackAnnotations) { a.DisplayLDBBeaconCode = v })
}

// pruneTCWDisplayAnnotations removes per-ACID annotation entries whose
// ACID is no longer present in the sim's track set. Called from the
// tick loop. Caller must hold s.mu.
func (s *Sim) pruneTCWDisplayAnnotations() {
	if len(s.TCWDisplay) == 0 {
		return
	}
	live := make(map[ACID]bool)
	for _, ac := range s.Aircraft {
		if fp := ac.NASFlightPlan; fp != nil {
			live[fp.ACID] = true
		}
	}
	for _, d := range s.TCWDisplay {
		for acid := range d.Annotations {
			if !live[acid] {
				delete(d.Annotations, acid)
				d.Rev++
			}
		}
	}
}
