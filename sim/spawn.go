// sim/spawn.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"fmt"
	"log/slog"
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/nav"
	"github.com/mmp/vice/rand"
	"github.com/mmp/vice/sim/schedule"

	"github.com/goforj/godump"
)

const initialSimSeconds = 30 * 60
const initialSimControlledSeconds = 60

type RunwayLaunchState struct {
	IFRSpawnRate float32
	VFRSpawnRate float32

	// For each runway, when to create the next departing aircraft, based
	// on the runway departure rate. The actual time an aircraft is
	// launched may be later, e.g. if we need longer for wake turbulence
	// separation, etc.
	NextIFRSpawn Time
	NextVFRSpawn Time

	// Aircraft follow the following flows:
	// VFR: ReleasedVFR -> Sequenced
	// IFR no release: Gate -> ReleasedIFR -> Sequenced
	// IFR release required: Gate -> Held -> ReleasedIFR -> Sequenced

	// At the gate, flight plan filed (if IFR), not yet ready to go
	Gate []DepartureAircraft
	// Ready to go, in hold for release purgatory.
	Held []DepartureAircraft
	// Ready to go.
	ReleasedIFR []DepartureAircraft
	ReleasedVFR []DepartureAircraft
	// Sequenced departures, pulled from Released. These are launched in-order.
	Sequenced []DepartureAircraft

	LastDeparture          *DepartureAircraft
	LastArrivalLandingTime Time           // when the last arrival landed on this runway
	LastArrivalFlightRules av.FlightRules // flight rules of the last arrival that landed

	// GoAroundHoldUntil is the time until which departures should be held
	// after a go-around. Departures auto-resume after this time.
	GoAroundHoldUntil Time

	VFRAttempts  int
	VFRSuccesses int
}

// DepartureAircraft represents a departing aircraft, either still on the
// ground or recently-launched.
type DepartureAircraft struct {
	ADSBCallsign  av.ADSBCallsign
	MinSeparation time.Duration // How long after takeoff it will be at ~6000' and airborne
	SpawnTime     Time          // when it was first spawned
	LaunchTime    Time          // when it was actually launched; used for wake turbulence separation, etc.

	// When they're ready to leave the gate
	ReadyDepartGateTime Time

	// HFR-only.
	ReleaseRequested   bool
	ReleaseDelay       time.Duration // minimum wait after release before the takeoff roll
	RequestReleaseTime Time
}

const (
	LaunchAutomatic int32 = iota
	LaunchManual
)

// LaunchConfig collects settings related to launching aircraft in the sim; it's
// passed back and forth between client and server: server provides them so client
// can draw the UI for what's available, then client returns one back when launching.
type LaunchConfig struct {
	// Controller is the TCW in charge of the launch settings; if empty then
	// launch control may be taken by any signed in controller.
	Controller TCW
	// LaunchManual or LaunchAutomatic, separate for each aircraft type
	DepartureMode  int32
	ArrivalMode    int32
	OverflightMode int32

	GoAroundRate         float32
	EnableTowerGoArounds bool
	// airport -> runway -> category -> rate
	DepartureRates     map[string]map[av.RunwayID]map[string]float32
	DepartureRateScale float32

	VFRDepartureRateScale   float32
	VFRAirportRates         map[string]int // name -> VFRRateSum()
	VFFRequestRate          int32
	HaveVFRReportingRegions bool

	// inbound flow -> airport / "overflights" -> rate
	InboundFlowRates            map[string]map[string]float32
	InboundFlowRateScale        float32
	ArrivalPushes               bool
	ArrivalPushFrequencyMinutes int
	ArrivalPushLengthMinutes    int

	EmergencyAircraftRate float32 // Aircraft per hour

	// Schedule, when non-nil, drives IFR rates from authored CSVs instead
	// of the static DepartureRates / InboundFlowRates above. Skipped from
	// JSON because it's a runtime-derived pointer.
	Schedule *schedule.Schedule `json:"-"`
}

func MakeLaunchConfig(dep []DepartureRunway, vfrRateScale float32, vfrAirports map[string]*av.Airport,
	inbound map[string]map[string]int, haveVFRReportingRegions bool) LaunchConfig {
	lc := LaunchConfig{
		GoAroundRate:                0.01,
		DepartureRateScale:          1,
		VFRDepartureRateScale:       vfrRateScale,
		VFRAirportRates:             make(map[string]int),
		VFFRequestRate:              10,
		HaveVFRReportingRegions:     haveVFRReportingRegions,
		InboundFlowRateScale:        1,
		ArrivalPushFrequencyMinutes: 20,
		ArrivalPushLengthMinutes:    10,
		EmergencyAircraftRate:       0,
	}

	for icao, ap := range vfrAirports {
		lc.VFRAirportRates[icao] = ap.VFRRateSum()
	}

	// Walk the departure runways to create the map for departures.
	lc.DepartureRates = make(map[string]map[av.RunwayID]map[string]float32)
	for _, rwy := range dep {
		if _, ok := lc.DepartureRates[rwy.Airport]; !ok {
			lc.DepartureRates[rwy.Airport] = make(map[av.RunwayID]map[string]float32)
		}
		if _, ok := lc.DepartureRates[rwy.Airport][rwy.Runway]; !ok {
			lc.DepartureRates[rwy.Airport][rwy.Runway] = make(map[string]float32)
		}
		lc.DepartureRates[rwy.Airport][rwy.Runway][rwy.Category] = float32(rwy.DefaultRate)
	}

	// Convert the inbound map from int to float32 rates
	lc.InboundFlowRates = make(map[string]map[string]float32)
	for flow, airportOverflights := range inbound {
		lc.InboundFlowRates[flow] = make(map[string]float32)
		for name, rate := range airportOverflights {
			lc.InboundFlowRates[flow][name] = float32(rate)
		}
	}

	return lc
}

// TotalDepartureRate returns the total departure rate (aircraft per hour) for all airports and runways
func (lc *LaunchConfig) TotalDepartureRate() float32 {
	var sum float32
	for _, runwayRates := range lc.DepartureRates {
		sum += sumRateMap2(runwayRates, lc.DepartureRateScale)
	}
	return sum
}

func (lc *LaunchConfig) HaveDepartures() bool {
	return len(lc.DepartureRates) > 0
}

// TotalInboundFlowRate returns the total inbound flow rate (aircraft per hour) for all flows
func (lc *LaunchConfig) TotalInboundFlowRate() float32 {
	var sum float32
	for _, flowRates := range lc.InboundFlowRates {
		for _, rate := range flowRates {
			sum += scaleRate(rate, lc.InboundFlowRateScale)
		}
	}
	return sum
}

// TotalArrivalRate returns the total arrival rate (aircraft per hour) excluding overflights
func (lc *LaunchConfig) TotalArrivalRate() float32 {
	var sum float32
	for _, flowRates := range lc.InboundFlowRates {
		for ap, rate := range flowRates {
			if ap != "overflights" {
				sum += scaleRate(rate, lc.InboundFlowRateScale)
			}
		}
	}
	return sum
}

func (lc *LaunchConfig) HaveArrivals() bool {
	for _, flowRates := range lc.InboundFlowRates {
		for rate := range flowRates {
			if rate != "overflights" {
				return true
			}
		}
	}
	return false
}

// TotalOverflightRate returns the total overflight rate (aircraft per hour)
func (lc *LaunchConfig) TotalOverflightRate() float32 {
	var sum float32
	for _, flowRates := range lc.InboundFlowRates {
		if rate, ok := flowRates["overflights"]; ok {
			sum += scaleRate(rate, lc.InboundFlowRateScale)
		}
	}
	return sum
}

func (lc *LaunchConfig) HaveOverflights() bool {
	for _, flowRates := range lc.InboundFlowRates {
		for rate := range flowRates {
			if rate == "overflights" {
				return true
			}
		}
	}
	return false
}

// CheckRateLimits returns true if both total departure rates and total inbound flow rates
// sum to less than the provided limit (aircraft per hour)
func (lc *LaunchConfig) CheckRateLimits(limit float32) bool {
	totalDepartures := lc.TotalDepartureRate()
	totalInbound := lc.TotalInboundFlowRate()
	return totalDepartures < limit && totalInbound < limit
}

// ClampRates adjusts the rate scale variables to ensure the total launch rate
// does not exceed the given limit (aircraft per hour)
func (lc *LaunchConfig) ClampRates(limit float32) {
	baseDepartureRate := lc.TotalDepartureRate()
	baseInboundRate := lc.TotalInboundFlowRate()

	// If either rate would exceed the limit with current scale, adjust it
	if baseDepartureRate > limit {
		lc.DepartureRateScale *= limit / baseDepartureRate * 0.99
	}

	if baseInboundRate > limit {
		fmt.Printf("%f > %f -> scale %f\n", baseInboundRate, limit, limit/baseInboundRate)
		lc.InboundFlowRateScale *= limit / baseInboundRate * 0.99
	}
}

// sumRateMap2 computes the total rate from a nested map structure
func sumRateMap2(rates map[av.RunwayID]map[string]float32, scale float32) float32 {
	var sum float32
	for _, categoryRates := range rates {
		for _, rate := range categoryRates {
			sum += scaleRate(rate, scale)
		}
	}
	return sum
}

func (s *Sim) SetLaunchConfig(tcw TCW, lc LaunchConfig) error {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	// Update the next spawn time for any rates that changed.
	for ap, rwyRates := range lc.DepartureRates {
		for rwy, categoryRates := range rwyRates {
			r := sumRateMap(categoryRates, s.State.LaunchConfig.DepartureRateScale)
			s.DepartureState[ap][rwy].setIFRRate(s, r)
		}

		for name, rate := range lc.VFRAirportRates {
			r := scaleRate(float32(rate), lc.VFRDepartureRateScale)
			rwy := s.State.VFRRunways[name]
			s.DepartureState[name][av.RunwayID(rwy.Id)].setVFRRate(s, r)
		}

		for group, groupRates := range lc.InboundFlowRates {
			var newSum, oldSum float32
			for ap, rate := range groupRates {
				newSum += rate
				oldSum += s.State.LaunchConfig.InboundFlowRates[group][ap]
			}
			newSum *= lc.InboundFlowRateScale
			oldSum *= s.State.LaunchConfig.InboundFlowRateScale

			if newSum != oldSum {
				pushActive := s.State.SimTime.Before(s.PushEnd)
				s.NextInboundSpawn[group] = s.State.SimTime.Add(randomWait(newSum, pushActive, s.Rand))
			}
		}
	}

	if lc.VFFRequestRate != s.State.LaunchConfig.VFFRequestRate {
		s.NextVFFRequest = s.State.SimTime.Add(randomInitialWait(float32(s.State.LaunchConfig.VFFRequestRate), s.Rand))
	}

	if lc.EmergencyAircraftRate != s.State.LaunchConfig.EmergencyAircraftRate {
		if lc.EmergencyAircraftRate > 0 {
			delay := max(5*time.Minute, randomInitialWait(lc.EmergencyAircraftRate, s.Rand))
			s.NextEmergencyTime = s.State.SimTime.Add(delay)
		} else {
			s.NextEmergencyTime = Time{} // zero time = disabled
		}
	}

	s.lg.Info("Set launch config", slog.Any("launch_config", lc))

	s.State.LaunchConfig = lc
	return nil
}

func (s *Sim) TakeOrReturnLaunchControl(tcw TCW) error {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if lctrl := s.State.LaunchConfig.Controller; lctrl != "" && lctrl != tcw {
		return ErrNotLaunchController
	} else if lctrl == "" {
		s.State.LaunchConfig.Controller = tcw
		s.eventStream.Post(Event{
			Type:        StatusMessageEvent,
			WrittenText: string(tcw) + " is now controlling aircraft launches.",
		})
		s.lg.Debugf("%s: now controlling launches", tcw)
		return nil
	} else {
		s.eventStream.Post(Event{
			Type:        StatusMessageEvent,
			WrittenText: string(s.State.LaunchConfig.Controller) + " is no longer controlling aircraft launches.",
		})
		s.lg.Debugf("%s: no longer controlling launches", tcw)
		s.State.LaunchConfig.Controller = ""
		return nil
	}
}

func (s *Sim) LaunchAircraft(ac Aircraft, departureRunway av.RunwayID) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if departureRunway != "" && ac.HoldForRelease {
		s.addDepartureToPool(&ac, departureRunway, true /* manual launch */)
	} else {
		s.addAircraftNoLock(ac)
	}
}

func (s *Sim) addDepartureToPool(ac *Aircraft, runway av.RunwayID, manualLaunch bool) {
	depac := makeDepartureAircraft(ac, s.State.SimTime, s.wxModel, s.Rand)

	ac.WaitingForLaunch = true
	s.addAircraftNoLock(*ac)

	// The journey begins...
	depState := s.DepartureState[ac.FlightPlan.DepartureAirport][runway]
	if ac.FlightPlan.Rules == av.FlightRulesIFR {
		if manualLaunch {
			// Keep them moving and for HFR, request the release immediately.
			depac.ReadyDepartGateTime = depac.SpawnTime
		}
		// IFRs spend some time at the gate to give them a chance to appear
		// in the FLIGHT PLAN list.
		depState.Gate = append(depState.Gate, depac)
	} else {
		// VFRs can go straight to the queue.
		depState.ReleasedVFR = append(depState.ReleasedVFR, depac)
	}
}

// Assumes the lock is already held (as is the case e.g. for automatic spawning...)
func (s *Sim) addAircraftNoLock(ac Aircraft) {
	if _, ok := s.Aircraft[ac.ADSBCallsign]; ok {
		s.lg.Warn("already have an aircraft with that callsign!",
			slog.String("adsb_callsign", string(ac.ADSBCallsign)))
		return
	}

	if s.CIDAllocator != nil {
		fp := ac.NASFlightPlan
		if fp == nil {
			fp = s.STARSComputer.lookupFlightPlanByACID(ACID(ac.ADSBCallsign))
		}
		if fp != nil && fp.CID == "" {
			if cid, err := s.CIDAllocator.Allocate(); err == nil {
				fp.CID = cid
			} else {
				s.lg.Warn("no CID available", slog.String("callsign", string(ac.ADSBCallsign)))
			}
		}
	}

	s.Aircraft[ac.ADSBCallsign] = &ac

	ac.Nav.Prespawn = s.prespawn && ac.FlightPlan.Rules == av.FlightRulesVFR

	ac.Nav.Check(s.lg)

	// Log initial route for navigation debugging
	nav.LogRoute(string(ac.ADSBCallsign), s.State.SimTime.NavTime(), ac.Nav.Waypoints)

	if ac.FlightPlan.Rules == av.FlightRulesIFR {
		s.TotalIFR++
	} else {
		s.TotalVFR++
	}

	if ac.IsDeparture() {
		s.lg.Debug("launched departure", slog.String("adsb_callsign", string(ac.ADSBCallsign)),
			slog.Any("aircraft", ac))
	} else if ac.IsArrival() {
		s.lg.Debug("launched arrival", slog.String("adsb_callsign", string(ac.ADSBCallsign)),
			slog.Any("aircraft", ac))
	} else if ac.IsOverflight() {
		s.lg.Debug("launched overflight", slog.String("adsb_callsign", string(ac.ADSBCallsign)),
			slog.Any("aircraft", ac))
	} else {
		s.lg.Errorf("%s: launched unknown type?\n", ac.ADSBCallsign)
	}
}

func (s *Sim) Prespawn() {
	start := time.Now()
	s.lg.Info("starting aircraft prespawn")

	// If a schedule is attached, apply its rates NOW so prespawn uses
	// schedule-driven IFR/VFR/overflight rates instead of static ones.
	if s.State.LaunchConfig.Schedule != nil {
		s.mu.Lock(s.lg)
		s.applyScheduledRates()
		s.lastScheduleBucket = scheduleBucketKey(s.State.SimTime.Time())
		s.mu.Unlock(s.lg)
	}

	s.setInitialSpawnTimes(s.State.SimTime)

	s.mu.Lock(s.lg)

	// Prime the pump before the user gets involved
	s.prespawn = true
	for i := range initialSimSeconds {
		// Controlled only at the tail end.
		s.prespawnUncontrolledOnly = i < initialSimSeconds-initialSimControlledSeconds
		// Pattern aircraft only need a few minutes to get established.
		s.prespawnPatternEligible = i >= initialSimSeconds-180

		s.State.SimTime = s.State.SimTime.Add(time.Second)

		s.updateState()
	}
	// Clear Prespawn for all remaining aircraft at the end of prespawn.
	for _, ac := range s.Aircraft {
		ac.Nav.Prespawn = false
	}
	s.prespawnUncontrolledOnly, s.prespawn, s.prespawnPatternEligible = false, false, false

	s.lastUpdateTime = time.Now()

	s.NextVFFRequest = s.State.SimTime.Add(randomInitialWait(float32(s.State.LaunchConfig.VFFRequestRate), s.Rand))

	if s.State.LaunchConfig.EmergencyAircraftRate > 0 {
		delay := max(5*time.Minute, randomInitialWait(s.State.LaunchConfig.EmergencyAircraftRate, s.Rand))
		s.NextEmergencyTime = s.State.SimTime.Add(delay)
	}

	s.mu.Unlock(s.lg)

	s.lg.Info("finished aircraft prespawn")
	fmt.Printf("Prespawn in %s, rates: dep %f arrival %f overflight %f\n", time.Since(start),
		s.State.LaunchConfig.TotalDepartureRate(), s.State.LaunchConfig.TotalArrivalRate(),
		s.State.LaunchConfig.TotalOverflightRate())
	fmt.Println("LaunchConfig:")
	godump.Dump(s.State.LaunchConfig)
}

func (s *Sim) setInitialSpawnTimes(now Time) {
	// Randomize next spawn time for departures and arrivals; may be before
	// or after the current time.
	randomDelay := func(rate float32) Time {
		if rate == 0 {
			return now.Add(365 * 24 * time.Hour)
		}
		avgWait := int(3600 / rate)
		delta := s.Rand.Intn(avgWait) - avgWait/2
		return now.Add(time.Duration(delta) * time.Second)
	}

	if s.State.LaunchConfig.ArrivalPushes {
		// Figure out when the next arrival push will start
		freq := time.Duration(s.State.LaunchConfig.ArrivalPushFrequencyMinutes) * time.Minute
		s.NextPushStart = now.Add(s.Rand.DurationRange(1*time.Minute, freq+1*time.Minute))
	}

	for group, rates := range s.State.LaunchConfig.InboundFlowRates {
		var rateSum float32
		for _, rate := range rates {
			rate = scaleRate(rate, s.State.LaunchConfig.InboundFlowRateScale)
			rateSum += rate
		}
		s.NextInboundSpawn[group] = randomDelay(rateSum)
	}

	for name := range s.State.DepartureAirports {
		s.DepartureState[name] = make(map[av.RunwayID]*RunwayLaunchState)

		if runwayRates, ok := s.State.LaunchConfig.DepartureRates[name]; ok {
			for rwy, rate := range runwayRates {
				r := sumRateMap(rate, s.State.LaunchConfig.DepartureRateScale)
				s.DepartureState[name][rwy] = &RunwayLaunchState{
					IFRSpawnRate: r,
					NextIFRSpawn: randomDelay(r),
				}
			}
		}

		ap := s.State.Airports[name]
		if vfrRate := float32(ap.VFRRateSum()); vfrRate > 0 {
			rwy := s.State.VFRRunways[name]
			state, ok := s.DepartureState[name][av.RunwayID(rwy.Id)]
			if !ok {
				state = &RunwayLaunchState{}
				s.DepartureState[name][av.RunwayID(rwy.Id)] = state
			}
			state.VFRSpawnRate = scaleRate(vfrRate, s.State.LaunchConfig.VFRDepartureRateScale)
			state.NextVFRSpawn = randomDelay(state.VFRSpawnRate)

			// Initialize pattern state for airports with VFR activity,
			// but not at airports that also have IFR departures or arrivals.
			_, hasIFRDepartures := s.State.LaunchConfig.DepartureRates[name]
			_, hasIFRArrivals := s.State.ArrivalAirports[name]
			if !hasIFRDepartures && !hasIFRArrivals {
				s.PatternState[name] = &PatternState{
					NextSpawn: now.Add(randomWait(patternSpawnRate, false, s.Rand)),
				}
			}
		}
	}
}

func scaleRate(rate, scale float32) float32 {
	rate *= scale
	if rate <= 0.5 {
		// Since we round to the nearest int when displaying rates in the UI,
		// we don't want to ever launch for ones that have rate 0.
		return 0
	}
	return rate
}

func sumRateMap(rates map[string]float32, scale float32) float32 {
	var sum float32
	for _, rate := range rates {
		sum += scaleRate(rate, scale)
	}
	return sum
}

// sampleRateMap randomly samples elements from a map of some type T to a
// rate with probability proportional to the element's rate.
func sampleRateMap[T comparable](rates map[T]float32, scale float32, r *rand.Rand) (T, float32) {
	var rateSum float32
	var result T
	for item, rate := range rates {
		rate = scaleRate(rate, scale)
		rateSum += rate
		// Weighted reservoir sampling...
		if rateSum == 0 || r.Float32() < rate/rateSum {
			result = item
		}
	}
	return result, rateSum
}

func randomWait(rate float32, pushActive bool, r *rand.Rand) time.Duration {
	if rate == 0 {
		return 365 * 24 * time.Hour
	}
	if pushActive {
		rate = rate * 3 / 2
	}

	avgSeconds := 3600 / rate
	seconds := r.Float32Range(.85*avgSeconds, 1.15*avgSeconds)
	return time.Duration(seconds * float32(time.Second))
}

// Wait from 0 up to the rate.
func randomInitialWait(rate float32, r *rand.Rand) time.Duration {
	if rate == 0 {
		return 365 * 24 * time.Hour
	}

	seconds := r.Float32Range(0, 3600/rate)
	return time.Duration(seconds * float32(time.Second))
}

func (s *Sim) spawnAircraft() {
	// Spawn each type independently based on its mode
	if s.State.LaunchConfig.ArrivalMode == LaunchAutomatic ||
		s.State.LaunchConfig.OverflightMode == LaunchAutomatic {
		s.spawnArrivalsAndOverflights()
	}
	if s.State.LaunchConfig.DepartureMode == LaunchAutomatic {
		s.spawnDepartures()
	}
	// Pattern aircraft complete a lap in well under a minute, so only
	// spawn them during the last 3 minutes of prespawn (and always after).
	if !s.prespawn || s.prespawnPatternEligible {
		s.spawnPatternAircraft()
	}
	s.updateDepartureSequence()
}

func getAircraftTime(now Time, r *rand.Rand) Time {
	// Hallucinate a random time around the present for the aircraft.
	delta := time.Duration(-20 + r.Intn(40))
	t := now.Add(delta * time.Minute)

	// 9 times out of 10, make it a multiple of 5 minutes
	if r.Intn(10) != 9 {
		dm := t.Minute() % 5
		t = t.Add(time.Duration(5-dm) * time.Minute)
	}

	return t
}

type DepartureRunway struct {
	Airport     string      `json:"airport"`
	Runway      av.RunwayID `json:"runway"`
	Category    string      `json:"category,omitempty"`
	DefaultRate int         `json:"rate"`
}

type ArrivalRunway struct {
	Airport  string             `json:"airport"`
	Runway   av.RunwayID        `json:"runway"`
	GoAround *GoAroundProcedure `json:"go_around,omitempty"`
}

// staticDepartureTotal returns the sum of static DepartureRates across all
// runways and categories for the given airport. Used as the proportional
// denominator when scaling to a schedule-driven total.
func (lc *LaunchConfig) staticDepartureTotal(airport string) float32 {
	var sum float32
	for _, runwayRates := range lc.DepartureRates[airport] {
		for _, r := range runwayRates {
			sum += r
		}
	}
	return sum
}

// staticInboundTotalForAirport returns the sum of static InboundFlowRates
// across all flows that feed the given airport.
func (lc *LaunchConfig) staticInboundTotalForAirport(airport string) float32 {
	var sum float32
	for _, flowRates := range lc.InboundFlowRates {
		sum += flowRates[airport]
	}
	return sum
}

// applyScheduledRates recomputes IFRSpawnRate per runway and
// NextInboundSpawn per flow based on the current SimTime's schedule
// bucket. No-op when LaunchConfig.Schedule is nil.
//
// Per-runway IFR spawn rate becomes:
//
//	(scheduledDepartures[airport]) × (this runway's static-rate share of the
//	 airport's static total)
//
// Per-(flow, airport) arrival rate becomes:
//
//	(scheduledArrivals[airport]) × (this flow's static-rate share of the
//	 airport's static inbound total)
//
// Overflights (entries with the special name "overflights" inside an inbound
// flow) keep their static rate — overflights aren't airport-anchored.
func (s *Sim) applyScheduledRates() {
	lc := &s.State.LaunchConfig
	if lc.Schedule == nil {
		return
	}
	simTime := s.State.SimTime.Time()

	// Departures: for each (airport, runway), set the IFRSpawnRate to the
	// scheduled per-airport total scaled by this runway's static share.
	for airport := range lc.DepartureRates {
		if !lc.Schedule.HasAirport(airport) {
			continue
		}
		scheduledDep, _ := lc.Schedule.RateAt(simTime, airport)
		staticTotal := lc.staticDepartureTotal(airport)
		for runway, categoryRates := range lc.DepartureRates[airport] {
			var runwayStatic float32
			for _, r := range categoryRates {
				runwayStatic += r
			}
			var newRate float32
			if staticTotal > 0 {
				newRate = scheduledDep * (runwayStatic / staticTotal) * lc.DepartureRateScale
			}
			if state := s.DepartureState[airport][runway]; state != nil {
				state.setIFRRate(s, newRate)
			}
		}
	}

	// Compute the busyness factor (current scheduled total / peak for today).
	covered := lc.Schedule.ScheduleAirports()
	if len(covered) > 0 {
		weekday := simTime.Weekday()
		if !s.peakBusynessSet || s.peakBusynessDay != weekday {
			s.peakBusyness = lc.Schedule.PeakTotalForDay(simTime, covered)
			s.peakBusynessDay = weekday
			s.peakBusynessSet = true
		}
		factor := float32(0.05) // floor
		if s.peakBusyness > 0 {
			current := lc.Schedule.CurrentTotalForAirports(simTime, covered)
			factor = current / s.peakBusyness
			if factor < 0.05 {
				factor = 0.05
			}
			if factor > 1.0 {
				factor = 1.0
			}
		}
		s.scheduleBusyness = factor
	} else {
		s.scheduleBusyness = 1.0
	}

	// Arrivals: build a runtime override map keyed [group][airport_or_overflights].
	// spawnArrivalsAndOverflights reads from this when LaunchConfig.Schedule
	// is set so every spawn within the bucket reflects the scheduled rate.
	s.runtimeInboundFlowRates = make(map[string]map[string]float32, len(lc.InboundFlowRates))
	for group, groupRates := range lc.InboundFlowRates {
		override := make(map[string]float32, len(groupRates))
		var newSum, staticSum float32
		for airport, staticRate := range groupRates {
			staticSum += staticRate
			if airport == "overflights" {
				scaled := staticRate * s.scheduleBusyness
				override[airport] = scaled
				newSum += scaled
				continue
			}
			if !lc.Schedule.HasAirport(airport) {
				override[airport] = staticRate
				newSum += staticRate
				continue
			}
			_, scheduledArr := lc.Schedule.RateAt(simTime, airport)
			staticTotal := lc.staticInboundTotalForAirport(airport)
			var rate float32
			if staticTotal > 0 {
				rate = scheduledArr * (staticRate / staticTotal)
			}
			override[airport] = rate
			newSum += rate
		}
		s.runtimeInboundFlowRates[group] = override
		// Only refresh the timer if the rate changed materially. Avoids the
		// "Poisson hiccup every 15 minutes when nothing actually changed" case.
		if newSum != staticSum && newSum > 0 {
			pushActive := s.State.SimTime.Before(s.PushEnd)
			s.NextInboundSpawn[group] = s.State.SimTime.Add(randomWait(newSum*lc.InboundFlowRateScale, pushActive, s.Rand))
		}
	}

	// VFR: rescale all VFR rates by the busyness factor.
	for name, rate := range lc.VFRAirportRates {
		r := scaleRate(float32(rate), lc.VFRDepartureRateScale*s.scheduleBusyness)
		rwy := s.State.VFRRunways[name]
		if state, ok := s.DepartureState[name][av.RunwayID(rwy.Id)]; ok && state != nil {
			state.setVFRRate(s, r)
		}
	}
}

// SetSchedule attaches the schedule and immediately applies the current
// bucket's rates so the sim doesn't lag behind. Pass nil to detach.
func (s *Sim) SetSchedule(sch *schedule.Schedule) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)
	s.State.LaunchConfig.Schedule = sch
	s.lastScheduleBucket = ""
	s.runtimeInboundFlowRates = nil
	if sch != nil {
		k := scheduleBucketKey(s.State.SimTime.Time())
		s.lastScheduleBucket = k
		s.applyScheduledRates()
	}
}
