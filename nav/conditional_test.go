package nav

import (
	"strings"
	"testing"

	av "github.com/mmp/vice/aviation"
	vmath "github.com/mmp/vice/math"
	vrand "github.com/mmp/vice/rand"
)

func TestNavHasPendingConditionalCommandField(t *testing.T) {
	var n Nav
	if n.PendingConditionalCommand != nil {
		t.Fatalf("PendingConditionalCommand should default to nil, got %+v", n.PendingConditionalCommand)
	}
	n.PendingConditionalCommand = &PendingConditionalCommand{
		Kind:     ConditionalLeaving,
		Altitude: 3000,
	}
	if n.PendingConditionalCommand.Kind != ConditionalLeaving {
		t.Fatalf("expected ConditionalLeaving, got %d", n.PendingConditionalCommand.Kind)
	}
	if n.PendingConditionalCommand.Altitude != 3000 {
		t.Fatalf("expected 3000, got %v", n.PendingConditionalCommand.Altitude)
	}
}

func TestConditionalHeadingExecuteClosest(t *testing.T) {
	n := makeTestNav(t, 180)
	action := ConditionalHeading{Heading: 10, Turn: av.TurnClosest}
	action.Execute(&n, Time{}, av.Temperature{})
	if assigned, ok := n.AssignedHeading(); !ok || assigned != 10 {
		t.Fatalf("expected assigned heading 10, got ok=%v heading=%v", ok, assigned)
	}
}

func TestConditionalHeadingExecuteByDegreesLeft(t *testing.T) {
	n := makeTestNav(t, 180)
	action := ConditionalHeading{ByDegrees: 30, Turn: av.TurnLeft}
	action.Execute(&n, Time{}, av.Temperature{})
	// TurnLeft 30 from 180 -> 150
	if assigned, ok := n.AssignedHeading(); !ok || assigned != 150 {
		t.Fatalf("expected assigned heading 150, got ok=%v heading=%v", ok, assigned)
	}
}

func TestConditionalHeadingExecuteByDegreesRight(t *testing.T) {
	n := makeTestNav(t, 180)
	action := ConditionalHeading{ByDegrees: 30, Turn: av.TurnRight}
	action.Execute(&n, Time{}, av.Temperature{})
	// TurnRight 30 from 180 -> 210
	if assigned, ok := n.AssignedHeading(); !ok || assigned != 210 {
		t.Fatalf("expected assigned heading 210, got ok=%v heading=%v", ok, assigned)
	}
}

func TestConditionalHeadingRender(t *testing.T) {
	cases := []struct {
		action ConditionalHeading
		want   string // substring in written form
	}{
		{ConditionalHeading{Heading: 10, Turn: av.TurnClosest}, "010"},
		{ConditionalHeading{Heading: 100, Turn: av.TurnRight}, "right"},
		{ConditionalHeading{Heading: 100, Turn: av.TurnLeft}, "left"},
		{ConditionalHeading{ByDegrees: 20, Turn: av.TurnLeft}, "left 20"},
		{ConditionalHeading{ByDegrees: 20, Turn: av.TurnRight}, "right 20"},
	}
	r := vrand.Make()
	for _, tc := range cases {
		rt := &av.RadioTransmission{}
		tc.action.Render(rt, r)
		written := rt.Written(r)
		if !strings.Contains(strings.ToLower(written), strings.ToLower(tc.want)) {
			t.Errorf("Render(%+v) = %q; want containing %q", tc.action, written, tc.want)
		}
	}
}

func makeTestNav(t *testing.T, heading vmath.MagneticHeading) Nav {
	t.Helper()
	n := Nav{
		Rand: vrand.Make(),
	}
	n.FlightState.Heading = heading
	n.FlightState.Altitude = 2000
	return n
}

func TestConditionalDirectFixExecute(t *testing.T) {
	n := makeTestNavWithRoute(t, "SAJUL")
	action := ConditionalDirectFix{Fix: "SAJUL", Turn: av.TurnClosest}
	action.Execute(n, Time{}, av.Temperature{})
	// After direct-fix, the first waypoint should be the target fix.
	if len(n.Waypoints) == 0 || n.Waypoints[0].Fix != "SAJUL" {
		t.Fatalf("expected first waypoint SAJUL, got %+v", n.Waypoints)
	}
}

func TestConditionalDirectFixRender(t *testing.T) {
	cases := []struct {
		action ConditionalDirectFix
		want   string
	}{
		{ConditionalDirectFix{Fix: "SAJUL", Turn: av.TurnClosest}, "direct"},
		{ConditionalDirectFix{Fix: "SAJUL", Turn: av.TurnLeft}, "left"},
		{ConditionalDirectFix{Fix: "SAJUL", Turn: av.TurnRight}, "right"},
	}
	r := vrand.Make()
	for _, tc := range cases {
		rt := &av.RadioTransmission{}
		tc.action.Render(rt, r)
		written := strings.ToLower(rt.Written(r))
		if !strings.Contains(written, strings.ToLower(tc.want)) {
			t.Errorf("Render(%+v) = %q; want containing %q", tc.action, written, tc.want)
		}
	}
}

// makeTestNavWithRoute returns a *Nav whose Waypoints contains a waypoint
// with the given fix name, suitable for calling DirectFix on it.
func makeTestNavWithRoute(t *testing.T, fix string) *Nav {
	t.Helper()
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        fix + "/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     250,
	})
	return f.nav
}

func TestConditionalSpeedExecute(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     250,
	})
	sr := av.MakeAtSpeedRestriction(210)
	action := ConditionalSpeed{Restriction: sr}
	action.Execute(f.nav, Time{}, av.Temperature{})
	if f.nav.Speed.Assigned == nil {
		t.Fatalf("expected Speed.Assigned set, got nil")
	}
	if got, ok := f.nav.Speed.Assigned.ExactValue(); !ok || got != 210 {
		t.Fatalf("expected 210, got ok=%v value=%v", ok, got)
	}
}

func TestConditionalMachExecute(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  30000,
		InitialSpeed:     280,
	})
	action := ConditionalMach{Mach: 0.78}
	// Use a plausible high-altitude temperature (ISA at 30k ≈ -45°C).
	action.Execute(f.nav, Time{}, av.MakeTemperatureFromCelsius(-45))

	// AssignMach sets Speed.Assigned with IsMach=true. Assert on that surface.
	if f.nav.Speed.Assigned == nil {
		t.Fatalf("expected Speed.Assigned set, got nil")
	}
	if !f.nav.Speed.Assigned.IsMach {
		t.Fatalf("expected mach restriction, got speed")
	}
}
