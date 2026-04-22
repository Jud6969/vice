// sim/tcw_display_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"io"
	"log/slog"
	"testing"

	"github.com/mmp/vice/log"
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

func TestSignOnSeedsTCWDisplay(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)
	tcw := E2ETCW()
	s.State.Range = 25
	s.State.Center = math.Point2LL{-73.7, 40.6}

	if _, _, err := s.SignOn(tcw, nil); err != nil {
		t.Fatalf("SignOn: %v", err)
	}
	d := s.GetTCWDisplay(tcw)
	if d == nil {
		t.Fatal("GetTCWDisplay returned nil after SignOn")
	}
	if d.ScopeView.Range != 25 {
		t.Errorf("seeded Range = %v, want 25", d.ScopeView.Range)
	}
	if d.ScopeView.UserCenter != (math.Point2LL{-73.7, 40.6}) {
		t.Errorf("seeded UserCenter = %+v, want {-73.7, 40.6}", d.ScopeView.UserCenter)
	}

	// Second SignOn (relief joiner) must not reseed.
	s.TCWDisplay[tcw].SetRange(99)
	if _, _, err := s.SignOn(tcw, nil); err != nil {
		t.Fatalf("second SignOn: %v", err)
	}
	if got := s.GetTCWDisplay(tcw).ScopeView.Range; got != 99 {
		t.Errorf("second SignOn reseeded Range to %v; should have left it at 99", got)
	}
}

func TestGetStateUpdateIncludesTCWDisplay(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)
	tcw := E2ETCW()
	if _, _, err := s.SignOn(tcw, nil); err != nil {
		t.Fatalf("SignOn: %v", err)
	}
	up := s.GetStateUpdate(tcw)
	if up.TCWDisplay == nil {
		t.Fatal("StateUpdate.TCWDisplay is nil after SignOn")
	}
	// Mutate and re-poll.
	s.TCWDisplay[tcw].SetRange(42)
	up2 := s.GetStateUpdate(tcw)
	if up2.TCWDisplay == nil || up2.TCWDisplay.ScopeView.Range != 42 {
		t.Errorf("poll did not observe mutation; got %+v", up2.TCWDisplay)
	}
	// A poll for a TCW with no display state returns nil.
	up3 := s.GetStateUpdate(TCW("UNKNOWN"))
	if up3.TCWDisplay != nil {
		t.Errorf("StateUpdate.TCWDisplay for unknown TCW = %+v, want nil", up3.TCWDisplay)
	}
}

func TestSimEnsureTCWDisplayIsLazy(t *testing.T) {
	s := &Sim{}
	if got := s.GetTCWDisplay("N01"); got != nil {
		t.Errorf("GetTCWDisplay before Ensure returned %+v, want nil", got)
	}
	seed := ScopeViewState{Range: 20, RangeRingRadius: 5}
	d := s.EnsureTCWDisplay("N01", seed)
	if d == nil || d.ScopeView != seed {
		t.Errorf("EnsureTCWDisplay returned %+v, want seeded state", d)
	}
	// Second call must return the same instance (no reseeding).
	d2 := s.EnsureTCWDisplay("N01", ScopeViewState{Range: 999})
	if d2 != d {
		t.Errorf("EnsureTCWDisplay returned new instance on second call")
	}
	if d2.ScopeView.Range != 20 {
		t.Errorf("second EnsureTCWDisplay clobbered existing state: Range=%v", d2.ScopeView.Range)
	}
}
