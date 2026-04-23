// server/scope_view_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package server

import (
	"testing"

	"github.com/mmp/vice/math"
)

// TestTwoClientsSeeEachOthersScopeViewChange exercises the scope-view
// dispatcher round trip: A mutates Range, B polls and sees it; B
// mutates UserCenter, A polls and sees it; A mutates RangeRingRadius,
// B polls and sees it. Rev is monotonic across the mutations.
func TestTwoClientsSeeEachOthersScopeViewChange(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	tokenB := addReliefHuman(t, sm, tcw)

	sd := &dispatcher{sm: sm}

	// A sets Range.
	var upA SimStateUpdate
	if err := sd.SetTCWRange(
		&SetTCWFloatArgs{ControllerToken: tokenA, Value: 42},
		&upA,
	); err != nil {
		t.Fatalf("A SetTCWRange: %v", err)
	}
	if upA.TCWDisplay == nil || upA.TCWDisplay.ScopeView.Range != 42 {
		t.Errorf("A echo ScopeView.Range = %+v, want 42", upA.TCWDisplay)
	}

	// B polls and sees A's range.
	var upB SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB.TCWDisplay == nil || upB.TCWDisplay.ScopeView.Range != 42 {
		t.Errorf("B sees ScopeView.Range = %+v, want 42", upB.TCWDisplay)
	}

	// B sets UserCenter.
	p := math.Point2LL{-73.5, 40.7}
	var upB2 SimStateUpdate
	if err := sd.SetTCWUserCenter(
		&SetTCWPointArgs{ControllerToken: tokenB, Value: p},
		&upB2,
	); err != nil {
		t.Fatalf("B SetTCWUserCenter: %v", err)
	}

	// A polls and sees B's center and the retained range.
	var upA2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA2); err != nil {
		t.Fatalf("A GetStateUpdate: %v", err)
	}
	if upA2.TCWDisplay == nil {
		t.Fatal("A.TCWDisplay is nil on poll")
	}
	if upA2.TCWDisplay.ScopeView.UserCenter != p {
		t.Errorf("A sees UserCenter = %+v, want %+v", upA2.TCWDisplay.ScopeView.UserCenter, p)
	}
	if upA2.TCWDisplay.ScopeView.Range != 42 {
		t.Errorf("A lost Range: got %v, want 42 retained", upA2.TCWDisplay.ScopeView.Range)
	}

	// A sets RangeRingRadius.
	var upA3 SimStateUpdate
	if err := sd.SetTCWRangeRingRadius(
		&SetTCWIntArgs{ControllerToken: tokenA, Value: 7},
		&upA3,
	); err != nil {
		t.Fatalf("A SetTCWRangeRingRadius: %v", err)
	}

	// B polls and sees all three fields.
	var upB3 SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB3); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB3.TCWDisplay == nil {
		t.Fatal("B.TCWDisplay is nil on poll")
	}
	if upB3.TCWDisplay.ScopeView.RangeRingRadius != 7 {
		t.Errorf("B sees RangeRingRadius = %v, want 7", upB3.TCWDisplay.ScopeView.RangeRingRadius)
	}
	if upB3.TCWDisplay.ScopeView.Range != 42 || upB3.TCWDisplay.ScopeView.UserCenter != p {
		t.Errorf("B lost earlier fields: %+v", upB3.TCWDisplay.ScopeView)
	}

	// Rev advanced across the three mutations.
	if upB3.TCWDisplay.Rev <= upA.TCWDisplay.Rev {
		t.Errorf("Rev did not advance: %d -> %d", upA.TCWDisplay.Rev, upB3.TCWDisplay.Rev)
	}
}

// TestScopeViewSurvivesRejoin: A sets a non-default ScopeView, leaves;
// a fresh human joins the same TCW and inherits the full ScopeView.
func TestScopeViewSurvivesRejoin(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	sd := &dispatcher{sm: sm}

	p := math.Point2LL{-118.4, 33.9}
	if err := sd.SetTCWRange(&SetTCWFloatArgs{ControllerToken: tokenA, Value: 55}, &SimStateUpdate{}); err != nil {
		t.Fatalf("SetTCWRange: %v", err)
	}
	if err := sd.SetTCWUserCenter(&SetTCWPointArgs{ControllerToken: tokenA, Value: p}, &SimStateUpdate{}); err != nil {
		t.Fatalf("SetTCWUserCenter: %v", err)
	}
	if err := sd.SetTCWRangeRingRadius(&SetTCWIntArgs{ControllerToken: tokenA, Value: 12}, &SimStateUpdate{}); err != nil {
		t.Fatalf("SetTCWRangeRingRadius: %v", err)
	}

	if err := sm.SignOff(tokenA); err != nil {
		t.Fatalf("SignOff: %v", err)
	}

	tokenC := newHumanAt(t, sm, tcw)
	var upC SimStateUpdate
	if err := sd.GetStateUpdate(tokenC, &upC); err != nil {
		t.Fatalf("GetStateUpdate C: %v", err)
	}
	if upC.TCWDisplay == nil {
		t.Fatal("C.TCWDisplay is nil")
	}
	sv := upC.TCWDisplay.ScopeView
	if sv.Range != 55 || sv.UserCenter != p || sv.RangeRingRadius != 12 {
		t.Errorf("C did not inherit ScopeView: got %+v, want Range=55 UserCenter=%+v RangeRingRadius=12", sv, p)
	}
}
