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
