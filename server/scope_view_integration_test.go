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

// TestReliefJoinWithSyncScopeStateEnablesSharedMode verifies the
// user-visible contract of the "Sync Scope Setup" checkbox: when a
// relief joins with SyncScopeState=true, the TCW-wide
// ScopeSyncEnabled flag flips on and is visible in SimStateUpdates
// delivered to the primary as well, so both sides route scope reads
// and writes through the shared state.
func TestReliefJoinWithSyncScopeStateEnablesSharedMode(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	sd := &dispatcher{sm: sm}

	// Primary's first poll should see a shared-scope-disabled TCW
	// (either no TCWDisplay at all, or one with the flag clear).
	var upA0 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA0); err != nil {
		t.Fatalf("A GetStateUpdate (pre-join): %v", err)
	}
	if upA0.TCWDisplay != nil && upA0.TCWDisplay.ScopeSyncEnabled {
		t.Fatal("primary sees ScopeSyncEnabled before any opt-in relief joined")
	}

	// Look up the session so we can drive ConnectToSim with a real
	// JoinSimRequest that the manager can match by SimName.
	var session *simSession
	for _, s := range sm.sessionsByName {
		session = s
		break
	}
	if session == nil {
		t.Fatal("no session registered in manager")
	}

	req := &JoinSimRequest{
		SimName:         session.name,
		TCW:             tcw,
		Initials:        "BB",
		JoiningAsRelief: true,
		SyncScopeState:  true,
	}
	var joinResult NewSimResult
	if err := sm.ConnectToSim(req, &joinResult); err != nil {
		t.Fatalf("ConnectToSim: %v", err)
	}

	// Primary polls again: the flag must now be on, even though the
	// primary never ticked the checkbox themselves.
	var upA SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA); err != nil {
		t.Fatalf("A GetStateUpdate (post-join): %v", err)
	}
	if upA.TCWDisplay == nil {
		t.Fatal("A.TCWDisplay is nil after opt-in relief joined")
	}
	if !upA.TCWDisplay.ScopeSyncEnabled {
		t.Error("primary does not see ScopeSyncEnabled after opt-in relief joined")
	}

	// And a plain relief join (SyncScopeState=false) must NOT flip the
	// flag back off once it has been enabled — the feature is sticky
	// for the session.
	req2 := &JoinSimRequest{
		SimName:         session.name,
		TCW:             tcw,
		Initials:        "CC",
		JoiningAsRelief: true,
		SyncScopeState:  false,
	}
	var joinResult2 NewSimResult
	if err := sm.ConnectToSim(req2, &joinResult2); err != nil {
		t.Fatalf("ConnectToSim plain relief: %v", err)
	}
	var upA2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA2); err != nil {
		t.Fatalf("A GetStateUpdate (post-plain-relief): %v", err)
	}
	if upA2.TCWDisplay == nil || !upA2.TCWDisplay.ScopeSyncEnabled {
		t.Error("sticky ScopeSyncEnabled was cleared by a non-opt-in relief join")
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
