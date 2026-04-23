// stars/scope_sync_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"testing"

	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/server"
	"github.com/mmp/vice/sim"
)

// newScopeTestPane builds an sp with local preferences seeded to known
// values. Helpers read ps via currentPrefs() → &sp.prefSet.Current.
func newScopeTestPane(localRange float32, localCenter math.Point2LL, localRRR int) *STARSPane {
	sp := &STARSPane{prefSet: &PreferenceSet{}}
	sp.prefSet.Current.Range = localRange
	sp.prefSet.Current.UserCenter = localCenter
	sp.prefSet.Current.RangeRingRadius = localRRR
	return sp
}

// newScopeTestClient builds a ControlClient with SyncScopeState set as
// requested and an optional TCWDisplay snapshot.
func newScopeTestClient(sync bool, tcw *sim.TCWDisplayState) *client.ControlClient {
	c := &client.ControlClient{SyncScopeState: sync}
	c.State = client.SimState{SimState: server.SimState{}}
	c.State.TCWDisplay = tcw
	return c
}

func TestScopeRangeWhenSyncOffReturnsLocal(t *testing.T) {
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(false, &sim.TCWDisplayState{
		ScopeView: sim.ScopeViewState{Range: 99, UserCenter: math.Point2LL{9, 9}, RangeRingRadius: 77},
	})

	if got := sp.scopeRange(c); got != 10 {
		t.Errorf("scopeRange = %v, want 10 (local, sync off)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{1, 2}) {
		t.Errorf("scopeUserCenter = %v, want {1,2} (local, sync off)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 5 {
		t.Errorf("scopeRangeRingRadius = %v, want 5 (local, sync off)", got)
	}
}

func TestScopeRangeWhenSyncOnAndSharedSeededReturnsShared(t *testing.T) {
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(true, &sim.TCWDisplayState{
		ScopeView: sim.ScopeViewState{Range: 99, UserCenter: math.Point2LL{9, 9}, RangeRingRadius: 77},
	})

	if got := sp.scopeRange(c); got != 99 {
		t.Errorf("scopeRange = %v, want 99 (shared, sync on)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{9, 9}) {
		t.Errorf("scopeUserCenter = %v, want {9,9} (shared, sync on)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 77 {
		t.Errorf("scopeRangeRingRadius = %v, want 77 (shared, sync on)", got)
	}
}

func TestScopeRangeWhenSyncOnAndSharedUnseededFallsBackToLocal(t *testing.T) {
	// Fresh TCW with no scope mutations yet: every ScopeView field is
	// zero. Helpers must fall back to the local preference rather than
	// return a garbage zero.
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(true, &sim.TCWDisplayState{})

	if got := sp.scopeRange(c); got != 10 {
		t.Errorf("scopeRange = %v, want 10 (unseeded shared, fallback)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{1, 2}) {
		t.Errorf("scopeUserCenter = %v, want {1,2} (unseeded shared, fallback)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 5 {
		t.Errorf("scopeRangeRingRadius = %v, want 5 (unseeded shared, fallback)", got)
	}
}

func TestScopeRangeWhenSyncOnAndNoTCWDisplayFallsBackToLocal(t *testing.T) {
	// Client connected with sync on but the first poll hasn't returned a
	// TCWDisplay yet (server-side TCW has no shared state). Fall back to
	// local rather than panic or return zero.
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(true, nil)

	if got := sp.scopeRange(c); got != 10 {
		t.Errorf("scopeRange = %v, want 10 (nil TCWDisplay, fallback)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{1, 2}) {
		t.Errorf("scopeUserCenter = %v, want {1,2} (nil TCWDisplay, fallback)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 5 {
		t.Errorf("scopeRangeRingRadius = %v, want 5 (nil TCWDisplay, fallback)", got)
	}
}
