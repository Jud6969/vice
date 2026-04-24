// server/shared_tcw_integration_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package server

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"
)

// seedAircraft inserts a minimal aircraft with the given callsign so it
// is "live" for pruning purposes.
func seedAircraft(s *sim.Sim, callsign string) {
	s.Aircraft[av.ADSBCallsign(callsign)] = &sim.Aircraft{
		ADSBCallsign: av.ADSBCallsign(callsign),
	}
}

// TestTwoClientsSeeEachOthersAnnotationChange exercises the
// per-callsign dispatcher round trip end-to-end: A mutates one
// callsign's annotation, B polls and sees it; B mutates a different
// callsign's annotation, A polls and sees it. Rev is monotonic across
// mutations.
func TestTwoClientsSeeEachOthersAnnotationChange(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	tokenB := addReliefHuman(t, sm, tcw)

	sd := &dispatcher{sm: sm}
	const cs1 av.ADSBCallsign = "AAL100"
	const cs2 av.ADSBCallsign = "UAL200"

	// A sets J-ring radius for cs1.
	var upA SimStateUpdate
	if err := sd.SetTrackJRingRadius(
		&SetTrackFloatArgs{ControllerToken: tokenA, Callsign: cs1, Value: 3.5},
		&upA,
	); err != nil {
		t.Fatalf("A SetTrackJRingRadius: %v", err)
	}
	if upA.TCWDisplay == nil {
		t.Fatal("A's echoed TCWDisplay is nil")
	}
	if got := upA.TCWDisplay.Annotations[cs1].JRingRadius; got != 3.5 {
		t.Errorf("A echo JRingRadius[%s]=%v, want 3.5", cs1, got)
	}

	// B polls and sees A's change.
	var upB SimStateUpdate
	if err := sd.GetStateUpdate(tokenB, &upB); err != nil {
		t.Fatalf("B GetStateUpdate: %v", err)
	}
	if upB.TCWDisplay == nil {
		t.Fatal("B.TCWDisplay is nil")
	}
	if got := upB.TCWDisplay.Annotations[cs1].JRingRadius; got != 3.5 {
		t.Errorf("B sees JRingRadius[%s]=%v, want 3.5", cs1, got)
	}

	// B toggles FDB for cs2.
	var upB2 SimStateUpdate
	if err := sd.SetTrackDisplayFDB(
		&SetTrackBoolArgs{ControllerToken: tokenB, Callsign: cs2, Value: true},
		&upB2,
	); err != nil {
		t.Fatalf("B SetTrackDisplayFDB: %v", err)
	}

	// A polls and sees both entries.
	var upA2 SimStateUpdate
	if err := sd.GetStateUpdate(tokenA, &upA2); err != nil {
		t.Fatalf("A GetStateUpdate: %v", err)
	}
	if upA2.TCWDisplay == nil {
		t.Fatal("A.TCWDisplay is nil on poll")
	}
	if !upA2.TCWDisplay.Annotations[cs2].DisplayFDB {
		t.Errorf("A sees DisplayFDB[%s]=false, want true", cs2)
	}
	if got := upA2.TCWDisplay.Annotations[cs1].JRingRadius; got != 3.5 {
		t.Errorf("A lost JRingRadius[%s]=%v, want 3.5 still present", cs1, got)
	}

	// Rev monotonicity across the two mutations.
	if upA2.TCWDisplay.Rev <= upA.TCWDisplay.Rev {
		t.Errorf("Rev did not advance: %d -> %d", upA.TCWDisplay.Rev, upA2.TCWDisplay.Rev)
	}

	// A signs off; the shared state must persist for B.
	if err := sm.SignOff(tokenA); err != nil {
		t.Fatalf("SignOff A: %v", err)
	}
	s := sm.sessionsByToken[tokenB].sim
	d := s.GetTCWDisplay(tcw)
	if d == nil {
		t.Fatal("TCWDisplay was cleared when A signed off while B remains")
	}
	if got := d.Annotations[cs1].JRingRadius; got != 3.5 {
		t.Errorf("after A signoff, JRingRadius[%s]=%v, want 3.5", cs1, got)
	}
	if !d.Annotations[cs2].DisplayFDB {
		t.Errorf("after A signoff, DisplayFDB[%s]=false, want true", cs2)
	}
}

// TestAnnotationsSurviveRejoin covers the "last leaves, new human
// joins" case for per-callsign annotations: the TCWDisplay survives
// the gap and the next signon inherits prior annotations rather than
// reseeding.
func TestAnnotationsSurviveRejoin(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	sd := &dispatcher{sm: sm}
	const callsign av.ADSBCallsign = "SWA500"

	// A sets a unique annotation.
	var up SimStateUpdate
	if err := sd.SetTrackJRingRadius(
		&SetTrackFloatArgs{ControllerToken: tokenA, Callsign: callsign, Value: 4.0},
		&up,
	); err != nil {
		t.Fatalf("A SetTrackJRingRadius: %v", err)
	}

	// Everyone leaves.
	if err := sm.SignOff(tokenA); err != nil {
		t.Fatalf("SignOff: %v", err)
	}

	// New human joins the same TCW.
	tokenC := newHumanAt(t, sm, tcw)
	var upC SimStateUpdate
	if err := sd.GetStateUpdate(tokenC, &upC); err != nil {
		t.Fatalf("GetStateUpdate C: %v", err)
	}
	if upC.TCWDisplay == nil {
		t.Fatal("C.TCWDisplay is nil")
	}
	if got := upC.TCWDisplay.Annotations[callsign].JRingRadius; got != 4.0 {
		t.Errorf("C did not inherit JRingRadius[%s]=4.0; got %v", callsign, got)
	}
}

// TestAnnotationsPrunedWhenAircraftDeparts covers the spec: when an
// aircraft leaves the sim, the next tick prunes its annotation entry
// so subsequent state snapshots no longer include it. We verify via
// direct TCWDisplay inspection rather than GetStateUpdate round-trip
// because the full state update path depends on av.DB, which this
// minimal server-test harness does not populate.
func TestAnnotationsPrunedWhenAircraftDeparts(t *testing.T) {
	sm, tokenA, tcw := newTestManagerWithHuman(t)
	_ = addReliefHuman(t, sm, tcw)

	const liveCS av.ADSBCallsign = "DAL10"
	const ghostCS av.ADSBCallsign = "JBU20"

	s := sm.sessionsByToken[tokenA].sim
	seedAircraft(s, string(liveCS))
	seedAircraft(s, string(ghostCS))

	// Annotations land in the shared per-TCW map. Mutations go through
	// the Sim helpers directly rather than the dispatcher: dispatcher
	// echoes call GetStateUpdate, which depends on av.DB -- not seeded
	// by this minimal server-test harness.
	s.SetTrackJRingRadius(tcw, liveCS, 2)
	s.SetTrackConeLength(tcw, ghostCS, 5)

	// Confirm pre-prune state: both entries present in the shared map.
	d := s.GetTCWDisplay(tcw)
	if _, ok := d.Annotations[liveCS]; !ok {
		t.Fatalf("pre-prune: %s missing", liveCS)
	}
	if _, ok := d.Annotations[ghostCS]; !ok {
		t.Fatalf("pre-prune: %s missing", ghostCS)
	}

	// Ghost aircraft departs; tick prunes.
	delete(s.Aircraft, ghostCS)
	revBefore := d.Rev
	s.PruneTCWDisplayAnnotationsForTest()

	// The shared TCWDisplay both clients poll from dropped the ghost
	// and kept the live entry, with Rev advancing.
	if _, ok := d.Annotations[ghostCS]; ok {
		t.Errorf("post-prune: %s still present", ghostCS)
	}
	if got := d.Annotations[liveCS].JRingRadius; got != 2 {
		t.Errorf("post-prune: JRingRadius[%s]=%v, want 2 retained", liveCS, got)
	}
	if d.Rev <= revBefore {
		t.Errorf("post-prune: Rev=%d, want > %d", d.Rev, revBefore)
	}
}
