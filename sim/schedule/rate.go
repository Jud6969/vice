// pkg/sim/schedule/rate.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package schedule

import (
	"fmt"
	"time"
)

// HasAirport reports whether the schedule has data for icao. Safe on a nil
// receiver (returns false).
func (s *Schedule) HasAirport(icao string) bool {
	if s == nil {
		return false
	}
	_, ok := s.Airports[icao]
	return ok
}

// RateAt returns the (dep, arr) per-hour rates for the given airport at the
// given simulation time. Missing airports or missing buckets yield (0, 0).
// MonthlyMultiplier (if any) is applied.
func (s *Schedule) RateAt(simTime time.Time, icao string) (dep, arr float32) {
	if s == nil {
		return 0, 0
	}
	ap, ok := s.Airports[icao]
	if !ok {
		return 0, 0
	}
	key := bucketKey(simTime)
	b := ap.Buckets[key]
	mult := float32(1.0)
	if m, ok := ap.MonthlyMultiplier[int(simTime.Month())]; ok {
		mult = m
	}
	return b.Dep * mult, b.Arr * mult
}

// AggregateForDay returns 96 Buckets, one per 15-min slot of simDate's day
// (in simDate's location), summed across the given airports. Used by the
// histogram. Missing airports contribute zero.
func (s *Schedule) AggregateForDay(simDate time.Time, airports []string) [96]Bucket {
	var out [96]Bucket
	if s == nil {
		return out
	}
	y, mo, d := simDate.Date()
	loc := simDate.Location()
	for i := 0; i < 96; i++ {
		t := time.Date(y, mo, d, i/4, (i%4)*15, 0, 0, loc)
		for _, icao := range airports {
			dep, arr := s.RateAt(t, icao)
			out[i].Dep += dep
			out[i].Arr += arr
		}
	}
	return out
}

// bucketKey formats a time as "DAY:HH:MM" floored to a 15-min boundary.
func bucketKey(t time.Time) string {
	day := weekdayKey(t.Weekday())
	hh := t.Hour()
	mm := (t.Minute() / 15) * 15
	return fmt.Sprintf("%s:%02d:%02d", day, hh, mm)
}

// PeakTotalForDay returns the maximum (dep+arr) total summed across
// `airports` across all 96 buckets of the day represented by simDate.
// Used to compute a "fraction-of-peak" busyness factor.
func (s *Schedule) PeakTotalForDay(simDate time.Time, airports []string) float32 {
	if s == nil {
		return 0
	}
	agg := s.AggregateForDay(simDate, airports)
	var peak float32
	for _, b := range agg {
		if v := b.Dep + b.Arr; v > peak {
			peak = v
		}
	}
	return peak
}

// CurrentTotalForAirports returns (dep+arr) summed across `airports`
// for the bucket containing simTime.
func (s *Schedule) CurrentTotalForAirports(simTime time.Time, airports []string) float32 {
	if s == nil {
		return 0
	}
	var sum float32
	for _, icao := range airports {
		dep, arr := s.RateAt(simTime, icao)
		sum += dep + arr
	}
	return sum
}

// ScheduleAirports returns the list of airport ICAOs the schedule covers.
func (s *Schedule) ScheduleAirports() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.Airports))
	for icao := range s.Airports {
		out = append(out, icao)
	}
	return out
}

// OverflightScaleForFlow returns the scale factor an overflight rate
// for the given inbound-flow group should use, based on the origin
// airport's scheduled-vs-static departure ratio. Returns (0, false) if
// the flow isn't mapped to an origin or the origin's static dep total
// is zero (callers should fall back to the global busyness factor in
// that case).
func (s *Schedule) OverflightScaleForFlow(simTime time.Time, flow string,
	staticDepTotal func(airport string) float32) (float32, bool) {
	if s == nil {
		return 0, false
	}
	icao, ok := s.OverflightOrigins[flow]
	if !ok {
		return 0, false
	}
	if !s.HasAirport(icao) {
		return 0, false
	}
	staticTotal := staticDepTotal(icao)
	if staticTotal <= 0 {
		return 0, false
	}
	scheduledDep, _ := s.RateAt(simTime, icao)
	return scheduledDep / staticTotal, true
}

func weekdayKey(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "MON"
	case time.Tuesday:
		return "TUE"
	case time.Wednesday:
		return "WED"
	case time.Thursday:
		return "THU"
	case time.Friday:
		return "FRI"
	case time.Saturday:
		return "SAT"
	case time.Sunday:
		return "SUN"
	}
	return ""
}
