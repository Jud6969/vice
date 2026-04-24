// sim/atpa_test.go
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
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/nav"
	vrand "github.com/mmp/vice/rand"
	"github.com/mmp/vice/wx"
)

// TestUpdateATPAEmptySim exercises the updateATPA entry point on a sim with
// no aircraft and no adapted ATPA volumes to confirm it does not panic.
func TestUpdateATPAEmptySim(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)
	s.updateATPA() // must not panic
}

// TestUpdateATPADisabledSkipsWalk asserts that when ATPA is disabled
// system-wide the derived state is zeroed and the walk exits early.
func TestUpdateATPADisabledSkipsWalk(t *testing.T) {
	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s := NewTestSim(lg)
	s.State.ATPAEnabled = false

	ac := MakeTestAircraft("UAL1", "31L")
	ac.ATPADerived = ATPADerived{
		IntrailDistance:  4.2,
		DrawATPAGraphics: true,
		ATPAStatus:       ATPAStatusAlert,
	}
	s.Aircraft[ac.ADSBCallsign] = ac

	s.updateATPA()

	if ac.ATPADerived != (ATPADerived{}) {
		t.Fatalf("expected ATPADerived to be zeroed when ATPA disabled, got %+v", ac.ATPADerived)
	}
}

// TestUpdateATPAComputesPair stands up a sim with two arrivals on a short
// ATPA volume and asserts the trailing aircraft gets IntrailDistance,
// DrawATPAGraphics, and ATPALeadAircraftCallsign populated.
func TestUpdateATPAComputesPair(t *testing.T) {
	// The walk consults av.DB.AircraftPerformance for landing speed; make
	// sure the DB exists and has the test aircraft type registered.
	if av.DB == nil {
		av.DB = &av.StaticDatabase{
			Airports:            map[string]av.FAAAirport{},
			AircraftPerformance: map[string]av.AircraftPerformance{},
		}
	}
	if av.DB.Airports == nil {
		av.DB.Airports = map[string]av.FAAAirport{}
	}
	if av.DB.AircraftPerformance == nil {
		av.DB.AircraftPerformance = map[string]av.AircraftPerformance{}
	}
	_, hadPerf := av.DB.AircraftPerformance["B738"]
	perf := av.AircraftPerformance{ICAO: "B738"}
	perf.Speed.Landing = 140
	av.DB.AircraftPerformance["B738"] = perf
	t.Cleanup(func() {
		if !hadPerf {
			delete(av.DB.AircraftPerformance, "B738")
		}
	})

	lg := &log.Logger{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}

	// Place the runway threshold at (0, 0). Runway heading 360 (north).
	threshold := math.Point2LL{0, 0}
	vol := &av.ATPAVolume{
		Id:                  "VOL1",
		Threshold:           threshold,
		Heading:             360,
		MaxHeadingDeviation: 90,
		Floor:               0,
		Ceiling:             10000,
		Length:              20, // 20 nm long
		LeftWidth:           10000,
		RightWidth:          10000,
	}

	airport := &av.Airport{
		Location: threshold,
		ATPAVolumes: map[string]*av.ATPAVolume{
			"VOL1": vol,
		},
	}

	tcw := TCW("TEST")
	freq := ControlPosition("125.0")
	s := &Sim{
		lg:   lg,
		Rand: vrand.Make(),
		State: &CommonState{
			DynamicState: DynamicState{
				METAR:                map[string]wx.METAR{},
				SimTime:              NewSimTime(time.Now()),
				CurrentConsolidation: map[TCW]*TCPConsolidation{tcw: {PrimaryTCP: TCP(freq)}},
				ATPAEnabled:          true,
				ATPAVolumeState: map[string]map[string]*ATPAVolumeState{
					"KJFK": {"VOL1": {}},
				},
			},
			Airports:          map[string]*av.Airport{"KJFK": airport},
			NmPerLongitude:    52,
			MagneticVariation: 0,
		},
		Aircraft:        map[av.ADSBCallsign]*Aircraft{},
		PendingContacts: make(map[TCP][]PendingContact),
		PrivilegedTCWs:  map[TCW]bool{tcw: true},
		STARSComputer:   &STARSComputer{},
	}

	// Lead aircraft: 5 nm south of the threshold, heading north (landing).
	// Trail aircraft: 8 nm south.
	makeAC := func(cs string, northOffsetNM float32) *Aircraft {
		return &Aircraft{
			ADSBCallsign:        av.ADSBCallsign(cs),
			TypeOfFlight:        av.FlightTypeArrival,
			Mode:                av.TransponderModeAltitude,
			ControllerFrequency: freq,
			FlightPlan: av.FlightPlan{
				ArrivalAirport: "KJFK",
				AircraftType:   "B738",
			},
			NASFlightPlan: &NASFlightPlan{
				ACID:         ACID(cs),
				CWTCategory:  "F",
				AircraftType: "B738",
			},
			Nav: nav.Nav{
				FlightState: nav.FlightState{
					// Longitude 0, latitude shifted south by northOffsetNM/60 deg.
					Position:          math.Point2LL{0, -northOffsetNM / 60},
					Heading:           360, // pointing north
					Altitude:          3000,
					GS:                140,
					NmPerLongitude:    52,
					MagneticVariation: 0,
				},
				Approach: nav.NavApproach{
					ATPAVolume: vol,
					Assigned: &av.Approach{
						Id:     "I31L",
						Type:   av.ILSApproach,
						Runway: "31L",
					},
				},
			},
		}
	}

	lead := makeAC("LEAD01", 5)
	trail := makeAC("TRL001", 8)
	s.Aircraft[lead.ADSBCallsign] = lead
	s.Aircraft[trail.ADSBCallsign] = trail

	s.updateATPA()

	// The lead aircraft has nobody in front -> nothing set.
	if lead.ATPADerived.DrawATPAGraphics {
		t.Errorf("lead: unexpected DrawATPAGraphics=true; %+v", lead.ATPADerived)
	}
	if lead.ATPADerived.ATPALeadAircraftCallsign != "" {
		t.Errorf("lead: unexpected ATPALeadAircraftCallsign=%q", lead.ATPADerived.ATPALeadAircraftCallsign)
	}

	// The trailing aircraft should get the pair populated.
	if !trail.ATPADerived.DrawATPAGraphics {
		t.Errorf("trail: expected DrawATPAGraphics=true; %+v", trail.ATPADerived)
	}
	if trail.ATPADerived.ATPALeadAircraftCallsign != lead.ADSBCallsign {
		t.Errorf("trail: expected lead=%q, got %q",
			lead.ADSBCallsign, trail.ATPADerived.ATPALeadAircraftCallsign)
	}
	if trail.ATPADerived.IntrailDistance <= 0 {
		t.Errorf("trail: expected IntrailDistance>0, got %v", trail.ATPADerived.IntrailDistance)
	}
	if trail.ATPADerived.MinimumMIT <= 0 {
		t.Errorf("trail: expected MinimumMIT>0 after CWT check, got %v", trail.ATPADerived.MinimumMIT)
	}
	// Baseline ATPAStatus should be Monitor or higher once CWT check ran.
	if trail.ATPADerived.ATPAStatus == ATPAStatusUnset {
		t.Errorf("trail: expected ATPAStatus != Unset, got Unset")
	}
}
