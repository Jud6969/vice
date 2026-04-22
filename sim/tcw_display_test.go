// sim/tcw_display_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"

	"github.com/mmp/vice/math"
)

func TestNewTCWDisplayStateSeedsFromScopeView(t *testing.T) {
	seed := ScopeViewState{
		Range:           30,
		UserCenter:      math.Point2LL{-73.7, 40.6},
		RangeRingRadius: 5,
	}
	s := NewTCWDisplayState(seed)
	if s.Rev != 1 {
		t.Errorf("Rev = %d, want 1", s.Rev)
	}
	if s.ScopeView != seed {
		t.Errorf("ScopeView = %+v, want %+v", s.ScopeView, seed)
	}
}

func TestSetRangeBumpsRev(t *testing.T) {
	s := NewTCWDisplayState(ScopeViewState{Range: 10})
	r0 := s.Rev
	s.SetRange(50)
	if s.ScopeView.Range != 50 {
		t.Errorf("Range = %v, want 50", s.ScopeView.Range)
	}
	if s.Rev != r0+1 {
		t.Errorf("Rev = %d, want %d", s.Rev, r0+1)
	}
	// Idempotent writes still bump Rev (caller is responsible for dedup).
	s.SetRange(50)
	if s.Rev != r0+2 {
		t.Errorf("Rev = %d, want %d", s.Rev, r0+2)
	}
}

func TestSetUserCenterAndRangeRingRadius(t *testing.T) {
	s := NewTCWDisplayState(ScopeViewState{})
	s.SetUserCenter(math.Point2LL{1, 2})
	if got := s.ScopeView.UserCenter; got != (math.Point2LL{1, 2}) {
		t.Errorf("UserCenter = %+v", got)
	}
	s.SetRangeRingRadius(10)
	if s.ScopeView.RangeRingRadius != 10 {
		t.Errorf("RangeRingRadius = %v, want 10", s.ScopeView.RangeRingRadius)
	}
}
