// stars/scope_sync_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"testing"

	"github.com/mmp/vice/client"
	"github.com/mmp/vice/server"
	"github.com/mmp/vice/sim"
)

func TestScopeSyncActiveGatesOnTCWFlag(t *testing.T) {
	cases := []struct {
		name string
		c    *client.ControlClient
		want bool
	}{
		{"nil client", nil, false},
		{"nil TCWDisplay", newSyncTestClient(nil), false},
		{"TCWDisplay present", newSyncTestClient(&sim.TCWDisplayState{}), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeSyncActive(tc.c); got != tc.want {
				t.Errorf("scopeSyncActive = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestApplyScopePrefsBlobPreservesLocalOnlyFields verifies that
// decoding a blob over a Preferences value restores the receiver's
// per-user fields (character size, audio, cursor, dwell, DCB
// position/visibility, restriction-area overrides) rather than
// letting the incoming blob overwrite them.
func TestApplyScopePrefsBlobPreservesLocalOnlyFields(t *testing.T) {
	// Sender's prefs: populate both shared and local-only fields, then
	// encode. encodeScopePrefs zeros the local-only fields before
	// marshaling so they can't leak to the receiver.
	sender := &Preferences{}
	sender.Range = 42
	sender.PTLLength = 3
	sender.CharSize.Datablocks = 7 // local-only: must NOT propagate
	sender.AudioVolume = 77        // local-only: must NOT propagate
	sender.DwellMode = DwellMode(2)
	sender.AutoCursorHome = true
	sender.DisplayDCB = true
	sender.DCBPosition = 3

	blob, err := encodeScopePrefs(sender)
	if err != nil {
		t.Fatalf("encodeScopePrefs: %v", err)
	}

	// Receiver starts with its own local-only values that must
	// survive an apply.
	receiver := &Preferences{}
	receiver.Range = 10 // will get replaced
	receiver.CharSize.Datablocks = 99
	receiver.AudioVolume = 5
	receiver.DwellMode = DwellMode(1)
	receiver.AutoCursorHome = false
	receiver.DisplayDCB = false
	receiver.DCBPosition = 0

	if err := applyScopePrefsBlob(receiver, blob); err != nil {
		t.Fatalf("applyScopePrefsBlob: %v", err)
	}

	// Shared fields were adopted.
	if receiver.Range != 42 {
		t.Errorf("Range = %v, want 42 (shared field adopted)", receiver.Range)
	}
	if receiver.PTLLength != 3 {
		t.Errorf("PTLLength = %v, want 3 (shared field adopted)", receiver.PTLLength)
	}

	// Local-only fields retained the receiver's original values.
	if receiver.CharSize.Datablocks != 99 {
		t.Errorf("CharSize.Datablocks = %v, want 99 (local preserved)", receiver.CharSize.Datablocks)
	}
	if receiver.AudioVolume != 5 {
		t.Errorf("AudioVolume = %v, want 5 (local preserved)", receiver.AudioVolume)
	}
	if receiver.DwellMode != DwellMode(1) {
		t.Errorf("DwellMode = %v, want 1 (local preserved)", receiver.DwellMode)
	}
	if receiver.AutoCursorHome != false {
		t.Errorf("AutoCursorHome = %v, want false (local preserved)", receiver.AutoCursorHome)
	}
	if receiver.DisplayDCB != false {
		t.Errorf("DisplayDCB = %v, want false (local preserved)", receiver.DisplayDCB)
	}
	if receiver.DCBPosition != 0 {
		t.Errorf("DCBPosition = %v, want 0 (local preserved)", receiver.DCBPosition)
	}
}

// TestEncodeScopePrefsDoesNotMutateSource verifies that the local-only
// zeroing done inside encodeScopePrefs is on a copy, not the caller's
// Preferences.
func TestEncodeScopePrefsDoesNotMutateSource(t *testing.T) {
	p := &Preferences{}
	p.CharSize.Datablocks = 7
	p.AudioVolume = 77
	p.DwellMode = DwellMode(2)

	if _, err := encodeScopePrefs(p); err != nil {
		t.Fatalf("encodeScopePrefs: %v", err)
	}

	if p.CharSize.Datablocks != 7 || p.AudioVolume != 77 || p.DwellMode != DwellMode(2) {
		t.Errorf("encodeScopePrefs mutated source: CharSize.Datablocks=%v AudioVolume=%v DwellMode=%v",
			p.CharSize.Datablocks, p.AudioVolume, p.DwellMode)
	}
}

// newSyncTestClient mirrors the helper in other client-using tests:
// build a minimal ControlClient with a pre-seeded TCWDisplay snapshot.
func newSyncTestClient(tcw *sim.TCWDisplayState) *client.ControlClient {
	c := &client.ControlClient{}
	c.State = client.SimState{SimState: server.SimState{}}
	c.State.TCWDisplay = tcw
	return c
}
