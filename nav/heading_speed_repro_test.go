package nav

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
)

// Probe: assign exact speed, then heading, observe speed evolution.
// This time waypoints have altitude restrictions, so assignHeading
// triggers the "off-route arrival" branch that sets Altitude.Cleared.
func TestProbeSpeedThenHeading_Exact(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/a10000/star DETGY/a7000/star HAUPT/a6000/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     280,
		OnSTAR:           true,
	})

	sr := av.MakeAtSpeedRestriction(220)
	f.nav.AssignSpeed(&sr, false)

	t.Logf("after AssignSpeed: Assigned=%+v Restriction=%+v", f.nav.Speed.Assigned, f.nav.Speed.Restriction)

	for i := 0; i < 30; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
	}

	t.Logf("before heading: IAS=%.0f hdg=%.0f", f.nav.FlightState.IAS, f.nav.FlightState.Heading)

	f.nav.AssignHeading(math.MagneticHeading(270), av.TurnClosest, f.simTime, 0)

	t.Logf("after AssignHeading (immediate): Assigned=%+v Heading.Assigned=%v Deferred=%v",
		f.nav.Speed.Assigned, f.nav.Heading.Assigned, f.nav.DeferredNavHeading)

	for i := 0; i < 120; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
		if i%10 == 0 {
			t.Logf("tick %d: IAS=%.0f hdg=%.0f Assigned=%+v",
				i, f.nav.FlightState.IAS, f.nav.FlightState.Heading, f.nav.Speed.Assigned)
		}
	}
}

// At-or-above: this is the suspect — the user asks for a SLOW speed
// floor, then changes heading. With the 44a796f3 logic, the target is
// clamp(IAS, lo, hi) which means the aircraft sits at whatever it
// happens to be at, but if IAS climbs (because acceleration overshoots
// the clamp) it could keep going.
func TestProbeSpeedThenHeading_AtOrAbove(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     220,
		OnSTAR:           true,
	})

	sr := av.MakeAtOrAboveSpeedRestriction(220)
	f.nav.AssignSpeed(&sr, false)

	t.Logf("after AssignSpeed (atOrAbove 220): Assigned=%+v", f.nav.Speed.Assigned)

	for i := 0; i < 30; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
	}

	t.Logf("before heading: IAS=%.0f hdg=%.0f", f.nav.FlightState.IAS, f.nav.FlightState.Heading)

	f.nav.AssignHeading(math.MagneticHeading(270), av.TurnClosest, f.simTime, 0)

	for i := 0; i < 120; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
		if i%10 == 0 {
			t.Logf("tick %d: IAS=%.0f hdg=%.0f Assigned=%+v",
				i, f.nav.FlightState.IAS, f.nav.FlightState.Heading, f.nav.Speed.Assigned)
		}
	}
}

// Probe: assigned altitude in progress with speed in transit, then heading.
// In prepareAltitudeAssignment, if a speed assignment exists and the
// aircraft is mid-altitude-change with delta>=20kt, Speed.Assigned gets
// stashed into Speed.AfterAltitude. Then the controller throws a heading.
func TestProbeAltSpeedHeading_Sequence(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     280,
		AssignedAltitude: 11000,
		OnSTAR:           true,
	})

	// Step 1: descend to 6000.
	f.nav.AssignAltitude(6000, false, f.simTime, 0)
	t.Logf("after AssignAltitude: Altitude=%+v Speed=%+v",
		f.nav.Altitude, f.nav.Speed)

	// Step 2: speed 220 (40kt change so >= 20).
	sr := av.MakeAtSpeedRestriction(220)
	f.nav.AssignSpeed(&sr, false)
	t.Logf("after AssignSpeed: Altitude=%+v Speed=%+v",
		f.nav.Altitude, f.nav.Speed)

	// run a few ticks
	for i := 0; i < 30; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
	}
	t.Logf("after 30 ticks: IAS=%.0f Alt=%.0f Speed=%+v Altitude=%+v",
		f.nav.FlightState.IAS, f.nav.FlightState.Altitude, f.nav.Speed, f.nav.Altitude)

	// Step 3: heading 270.
	f.nav.AssignHeading(math.MagneticHeading(270), av.TurnClosest, f.simTime, 0)
	t.Logf("after AssignHeading: Speed=%+v Altitude=%+v",
		f.nav.Speed, f.nav.Altitude)

	for i := 0; i < 180; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
		if i%15 == 0 {
			t.Logf("tick %d: IAS=%.0f Alt=%.0f hdg=%.0f Speed.Assigned=%v Speed.AfterAlt=%v Altitude.Assigned=%v",
				i, f.nav.FlightState.IAS, f.nav.FlightState.Altitude,
				f.nav.FlightState.Heading,
				f.nav.Speed.Assigned, f.nav.Speed.AfterAltitude,
				f.nav.Altitude.Assigned)
		}
	}
}

// Probe: speed first, THEN altitude (assignAltitude after assignSpeed
// triggers prepareAltitudeAssignment's swap of Speed.Assigned ->
// Speed.AfterAltitude). After that, give heading.
func TestProbeSpeedAltHeading_Sequence(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     280,
		AssignedAltitude: 11000,
		OnSTAR:           true,
	})

	// Step 1: speed 220.
	sr := av.MakeAtSpeedRestriction(220)
	f.nav.AssignSpeed(&sr, false)
	t.Logf("after AssignSpeed: Altitude=%+v Speed=%+v", f.nav.Altitude, f.nav.Speed)

	// Step 2: descend to 6000. Speed delta is 60 (>= 20), so the
	// "defer speed until altitude reached" branch fires in
	// prepareAltitudeAssignment.
	f.nav.AssignAltitude(6000, false, f.simTime, 0)
	t.Logf("after AssignAltitude: Altitude=%+v Speed=%+v", f.nav.Altitude, f.nav.Speed)

	for i := 0; i < 30; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
	}
	t.Logf("after 30 ticks: IAS=%.0f Alt=%.0f Speed.Assigned=%v Speed.AfterAlt=%v",
		f.nav.FlightState.IAS, f.nav.FlightState.Altitude,
		f.nav.Speed.Assigned, f.nav.Speed.AfterAltitude)

	// Step 3: heading 270.
	f.nav.AssignHeading(math.MagneticHeading(270), av.TurnClosest, f.simTime, 0)
	t.Logf("after AssignHeading: Speed=%+v", f.nav.Speed)

	for i := 0; i < 240; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
		if i%20 == 0 {
			t.Logf("tick %d: IAS=%.0f Alt=%.0f hdg=%.0f Speed.Assigned=%v Speed.AfterAlt=%v",
				i, f.nav.FlightState.IAS, f.nav.FlightState.Altitude, f.nav.FlightState.Heading,
				f.nav.Speed.Assigned, f.nav.Speed.AfterAltitude)
		}
	}
}

// Same but with at-or-below
func TestProbeSpeedThenHeading_AtOrBelow(t *testing.T) {
	f := NewArrivalFlight(t, ArrivalConfig{
		Waypoints:        "SAJUL/star DETGY/star HAUPT/star",
		DepartureAirport: "KMCO",
		ArrivalAirport:   "KJFK",
		AircraftType:     "A320",
		InitialAltitude:  11000,
		InitialSpeed:     280,
		OnSTAR:           true,
	})

	sr := av.MakeAtOrBelowSpeedRestriction(220)
	f.nav.AssignSpeed(&sr, false)

	t.Logf("after AssignSpeed (atOrBelow 220): Assigned=%+v", f.nav.Speed.Assigned)

	for i := 0; i < 30; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
	}

	t.Logf("before heading: IAS=%.0f hdg=%.0f", f.nav.FlightState.IAS, f.nav.FlightState.Heading)

	f.nav.AssignHeading(math.MagneticHeading(270), av.TurnClosest, f.simTime, 0)

	for i := 0; i < 120; i++ {
		wxs := f.weather(f.nav.FlightState.Altitude)
		f.nav.UpdateWithWeather(f.callsign, wxs, &f.fp, f.simTime, nil)
		f.simTime = f.simTime.Add(1_000_000_000)
		if i%10 == 0 {
			t.Logf("tick %d: IAS=%.0f hdg=%.0f Assigned=%+v",
				i, f.nav.FlightState.IAS, f.nav.FlightState.Heading, f.nav.Speed.Assigned)
		}
	}
}
