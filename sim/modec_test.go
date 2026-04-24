// sim/modec_test.go
// Copyright(c) 2022-2025 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"io"
	"log/slog"
	"testing"
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/nav"
)

// TestUpdateModeCEmptySim exercises the updateModeC entry point on a sim
// with no aircraft to confirm it does not panic.
func TestUpdateModeCEmptySim(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)
	s.updateModeC() // must not panic
}

// makeModeCAircraft builds a minimal aircraft that will report a Mode-C
// altitude of the given value via GetRadarTrack.
func makeModeCAircraft(cs av.ADSBCallsign, altitude float32) *Aircraft {
	return &Aircraft{
		ADSBCallsign: cs,
		Mode:         av.TransponderModeAltitude,
		Nav: nav.Nav{
			FlightState: nav.FlightState{
				Altitude:          altitude,
				NmPerLongitude:    52,
				MagneticVariation: 0,
			},
		},
	}
}

// TestUpdateModeCFlagsUnreasonableRate verifies that an altitude delta
// implying > FPMThreshold fpm across the sim tick flips the server-side
// UnreasonableModeC flag on.
func TestUpdateModeCFlagsUnreasonableRate(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)

	// Current altitude: 10000; previous: 9000 one second ago. That's
	// 1000 ft / (1/60) min = 60,000 fpm >> FPMThreshold (8400).
	ac := makeModeCAircraft("TST001", 10000)
	ac.PreviousTransponderAlt = 9000
	ac.PreviousTransponderTime = s.State.SimTime.Add(-1 * time.Second)
	s.Aircraft[ac.ADSBCallsign] = ac

	s.updateModeC()

	if !ac.UnreasonableModeC {
		t.Fatalf("expected UnreasonableModeC=true after 60000 fpm jump, got false")
	}
	if ac.ConsecutiveNormalTracks != 0 {
		t.Errorf("expected ConsecutiveNormalTracks=0 after flag set, got %d",
			ac.ConsecutiveNormalTracks)
	}
}

// TestUpdateModeCClearsAfterFiveNormalTicks verifies that a previously
// flagged aircraft clears UnreasonableModeC only after five consecutive
// normal readings, matching the legacy stars-side behavior.
func TestUpdateModeCClearsAfterFiveNormalTicks(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)

	// Start flagged. Previous reading one second ago at 10000;
	// current reading (returned by GetRadarTrack) will also be 10000,
	// so rate = 0 fpm — well under threshold.
	ac := makeModeCAircraft("TST002", 10000)
	ac.UnreasonableModeC = true
	ac.ConsecutiveNormalTracks = 0
	ac.PreviousTransponderAlt = 10000
	ac.PreviousTransponderTime = s.State.SimTime.Add(-1 * time.Second)
	s.Aircraft[ac.ADSBCallsign] = ac

	// First four normal ticks: counter increments, flag stays on.
	for i := 1; i <= 4; i++ {
		// Advance sim time so deltaMinutes != 0 on every tick.
		s.State.SimTime = s.State.SimTime.Add(1 * time.Second)
		s.updateModeC()
		if !ac.UnreasonableModeC {
			t.Fatalf("tick %d: expected UnreasonableModeC=true, got false", i)
		}
		if ac.ConsecutiveNormalTracks != i {
			t.Fatalf("tick %d: expected ConsecutiveNormalTracks=%d, got %d",
				i, i, ac.ConsecutiveNormalTracks)
		}
	}

	// Fifth normal tick: flag clears, counter resets.
	s.State.SimTime = s.State.SimTime.Add(1 * time.Second)
	s.updateModeC()
	if ac.UnreasonableModeC {
		t.Errorf("tick 5: expected UnreasonableModeC=false after clear, got true")
	}
	if ac.ConsecutiveNormalTracks != 0 {
		t.Errorf("tick 5: expected ConsecutiveNormalTracks=0 after clear, got %d",
			ac.ConsecutiveNormalTracks)
	}
}

// TestUpdateModeCResetsWhenModeCUnavailable verifies that the flag and
// counter are both cleared when Mode-C is unavailable on the current
// reading (transponder not in altitude mode or reporting zero).
func TestUpdateModeCResetsWhenModeCUnavailable(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)

	ac := makeModeCAircraft("TST003", 10000)
	ac.Mode = av.TransponderModeStandby // Mode-C unavailable
	ac.UnreasonableModeC = true
	ac.ConsecutiveNormalTracks = 3
	ac.PreviousTransponderAlt = 9000
	ac.PreviousTransponderTime = s.State.SimTime.Add(-1 * time.Second)
	s.Aircraft[ac.ADSBCallsign] = ac

	s.updateModeC()

	if ac.UnreasonableModeC {
		t.Errorf("expected UnreasonableModeC=false when Mode-C unavailable, got true")
	}
	if ac.ConsecutiveNormalTracks != 0 {
		t.Errorf("expected ConsecutiveNormalTracks=0 when Mode-C unavailable, got %d",
			ac.ConsecutiveNormalTracks)
	}
}
