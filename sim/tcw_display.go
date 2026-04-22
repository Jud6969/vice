// sim/tcw_display.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"github.com/mmp/vice/math"
)

// TCWDisplayState is the set of STARS display state that is shared
// across all relief controllers occupying a single TCW. One exists per
// active TCW on the Sim; see stars/shared_fields.go for the field
// inventory. It is not persisted to disk -- it lives for the sim
// session only.
type TCWDisplayState struct {
	ScopeView ScopeViewState

	// Monotonic revision, bumped on every mutation. Clients can send
	// last-seen rev to the server for diff detection in future plans.
	Rev uint64
}

// ScopeViewState holds flat scope-view fields that are synced.
// This plan adds only Range, UserCenter, and RangeRingRadius;
// subsequent plans extend this struct.
type ScopeViewState struct {
	Range           float32
	UserCenter      math.Point2LL
	RangeRingRadius int
}

// NewTCWDisplayState constructs a state seeded from a scope view
// snapshot, starting at Rev=1.
func NewTCWDisplayState(seed ScopeViewState) *TCWDisplayState {
	return &TCWDisplayState{
		ScopeView: seed,
		Rev:       1,
	}
}

// SetRange updates the shared range and bumps Rev.
func (s *TCWDisplayState) SetRange(r float32) {
	s.ScopeView.Range = r
	s.Rev++
}

// SetUserCenter updates the shared center and bumps Rev.
func (s *TCWDisplayState) SetUserCenter(p math.Point2LL) {
	s.ScopeView.UserCenter = p
	s.Rev++
}

// SetRangeRingRadius updates the shared range-ring radius (nm) and bumps Rev.
func (s *TCWDisplayState) SetRangeRingRadius(r int) {
	s.ScopeView.RangeRingRadius = r
	s.Rev++
}

// GetTCWDisplay returns the shared state for the given TCW or nil if
// it has not been created yet. Caller must hold s.mu.
func (s *Sim) GetTCWDisplay(tcw TCW) *TCWDisplayState {
	return s.TCWDisplay[tcw]
}

// EnsureTCWDisplay returns the existing shared state for the TCW or
// lazily creates one seeded from `seed` if none exists. Caller must
// hold s.mu.
func (s *Sim) EnsureTCWDisplay(tcw TCW, seed ScopeViewState) *TCWDisplayState {
	if s.TCWDisplay == nil {
		s.TCWDisplay = make(map[TCW]*TCWDisplayState)
	}
	if d, ok := s.TCWDisplay[tcw]; ok {
		return d
	}
	d := NewTCWDisplayState(seed)
	s.TCWDisplay[tcw] = d
	return d
}
