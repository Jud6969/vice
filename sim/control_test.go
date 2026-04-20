// sim/control_test.go
// Copyright (c) 2025 Matthew Murphy. All rights reserved.

package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/nav"
	"github.com/mmp/vice/rand"
)

func TestParseHold(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		wantFix       string
		wantHold      *av.Hold
		wantErr       bool
		errContains   string
		checkTurn     bool
		wantTurnDir   av.TurnDirection
		checkLeg      bool
		wantLegLength float32
		wantLegTime   float32
		checkRadial   bool
		wantRadial    math.MagneticHeading
	}{
		{
			name:     "Published hold - no options",
			command:  "JIMEE",
			wantFix:  "JIMEE",
			wantHold: nil,
			wantErr:  false,
		},
		{
			name:        "Controller hold - left turns with radial",
			command:     "JIMEE/L/R090",
			wantFix:     "JIMEE",
			wantErr:     false,
			checkTurn:   true,
			wantTurnDir: av.TurnLeft,
			checkRadial: true,
			wantRadial:  90,
			checkLeg:    true,
			wantLegTime: 1.0,
		},
		{
			name:        "Controller hold - right turns with radial",
			command:     "JIMEE/R/R270",
			wantFix:     "JIMEE",
			wantErr:     false,
			checkTurn:   true,
			wantTurnDir: av.TurnRight,
			checkRadial: true,
			wantRadial:  270,
			checkLeg:    true,
			wantLegTime: 1.0,
		},
		{
			name:          "Controller hold - distance legs",
			command:       "JIMEE/5NM/R180",
			wantFix:       "JIMEE",
			wantErr:       false,
			checkLeg:      true,
			wantLegLength: 5.0,
			wantLegTime:   0,
			checkRadial:   true,
			wantRadial:    180,
		},
		{
			name:          "Controller hold - time legs",
			command:       "JIMEE/2M/R045",
			wantFix:       "JIMEE",
			wantErr:       false,
			checkLeg:      true,
			wantLegTime:   2.0,
			wantLegLength: 0,
			checkRadial:   true,
			wantRadial:    45,
		},
		{
			name:          "Controller hold - all options",
			command:       "JIMEE/L/5NM/R090",
			wantFix:       "JIMEE",
			wantErr:       false,
			checkTurn:     true,
			wantTurnDir:   av.TurnLeft,
			checkLeg:      true,
			wantLegLength: 5.0,
			wantLegTime:   0,
			checkRadial:   true,
			wantRadial:    90,
		},
		{
			name:        "Controller hold - variable digit radial (2 digits)",
			command:     "JIMEE/R90",
			wantFix:     "JIMEE",
			wantErr:     false,
			checkRadial: true,
			wantRadial:  90,
		},
		{
			name:        "Controller hold - variable digit radial (1 digit)",
			command:     "JIMEE/R5",
			wantFix:     "JIMEE",
			wantErr:     false,
			checkRadial: true,
			wantRadial:  5,
		},
		{
			name:        "Controller hold - lowercase options normalized",
			command:     "jimee/l/5nm/r090",
			wantFix:     "JIMEE",
			wantErr:     false,
			checkTurn:   true,
			wantTurnDir: av.TurnLeft,
		},
		{
			name:        "Error - conflicting turn directions",
			command:     "JIMEE/L/R/R090",
			wantErr:     true,
			errContains: "conflicting hold options: both left and right turns",
		},
		{
			name:        "Error - conflicting leg types",
			command:     "JIMEE/2M/5NM/R090",
			wantErr:     true,
			errContains: "conflicting hold options: both distance and time legs",
		},
		{
			name:        "Error - duplicate left turns",
			command:     "JIMEE/L/L/R090",
			wantErr:     true,
			errContains: "duplicate hold option: left turns",
		},
		{
			name:        "Error - duplicate right turns",
			command:     "JIMEE/R/R/R090",
			wantErr:     true,
			errContains: "duplicate hold option: right turns",
		},
		{
			name:        "Error - duplicate distance legs",
			command:     "JIMEE/5NM/3NM/R090",
			wantErr:     true,
			errContains: "duplicate hold option: distance legs",
		},
		{
			name:        "Error - duplicate time legs",
			command:     "JIMEE/2M/3M/R090",
			wantErr:     true,
			errContains: "duplicate hold option: time legs",
		},
		{
			name:        "Error - duplicate radials",
			command:     "JIMEE/R090/R180",
			wantErr:     true,
			errContains: "duplicate hold option: radial",
		},
		{
			name:        "Error - missing radial for controller hold",
			command:     "JIMEE/L",
			wantErr:     true,
			errContains: "radial (Rxxx) is required",
		},
		{
			name:        "Error - invalid distance",
			command:     "JIMEE/XNM/R090",
			wantErr:     true,
			errContains: "invalid distance",
		},
		{
			name:        "Error - negative distance",
			command:     "JIMEE/-5NM/R090",
			wantErr:     true,
			errContains: "invalid distance",
		},
		{
			name:        "Error - zero distance",
			command:     "JIMEE/0NM/R090",
			wantErr:     true,
			errContains: "invalid distance",
		},
		{
			name:        "Error - invalid time",
			command:     "JIMEE/XM/R090",
			wantErr:     true,
			errContains: "invalid time",
		},
		{
			name:        "Error - negative time",
			command:     "JIMEE/-2M/R090",
			wantErr:     true,
			errContains: "invalid time",
		},
		{
			name:        "Error - zero time",
			command:     "JIMEE/0M/R090",
			wantErr:     true,
			errContains: "invalid time",
		},
		{
			name:        "Error - invalid radial format",
			command:     "JIMEE/RX",
			wantErr:     true,
			errContains: "invalid radial",
		},
		{
			name:        "Error - radial too large",
			command:     "JIMEE/R361",
			wantErr:     true,
			errContains: "invalid radial",
		},
		{
			name:        "Error - negative radial",
			command:     "JIMEE/R-90",
			wantErr:     true,
			errContains: "invalid radial",
		},
		{
			name:        "Error - invalid option",
			command:     "JIMEE/INVALID/R090",
			wantErr:     true,
			errContains: "invalid hold option",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotFix, gotHold, ok := parseHold(tt.command)

			if tt.wantErr {
				if ok {
					t.Errorf("parseHold() expected error, got success")
					return
				}
				return
			}

			if !ok {
				t.Errorf("parseHold() unexpected failure")
				return
			}

			if gotFix != tt.wantFix {
				t.Errorf("parseHold() fix = %v, want %v", gotFix, tt.wantFix)
			}

			// If no checks are specified, we expect a published hold (nil)
			expectPublishedHold := !tt.checkTurn && !tt.checkLeg && !tt.checkRadial

			if expectPublishedHold {
				if gotHold != nil {
					t.Errorf("parseHold() hold = %v, want nil", gotHold)
				}
				return
			}

			if gotHold == nil {
				t.Errorf("parseHold() hold = nil, want non-nil")
				return
			}

			if gotHold.Fix != tt.wantFix {
				t.Errorf("parseHold() hold.Fix = %v, want %v", gotHold.Fix, tt.wantFix)
			}

			if tt.checkTurn && gotHold.TurnDirection != tt.wantTurnDir {
				t.Errorf("parseHold() hold.TurnDirection = %v, want %v", gotHold.TurnDirection, tt.wantTurnDir)
			}

			if tt.checkLeg {
				if gotHold.LegLengthNM != tt.wantLegLength {
					t.Errorf("parseHold() hold.LegLengthNM = %v, want %v", gotHold.LegLengthNM, tt.wantLegLength)
				}
				if gotHold.LegMinutes != tt.wantLegTime {
					t.Errorf("parseHold() hold.LegMinutes = %v, want %v", gotHold.LegMinutes, tt.wantLegTime)
				}
			}

			if tt.checkRadial && gotHold.InboundCourse != tt.wantRadial {
				t.Errorf("parseHold() hold.InboundCourse = %v, want %v", gotHold.InboundCourse, tt.wantRadial)
			}
		})
	}
}

func TestRunOneControlCommandAtFixClearedStraightInApproach(t *testing.T) {
	lg := log.New(true, "error", t.TempDir())

	appr := &av.Approach{
		FullName: "RNAV Runway 24",
		Waypoints: []av.WaypointArray{
			{
				{Fix: "MATTY"},
			},
		},
	}

	callsign := av.ADSBCallsign("TEST123")
	s := &Sim{
		State: &CommonState{
			DynamicState: DynamicState{
				CurrentConsolidation: map[TCW]*TCPConsolidation{
					"TCW1": {PrimaryTCP: "1A"},
				},
			},
		},
		Aircraft: map[av.ADSBCallsign]*Aircraft{
			callsign: {
				ADSBCallsign:        callsign,
				ControllerFrequency: "1A",
				Nav: nav.Nav{
					Waypoints: []av.Waypoint{
						{Fix: "MATTY"},
					},
					Approach: nav.NavApproach{
						Assigned:   appr,
						AssignedId: "RG24",
					},
				},
			},
		},
		PendingContacts: map[TCP][]PendingContact{},
		lg:              lg,
	}

	intent, err := s.runOneControlCommand("TCW1", callsign, "AMATTY/CSIRG24", 0, false)
	if err != nil {
		t.Fatalf("runOneControlCommand() returned error: %v", err)
	}

	approachIntent, ok := intent.(av.ApproachIntent)
	if !ok {
		t.Fatalf("runOneControlCommand() returned %T, want av.ApproachIntent", intent)
	}
	if approachIntent.Type != av.ApproachAtFixCleared {
		t.Fatalf("runOneControlCommand() intent type = %v, want %v", approachIntent.Type, av.ApproachAtFixCleared)
	}
	if !approachIntent.StraightIn {
		t.Fatal("runOneControlCommand() did not preserve straight-in clearance")
	}
	if approachIntent.Fix != "MATTY" {
		t.Fatalf("runOneControlCommand() fix = %q, want %q", approachIntent.Fix, "MATTY")
	}
	if s.Aircraft[callsign].Nav.Approach.AtFixClearedRoute == nil {
		t.Fatal("AtFixClearedRoute was not populated")
	}
}

func TestTowersForAirport(t *testing.T) {
	s := &Sim{
		State: &CommonState{
			Controllers: map[TCP]*av.Controller{
				"IAD_N_TWR": {Callsign: "IAD_N_TWR", Frequency: 120100},
				"IAD_E_TWR": {Callsign: "IAD_E_TWR", Frequency: 120750},
				"IAD_W_TWR": {Callsign: "IAD_W_TWR", Frequency: 119850},
				"DCA_TWR":   {Callsign: "DCA_TWR", Frequency: 119100},
				"MCO_APP":   {Callsign: "MCO_APP", Frequency: 127750},
			},
		},
	}
	got := s.towersForAirport("IAD")
	if len(got) != 3 {
		t.Fatalf("IAD: got %d towers, want 3", len(got))
	}
	got = s.towersForAirport("DCA")
	if len(got) != 1 || got[0].Callsign != "DCA_TWR" {
		t.Errorf("DCA: got %+v, want 1 DCA_TWR", got)
	}
	got = s.towersForAirport("MCO")
	if len(got) != 0 {
		t.Errorf("MCO: got %d, want 0", len(got))
	}
}

func TestResolveControllerByFrequency_ZeroMatches(t *testing.T) {
	s := &Sim{State: &CommonState{Controllers: map[TCP]*av.Controller{
		"A": {Callsign: "A", Frequency: 127750},
	}}}
	ac := &Aircraft{}
	ctrl, err := s.resolveControllerByFrequency(ac, 135000, "")
	if ctrl != nil || err == nil {
		t.Errorf("want (nil, err), got (%v, %v)", ctrl, err)
	}
}

func TestResolveControllerByFrequency_UniqueMatch(t *testing.T) {
	target := &av.Controller{Callsign: "A", Frequency: 127750}
	s := &Sim{State: &CommonState{Controllers: map[TCP]*av.Controller{"A": target}}}
	ac := &Aircraft{}
	ctrl, err := s.resolveControllerByFrequency(ac, 127750, "")
	if err != nil || ctrl != target {
		t.Errorf("want target, got (%v, %v)", ctrl, err)
	}
}

func TestResolveControllerByFrequency_NameHintWins(t *testing.T) {
	a := &av.Controller{Callsign: "X", RadioName: "Orlando Approach", Frequency: 127750, Facility: "MCO"}
	b := &av.Controller{Callsign: "Y", RadioName: "Tampa Approach", Frequency: 127750, Facility: "TPA"}
	s := &Sim{State: &CommonState{Controllers: map[TCP]*av.Controller{"X": a, "Y": b}}}
	ac := &Aircraft{}
	ctrl, err := s.resolveControllerByFrequency(ac, 127750, "orlando")
	if err != nil || ctrl != a {
		t.Errorf("want Orlando, got (%v, %v)", ctrl, err)
	}
}

func TestResolveControllerByFrequency_MultiTokenHint(t *testing.T) {
	// Two controllers on the same frequency; only one has RadioName
	// "Los Angeles Center". An underscore-joined hint from the STT grammar
	// should be tokenized and match every token against either field.
	a := &av.Controller{Callsign: "LAX_CTR", RadioName: "Los Angeles Center", Frequency: 132400, Facility: "ZLA"}
	b := &av.Controller{Callsign: "OAK_CTR", RadioName: "Oakland Center", Frequency: 132400, Facility: "ZOA"}
	s := &Sim{State: &CommonState{Controllers: map[TCP]*av.Controller{"LAX_CTR": a, "OAK_CTR": b}}}
	ac := &Aircraft{}
	ctrl, err := s.resolveControllerByFrequency(ac, 132400, "los_angeles")
	if err != nil || ctrl != a {
		t.Errorf("want Los Angeles Center, got (%v, %v)", ctrl, err)
	}
}

func TestResolveControllerByFrequency_OutOfBandError(t *testing.T) {
	s := &Sim{State: &CommonState{Controllers: map[TCP]*av.Controller{}}}
	ac := &Aircraft{}
	_, err := s.resolveControllerByFrequency(ac, 99000, "")
	if err == nil {
		t.Errorf("want err for out-of-band freq, got nil")
	}
}

// makeFreqChangeSim builds a minimal Sim for FrequencyChange tests.
// fromCtrl and target are placed on the same facility so that same-facility
// logic can fire when RealisticFrequencyManagement is true.
func makeFreqChangeSim(t *testing.T, realistic bool) (*Sim, av.ADSBCallsign) {
	t.Helper()
	lg := log.New(true, "error", t.TempDir())

	const (
		callsign  av.ADSBCallsign = "UAL123"
		fromTCP   TCP             = "NYC_APP"
		targetTCP TCP             = "NYC_CTR"
		facility                  = "ZNY"
		targetFreq av.Frequency  = 127750
	)

	fromCtrl := &av.Controller{Callsign: string(fromTCP), Frequency: 121500, Facility: facility}
	targetCtrl := &av.Controller{Callsign: string(targetTCP), Frequency: targetFreq, Facility: facility}

	const tcw TCW = "TCW1"
	s := &Sim{
		State: &CommonState{
			RealisticFrequencyManagement: realistic,
			Controllers: map[TCP]*av.Controller{
				fromTCP:   fromCtrl,
				targetTCP: targetCtrl,
			},
			DynamicState: DynamicState{
				CurrentConsolidation: map[TCW]*TCPConsolidation{
					tcw: {PrimaryTCP: fromTCP},
				},
			},
		},
		Aircraft: map[av.ADSBCallsign]*Aircraft{
			callsign: {
				ADSBCallsign:        callsign,
				ControllerFrequency: ControlPosition(fromTCP),
			},
		},
		PrivilegedTCWs:  map[TCW]bool{tcw: true},
		PendingContacts: map[TCP][]PendingContact{},
		lg:              lg,
	}
	return s, callsign
}

// makeBareFCSim builds a minimal Sim for bare-FC and unknown-freq FC tests.
// It includes a NASFlightPlan so that ContactTrackingController can succeed.
// trackingTCP is used as the aircraft's TrackingController so contactController
// can find it in State.Controllers.
func makeBareFCSim(t *testing.T, realistic bool) (*Sim, av.ADSBCallsign) {
	t.Helper()
	lg := log.New(true, "error", t.TempDir())

	const (
		callsign    av.ADSBCallsign = "UAL456"
		fromTCP     TCP             = "NYC_APP"
		trackingTCP TCP             = "NYC_CTR" // distinct so ContactTrackingController doesn't see "already on freq"
		facility                   = "ZNY"
	)

	fromCtrl := &av.Controller{Callsign: string(fromTCP), Frequency: 121500, Facility: facility}
	trackingCtrl := &av.Controller{Callsign: string(trackingTCP), Frequency: 132100, Facility: facility}

	fp := &NASFlightPlan{
		ACID:               ACID(callsign),
		CID:                "1",
		TrackingController: ControlPosition(trackingTCP),
		OwningTCW:          "TCW1",
	}
	ac := &Aircraft{
		ADSBCallsign:        callsign,
		ControllerFrequency: ControlPosition(fromTCP),
		NASFlightPlan:       fp,
	}

	const tcw TCW = "TCW1"
	s := &Sim{
		State: &CommonState{
			RealisticFrequencyManagement: realistic,
			Controllers: map[TCP]*av.Controller{
				fromTCP:     fromCtrl,
				trackingTCP: trackingCtrl,
			},
			DynamicState: DynamicState{
				CurrentConsolidation: map[TCW]*TCPConsolidation{
					tcw: {PrimaryTCP: fromTCP},
				},
			},
		},
		Aircraft: map[av.ADSBCallsign]*Aircraft{
			callsign: ac,
		},
		STARSComputer:   &STARSComputer{},
		PrivilegedTCWs:  map[TCW]bool{tcw: true},
		PendingContacts: map[TCP][]PendingContact{},
		Rand:            rand.Make(),
		lg:              lg,
	}
	return s, callsign
}

// TestFC_Bare_Conventional_FallsBackToTrackingController verifies that a bare
// "FC" command in Conventional mode on a non-cleared aircraft falls through to
// ContactTrackingController (no error, ContactIntent returned).
func TestFC_Bare_Conventional_FallsBackToTrackingController(t *testing.T) {
	s, callsign := makeBareFCSim(t, false /* conventional */)

	intent, err := s.runOneControlCommand("TCW1", callsign, "FC", 0, false)
	if err != nil {
		t.Fatalf("Conventional bare FC: got error %v, want nil", err)
	}
	if intent == nil {
		t.Fatal("Conventional bare FC: got nil intent, want non-nil ContactIntent")
	}
	if _, ok := intent.(av.ContactIntent); !ok {
		t.Fatalf("Conventional bare FC: got %T, want av.ContactIntent", intent)
	}
}

// TestFC_Bare_Realistic_Rejects verifies that a bare "FC" command in Realistic
// mode on a non-cleared aircraft is rejected with ErrInvalidCommandSyntax.
func TestFC_Bare_Realistic_Rejects(t *testing.T) {
	s, callsign := makeBareFCSim(t, true /* realistic */)

	_, err := s.runOneControlCommand("TCW1", callsign, "FC", 0, false)
	if err == nil {
		t.Fatal("Realistic bare FC: got nil error, want ErrInvalidCommandSyntax")
	}
	if err != ErrInvalidCommandSyntax {
		t.Fatalf("Realistic bare FC: got error %v, want ErrInvalidCommandSyntax", err)
	}
}

// TestFC_UnknownFreq_Conventional_RoutesToTrackingController verifies that
// FC<digits> with an unknown frequency in Conventional mode silently routes to
// ContactTrackingController (no error, ContactIntent returned).
func TestFC_UnknownFreq_Conventional_RoutesToTrackingController(t *testing.T) {
	s, callsign := makeBareFCSim(t, false /* conventional */)

	// 13560 (135.60 MHz) is not assigned to any controller in the test sim.
	intent, err := s.runOneControlCommand("TCW1", callsign, "FC13560", 0, false)
	if err != nil {
		t.Fatalf("Conventional unknown-freq FC: got error %v, want nil", err)
	}
	if intent == nil {
		t.Fatal("Conventional unknown-freq FC: got nil intent, want ContactIntent")
	}
	if _, ok := intent.(av.ContactIntent); !ok {
		t.Fatalf("Conventional unknown-freq FC: got %T, want av.ContactIntent", intent)
	}
}

// TestFC_UnknownFreq_Realistic_UnknownFrequencyIntent verifies that
// FC<digits> with an unknown frequency in Realistic mode returns an
// UnknownFrequencyIntent (no error).
func TestFC_UnknownFreq_Realistic_UnknownFrequencyIntent(t *testing.T) {
	s, callsign := makeBareFCSim(t, true /* realistic */)

	intent, err := s.runOneControlCommand("TCW1", callsign, "FC13560", 0, false)
	if err != nil {
		t.Fatalf("Realistic unknown-freq FC: got error %v, want nil", err)
	}
	if _, ok := intent.(av.UnknownFrequencyIntent); !ok {
		t.Fatalf("Realistic unknown-freq FC: got %T, want av.UnknownFrequencyIntent", intent)
	}
}

func TestFrequencyChange_ConventionalMode_ForcesPositionReadback(t *testing.T) {
	s, callsign := makeFreqChangeSim(t, false /* conventional */)

	intent, err := s.FrequencyChange("TCW1", callsign, 127750, "", false)
	if err != nil {
		t.Fatalf("FrequencyChange returned error: %v", err)
	}
	ci, ok := intent.(av.ContactIntent)
	if !ok {
		t.Fatalf("expected av.ContactIntent, got %T", intent)
	}
	if ci.SameFacility {
		t.Errorf("Conventional mode: SameFacility = true, want false")
	}
}

func TestFrequencyChange_RealisticMode_AllowsSameFacility(t *testing.T) {
	s, callsign := makeFreqChangeSim(t, true /* realistic */)

	intent, err := s.FrequencyChange("TCW1", callsign, 127750, "", false)
	if err != nil {
		t.Fatalf("FrequencyChange returned error: %v", err)
	}
	ci, ok := intent.(av.ContactIntent)
	if !ok {
		t.Fatalf("expected av.ContactIntent, got %T", intent)
	}
	if !ci.SameFacility {
		t.Errorf("Realistic mode: SameFacility = false, want true")
	}
}
