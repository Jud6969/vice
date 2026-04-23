// server/scope_view_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package server

import (
	"bytes"
	"testing"
)

// TestTwoClientsSeeEachOthersScopePrefsBlob exercises the unified
// scope-prefs blob dispatcher round trip: A pushes a blob, B polls
// and sees it; B pushes a new blob, A polls and sees it. Rev is
// monotonic across the mutations.
func TestTwoClientsSeeEachOthersScopePrefsBlob(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	tokenB := addReliefHuman(t, sm, tcw)

	sd := &dispatcher{sm: sm}

	blob1 := []byte(`{"Range":42}`)
	var upA SimStateUpdate
	if err := sd.SetScopePrefsBlob(
		&SetScopePrefsBlobArgs{ControllerToken: tokenA, Blob: blob1},
		&upA,
	); err != nil {
		t.Fatalf("A SetScopePrefsBlob: %v", err)
	}
	if upA.TCWDisplay == nil || !bytes.Equal(upA.TCWDisplay.ScopePrefsBlob, blob1) {
		t.Errorf("A echo ScopePrefsBlob = %q, want %q", upA.TCWDisplay.ScopePrefsBlob, blob1)
	}
	rev1 := upA.TCWDisplay.ScopePrefsRev

	// B polls and sees A's blob.
	var upB SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB.TCWDisplay == nil || !bytes.Equal(upB.TCWDisplay.ScopePrefsBlob, blob1) {
		t.Errorf("B sees ScopePrefsBlob = %q, want %q", upB.TCWDisplay.ScopePrefsBlob, blob1)
	}
	if upB.TCWDisplay.ScopePrefsRev != rev1 {
		t.Errorf("B sees ScopePrefsRev = %d, want %d", upB.TCWDisplay.ScopePrefsRev, rev1)
	}

	// B pushes a new blob.
	blob2 := []byte(`{"Range":99,"PTLLength":3}`)
	var upB2 SimStateUpdate
	if err := sd.SetScopePrefsBlob(
		&SetScopePrefsBlobArgs{ControllerToken: tokenB, Blob: blob2},
		&upB2,
	); err != nil {
		t.Fatalf("B SetScopePrefsBlob: %v", err)
	}

	// A polls and sees B's blob and a bumped rev.
	var upA2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA2); err != nil {
		t.Fatalf("A GetStateUpdate: %v", err)
	}
	if upA2.TCWDisplay == nil {
		t.Fatal("A.TCWDisplay is nil on poll")
	}
	if !bytes.Equal(upA2.TCWDisplay.ScopePrefsBlob, blob2) {
		t.Errorf("A sees ScopePrefsBlob = %q, want %q", upA2.TCWDisplay.ScopePrefsBlob, blob2)
	}
	if upA2.TCWDisplay.ScopePrefsRev <= rev1 {
		t.Errorf("ScopePrefsRev did not advance: %d -> %d", rev1, upA2.TCWDisplay.ScopePrefsRev)
	}
	if upA2.TCWDisplay.Rev <= upA.TCWDisplay.Rev {
		t.Errorf("Rev did not advance: %d -> %d", upA.TCWDisplay.Rev, upA2.TCWDisplay.Rev)
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

// TestScopePrefsBlobSurvivesRejoin: A pushes a non-default
// scope-prefs blob, leaves; a fresh human joins the same TCW and
// inherits the blob.
func TestScopePrefsBlobSurvivesRejoin(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	sd := &dispatcher{sm: sm}

	blob := []byte(`{"Range":55,"PTLLength":2.5}`)
	if err := sd.SetScopePrefsBlob(
		&SetScopePrefsBlobArgs{ControllerToken: tokenA, Blob: blob},
		&SimStateUpdate{},
	); err != nil {
		t.Fatalf("SetScopePrefsBlob: %v", err)
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
	if !bytes.Equal(upC.TCWDisplay.ScopePrefsBlob, blob) {
		t.Errorf("C did not inherit ScopePrefsBlob: got %q, want %q", upC.TCWDisplay.ScopePrefsBlob, blob)
	}
}
