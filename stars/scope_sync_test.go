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

// newScopeTestClient builds a ControlClient with an optional TCWDisplay
// snapshot. ScopeSyncEnabled on that snapshot is what gates the helpers
// now — the client's local SyncScopeState flag is kept for wire
// round-tripping but no longer affects read/write routing.
func newScopeTestClient(tcw *sim.TCWDisplayState) *client.ControlClient {
	c := &client.ControlClient{}
	c.State = client.SimState{SimState: server.SimState{}}
	c.State.TCWDisplay = tcw
	return c
}

func TestScopeReadsWhenSyncDisabledReturnLocal(t *testing.T) {
	// ScopeView is seeded with non-zero values but ScopeSyncEnabled is
	// false — helpers must ignore shared state.
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(&sim.TCWDisplayState{
		ScopeView: sim.ScopeViewState{Range: 99, UserCenter: math.Point2LL{9, 9}, RangeRingRadius: 77},
	})

	if got := sp.scopeRange(c); got != 10 {
		t.Errorf("scopeRange = %v, want 10 (local, sync disabled)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{1, 2}) {
		t.Errorf("scopeUserCenter = %v, want {1,2} (local, sync disabled)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 5 {
		t.Errorf("scopeRangeRingRadius = %v, want 5 (local, sync disabled)", got)
	}
}

func TestScopeReadsWhenSyncEnabledAndSeededReturnShared(t *testing.T) {
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(&sim.TCWDisplayState{
		ScopeSyncEnabled: true,
		ScopeView:        sim.ScopeViewState{Range: 99, UserCenter: math.Point2LL{9, 9}, RangeRingRadius: 77},
	})

	if got := sp.scopeRange(c); got != 99 {
		t.Errorf("scopeRange = %v, want 99 (shared, sync enabled)", got)
	}
	if got := sp.scopeUserCenter(c); got != (math.Point2LL{9, 9}) {
		t.Errorf("scopeUserCenter = %v, want {9,9} (shared, sync enabled)", got)
	}
	if got := sp.scopeRangeRingRadius(c); got != 77 {
		t.Errorf("scopeRangeRingRadius = %v, want 77 (shared, sync enabled)", got)
	}
}

func TestScopeReadsWhenSyncEnabledAndSharedUnseededFallBackToLocal(t *testing.T) {
	// TCW flipped to shared-scope mode but no one has written yet —
	// every ScopeView field is zero. Helpers fall back to local rather
	// than return a garbage zero so the scope doesn't jump on join.
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(&sim.TCWDisplayState{ScopeSyncEnabled: true})

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

func TestScopeReadsWhenTCWDisplayNilFallBackToLocal(t *testing.T) {
	// Client connected but the first poll hasn't returned a TCWDisplay
	// yet. Fall back to local rather than panic.
	sp := newScopeTestPane(10, math.Point2LL{1, 2}, 5)
	c := newScopeTestClient(nil)

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
