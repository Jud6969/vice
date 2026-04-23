// sim/tcw_display_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/math"
)

func TestSetTrackJRingRadiusCreatesEntry(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")
	acid := ACID("AAL123")

	if d := s.GetTCWDisplay(tcw); d != nil {
		t.Fatalf("TCWDisplay pre-mutation = %+v, want nil", d)
	}

	s.SetTrackJRingRadius(tcw, acid, 3.5)

	d := s.GetTCWDisplay(tcw)
	if d == nil {
		t.Fatalf("TCWDisplay nil after mutation")
	}
	if got := d.Annotations[acid].JRingRadius; got != 3.5 {
		t.Errorf("JRingRadius = %v, want 3.5", got)
	}
	if d.Rev != 1 {
		t.Errorf("Rev = %d, want 1", d.Rev)
	}
}

func TestSetTrackBumpsRevOnEachMutation(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")
	acid := ACID("AAL123")

	s.SetTrackJRingRadius(tcw, acid, 3)
	s.SetTrackJRingRadius(tcw, acid, 5)
	s.SetTrackConeLength(tcw, acid, 10)

	d := s.GetTCWDisplay(tcw)
	if d.Rev != 3 {
		t.Errorf("Rev = %d, want 3", d.Rev)
	}
	if got := d.Annotations[acid].JRingRadius; got != 5 {
		t.Errorf("JRingRadius = %v, want 5 (updated in place)", got)
	}
	if got := d.Annotations[acid].ConeLength; got != 10 {
		t.Errorf("ConeLength = %v, want 10", got)
	}
}

func TestSetTrackIsolatesACIDs(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")

	s.SetTrackJRingRadius(tcw, "AAL123", 3)
	s.SetTrackJRingRadius(tcw, "UAL456", 5)

	d := s.GetTCWDisplay(tcw)
	if got := d.Annotations["AAL123"].JRingRadius; got != 3 {
		t.Errorf("AAL123 JRingRadius = %v, want 3", got)
	}
	if got := d.Annotations["UAL456"].JRingRadius; got != 5 {
		t.Errorf("UAL456 JRingRadius = %v, want 5", got)
	}
	if len(d.Annotations) != 2 {
		t.Errorf("len(Annotations) = %d, want 2", len(d.Annotations))
	}
}

func TestSetTrackIsolatesTCWs(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	acid := ACID("AAL123")

	s.SetTrackJRingRadius("N90", acid, 3)
	s.SetTrackJRingRadius("N01", acid, 5)

	if got := s.GetTCWDisplay("N90").Annotations[acid].JRingRadius; got != 3 {
		t.Errorf("N90 JRingRadius = %v, want 3", got)
	}
	if got := s.GetTCWDisplay("N01").Annotations[acid].JRingRadius; got != 5 {
		t.Errorf("N01 JRingRadius = %v, want 5", got)
	}
}

func TestSetTrackAllFields(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")
	acid := ACID("AAL123")

	north := math.CardinalOrdinalDirection(math.North)
	tru := true
	fal := false

	s.SetTrackJRingRadius(tcw, acid, 3.5)
	s.SetTrackConeLength(tcw, acid, 7)
	s.SetTrackLeaderLineDirection(tcw, acid, &north)
	s.SetTrackFDAMLeaderLineDirection(tcw, acid, &north)
	s.SetTrackUseGlobalLeaderLine(tcw, acid, true)
	s.SetTrackDisplayFDB(tcw, acid, true)
	s.SetTrackDisplayPTL(tcw, acid, true)
	s.SetTrackDisplayTPASize(tcw, acid, &tru)
	s.SetTrackDisplayATPAMonitor(tcw, acid, &tru)
	s.SetTrackDisplayATPAWarnAlert(tcw, acid, &fal)
	s.SetTrackDisplayRequestedAltitude(tcw, acid, &tru)
	s.SetTrackDisplayLDBBeaconCode(tcw, acid, true)

	a := s.GetTCWDisplay(tcw).Annotations[acid]
	if a.JRingRadius != 3.5 || a.ConeLength != 7 {
		t.Errorf("scalar fields: got %+v", a)
	}
	if a.LeaderLineDirection == nil || *a.LeaderLineDirection != math.North {
		t.Errorf("LeaderLineDirection = %v, want North", a.LeaderLineDirection)
	}
	if a.FDAMLeaderLineDirection == nil || *a.FDAMLeaderLineDirection != math.North {
		t.Errorf("FDAMLeaderLineDirection = %v, want North", a.FDAMLeaderLineDirection)
	}
	if !a.UseGlobalLeaderLine || !a.DisplayFDB || !a.DisplayPTL || !a.DisplayLDBBeaconCode {
		t.Errorf("bool flags not all set: %+v", a)
	}
	if a.DisplayTPASize == nil || !*a.DisplayTPASize {
		t.Errorf("DisplayTPASize = %v, want true", a.DisplayTPASize)
	}
	if a.DisplayATPAMonitor == nil || !*a.DisplayATPAMonitor {
		t.Errorf("DisplayATPAMonitor = %v, want true", a.DisplayATPAMonitor)
	}
	if a.DisplayATPAWarnAlert == nil || *a.DisplayATPAWarnAlert {
		t.Errorf("DisplayATPAWarnAlert = %v, want false", a.DisplayATPAWarnAlert)
	}
	if a.DisplayRequestedAltitude == nil || !*a.DisplayRequestedAltitude {
		t.Errorf("DisplayRequestedAltitude = %v, want true", a.DisplayRequestedAltitude)
	}
}

func TestPruneTCWDisplayAnnotationsRemovesDepartedACIDs(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")

	s.SetTrackJRingRadius(tcw, "LIVE", 3)
	s.SetTrackJRingRadius(tcw, "GHOST", 5)

	// Mark LIVE as present by adding an aircraft with a NASFlightPlan.
	s.Aircraft["LIVE_CS"] = &Aircraft{
		ADSBCallsign:  av.ADSBCallsign("LIVE_CS"),
		NASFlightPlan: &NASFlightPlan{ACID: "LIVE"},
	}

	revBefore := s.GetTCWDisplay(tcw).Rev
	s.pruneTCWDisplayAnnotations()
	d := s.GetTCWDisplay(tcw)

	if _, ok := d.Annotations["LIVE"]; !ok {
		t.Errorf("LIVE pruned, want retained")
	}
	if _, ok := d.Annotations["GHOST"]; ok {
		t.Errorf("GHOST retained, want pruned")
	}
	if d.Rev <= revBefore {
		t.Errorf("Rev = %d, want > %d after pruning", d.Rev, revBefore)
	}
}

func TestUpdateStatePrunesDepartedAnnotations(t *testing.T) {
	// Verify the hook is wired: updateState's tail-end call to
	// pruneTCWDisplayAnnotations should clear annotations whose ACIDs
	// have no corresponding aircraft.
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("TEST")

	s.SetTrackJRingRadius(tcw, "GHOST", 3)
	// No aircraft in s.Aircraft, so pruning should remove GHOST.

	s.updateState()

	d := s.GetTCWDisplay(tcw)
	if _, ok := d.Annotations["GHOST"]; ok {
		t.Errorf("GHOST retained after updateState(); want pruned via the tick-loop hook")
	}
}

func TestPruneTCWDisplayAnnotationsNoopWhenAllLive(t *testing.T) {
	s := NewTestSim(log.New(true, "error", t.TempDir()))
	tcw := TCW("N90")

	s.SetTrackJRingRadius(tcw, "LIVE1", 3)
	s.SetTrackJRingRadius(tcw, "LIVE2", 5)
	s.Aircraft["CS1"] = &Aircraft{NASFlightPlan: &NASFlightPlan{ACID: "LIVE1"}}
	s.Aircraft["CS2"] = &Aircraft{NASFlightPlan: &NASFlightPlan{ACID: "LIVE2"}}

	revBefore := s.GetTCWDisplay(tcw).Rev
	s.pruneTCWDisplayAnnotations()
	d := s.GetTCWDisplay(tcw)

	if len(d.Annotations) != 2 {
		t.Errorf("len(Annotations) = %d, want 2", len(d.Annotations))
	}
	if d.Rev != revBefore {
		t.Errorf("Rev changed: %d -> %d, want unchanged", revBefore, d.Rev)
	}
}
