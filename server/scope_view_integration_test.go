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

// TestScopeSyncIsAutomatic verifies that two clients at the same TCW
// both see TCWDisplay != nil on their state updates, and a blob
// pushed by one client round-trips to the other with no opt-in
// needed. This is the client-visible contract that the STARS-side
// scopeSyncActive() gate now depends on.
func TestScopeSyncIsAutomatic(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	tokenB := addReliefHuman(t, sm, tcw)
	sd := &dispatcher{sm: sm}

	// Primary's TCWDisplay must be non-nil out of the box.
	var upA SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA); err != nil {
		t.Fatalf("A GetStateUpdate: %v", err)
	}
	if upA.TCWDisplay == nil {
		t.Fatal("primary A.TCWDisplay is nil (sync gate would refuse to activate)")
	}

	// Relief's TCWDisplay must also be non-nil -- no checkbox needed.
	var upB SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB.TCWDisplay == nil {
		t.Fatal("relief B.TCWDisplay is nil (sync gate would refuse to activate)")
	}

	// A pushes a scope prefs blob; B must see it on its next poll.
	blob := []byte(`{"Range":42}`)
	if err := sd.SetScopePrefsBlob(
		&SetScopePrefsBlobArgs{ControllerToken: tokenA, Blob: blob},
		&SimStateUpdate{},
	); err != nil {
		t.Fatalf("A SetScopePrefsBlob: %v", err)
	}
	var upB2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB2); err != nil {
		t.Fatalf("B GetStateUpdate (post-push): %v", err)
	}
	if upB2.TCWDisplay == nil {
		t.Fatal("B.TCWDisplay is nil after A pushed a blob")
	}
	if !bytes.Equal(upB2.TCWDisplay.ScopePrefsBlob, blob) {
		t.Errorf("B did not see A's blob: got %q, want %q",
			upB2.TCWDisplay.ScopePrefsBlob, blob)
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
