// server/shared_tcw_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package server

import (
	"testing"

	"github.com/mmp/vice/math"
)

// TestTwoClientsSeeEachOthersRangeChange exercises the dispatcher round
// trip end-to-end: A mutates a synced field, B polls and sees it; B
// mutates another, A polls and sees it; A signs off and the shared
// state survives because B is still at the TCW.
func TestTwoClientsSeeEachOthersRangeChange(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	tokenB := addReliefHuman(t, sm, tcw)

	sd := &dispatcher{sm: sm}

	// A changes range to 99.
	var upA SimStateUpdate
	if err := sd.SetTCWRange(&SetTCWRangeArgs{ControllerToken: tokenA, Range: 99}, &upA); err != nil {
		t.Fatalf("A SetTCWRange: %v", err)
	}
	if upA.TCWDisplay == nil {
		t.Fatal("A's echoed TCWDisplay is nil")
	}

	// B polls state.
	var upB SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB.TCWDisplay == nil {
		t.Fatal("B.TCWDisplay is nil")
	}
	if got := upB.TCWDisplay.ScopeView.Range; got != 99 {
		t.Errorf("B sees Range=%v, want 99", got)
	}

	// B changes center.
	p := math.Point2LL{-73.7, 40.6}
	var upB2 SimStateUpdate
	if err := sd.SetTCWUserCenter(&SetTCWUserCenterArgs{ControllerToken: tokenB, Center: p}, &upB2); err != nil {
		t.Fatalf("B SetTCWUserCenter: %v", err)
	}

	// A polls state.
	var upA2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA2); err != nil {
		t.Fatalf("A GetStateUpdate: %v", err)
	}
	if upA2.TCWDisplay == nil || upA2.TCWDisplay.ScopeView.UserCenter != p {
		t.Errorf("A sees UserCenter=%+v, want %+v", upA2.TCWDisplay.ScopeView.UserCenter, p)
	}

	// Rev monotonicity across mutations.
	if upA2.TCWDisplay.Rev <= upA.TCWDisplay.Rev {
		t.Errorf("Rev did not advance: %d -> %d", upA.TCWDisplay.Rev, upA2.TCWDisplay.Rev)
	}

	// Range-ring radius via dispatcher; both clients see it.
	var upA3 SimStateUpdate
	if err := sd.SetTCWRangeRingRadius(&SetTCWRangeRingRadiusArgs{ControllerToken: tokenA, Radius: 7}, &upA3); err != nil {
		t.Fatalf("A SetTCWRangeRingRadius: %v", err)
	}
	var upB3 SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB3); err != nil {
		t.Fatalf("B GetStateUpdate after RR: %v", err)
	}
	if upB3.TCWDisplay == nil || upB3.TCWDisplay.ScopeView.RangeRingRadius != 7 {
		t.Errorf("B sees RangeRingRadius=%+v, want 7", upB3.TCWDisplay)
	}

	// A signs off; the shared state must persist for B.
	if err := sm.SignOff(tokenA); err != nil {
		t.Fatalf("SignOff A: %v", err)
	}
	s := sm.sessionsByToken[tokenB].sim
	if s.GetTCWDisplay(tcw) == nil {
		t.Error("TCWDisplay was cleared when A signed off while B remains")
	}
	if got := s.GetTCWDisplay(tcw).ScopeView.Range; got != 99 {
		t.Errorf("after A signoff, Range=%v, want 99", got)
	}
}
