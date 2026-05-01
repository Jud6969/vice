// pkg/sim/schedule/rate_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package schedule

import (
	"testing"
	"time"
)

func mkSched() *Schedule {
	return &Schedule{
		Airports: map[string]*AirportSchedule{
			"KLGA": {
				MonthlyMultiplier: map[int]float32{7: 1.25, 12: 0.85},
				Buckets: map[string]Bucket{
					"MON:07:00": {Dep: 28, Arr: 22},
					"MON:07:15": {Dep: 28, Arr: 8},
				},
			},
		},
	}
}

func TestHasAirport(t *testing.T) {
	s := mkSched()
	if !s.HasAirport("KLGA") {
		t.Fatal("KLGA should be present")
	}
	if s.HasAirport("KJFK") {
		t.Fatal("KJFK should not be present")
	}
	var nilS *Schedule
	if nilS.HasAirport("KLGA") {
		t.Fatal("nil schedule must report no airports")
	}
}

func TestRateAtBucketLookup(t *testing.T) {
	s := mkSched()
	// Mon 07:14 → 07:00 bucket.
	mon0714 := time.Date(2026, 5, 4, 7, 14, 0, 0, time.UTC) // 2026-05-04 is Monday
	dep, arr := s.RateAt(mon0714, "KLGA")
	if dep != 28 || arr != 22 {
		t.Fatalf("Mon 07:14 want (28,22), got (%v,%v)", dep, arr)
	}
	// Mon 07:15 → 07:15 bucket.
	mon0715 := time.Date(2026, 5, 4, 7, 15, 0, 0, time.UTC)
	dep, arr = s.RateAt(mon0715, "KLGA")
	if dep != 28 || arr != 8 {
		t.Fatalf("Mon 07:15 want (28,8), got (%v,%v)", dep, arr)
	}
	// Mon 06:59 → 06:45 bucket → not present → zero.
	mon0659 := time.Date(2026, 5, 4, 6, 59, 0, 0, time.UTC)
	dep, arr = s.RateAt(mon0659, "KLGA")
	if dep != 0 || arr != 0 {
		t.Fatalf("Mon 06:59 want (0,0), got (%v,%v)", dep, arr)
	}
}

func TestRateAtMonthlyMultiplier(t *testing.T) {
	s := mkSched()
	// Same Monday in July → ×1.25.
	jul := time.Date(2026, 7, 6, 7, 0, 0, 0, time.UTC) // Monday
	dep, arr := s.RateAt(jul, "KLGA")
	if dep != 28*1.25 || arr != 22*1.25 {
		t.Fatalf("July Mon 07:00 want (35,27.5), got (%v,%v)", dep, arr)
	}
	// December → ×0.85.
	dec := time.Date(2026, 12, 7, 7, 0, 0, 0, time.UTC) // Monday
	dep, arr = s.RateAt(dec, "KLGA")
	_ = arr
	want := float32(28) * float32(0.85)
	if dep != want {
		t.Fatalf("Dec dep mult: %v", dep)
	}
}

func TestRateAtMissingAirport(t *testing.T) {
	s := mkSched()
	dep, arr := s.RateAt(time.Now(), "KJFK")
	if dep != 0 || arr != 0 {
		t.Fatalf("missing airport want (0,0), got (%v,%v)", dep, arr)
	}
}

func TestAggregateForBuckets(t *testing.T) {
	s := mkSched()
	// On a Monday in May, aggregate over KLGA and missing-airport KJFK
	// for the full 96-bucket day. Only two of those buckets have data.
	day := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // Monday
	agg := s.AggregateForDay(day, []string{"KLGA", "KJFK"})
	if len(agg) != 96 {
		t.Fatalf("want 96 buckets, got %d", len(agg))
	}
	// 07:00 bucket → idx 28
	if agg[28].Dep != 28 || agg[28].Arr != 22 {
		t.Fatalf("idx 28 want (28,22), got %+v", agg[28])
	}
	// 07:15 bucket → idx 29
	if agg[29].Dep != 28 || agg[29].Arr != 8 {
		t.Fatalf("idx 29 want (28,8), got %+v", agg[29])
	}
	// All others zero
	for i, b := range agg {
		if i == 28 || i == 29 {
			continue
		}
		if b.Dep != 0 || b.Arr != 0 {
			t.Fatalf("idx %d want zero, got %+v", i, b)
		}
	}
}

func TestOverflightScaleForFlow(t *testing.T) {
	s := mkSched()
	s.OverflightOrigins = map[string]string{
		"EWR_EAST_DEPS": "KLGA", // mkSched only knows about KLGA, so use it as a stand-in
	}
	// At MON 07:00, KLGA's scheduled dep is 28; suppose the static dep
	// total for KLGA is 56. Then scale should be 28/56 = 0.5.
	staticTotal := func(airport string) float32 {
		if airport == "KLGA" {
			return 56
		}
		return 0
	}
	mon0700 := time.Date(2026, 5, 4, 7, 0, 0, 0, time.UTC)
	scale, ok := s.OverflightScaleForFlow(mon0700, "EWR_EAST_DEPS", staticTotal)
	if !ok {
		t.Fatal("expected ok")
	}
	if scale != 0.5 {
		t.Fatalf("scale: %v", scale)
	}

	// Unmapped flow → not ok.
	if _, ok := s.OverflightScaleForFlow(mon0700, "UNMAPPED", staticTotal); ok {
		t.Fatal("expected not ok for unmapped flow")
	}

	// Origin airport with zero static total → not ok (caller falls back).
	staticTotalZero := func(airport string) float32 { return 0 }
	if _, ok := s.OverflightScaleForFlow(mon0700, "EWR_EAST_DEPS", staticTotalZero); ok {
		t.Fatal("expected not ok when static total is zero")
	}
}

func TestPeakAndCurrentTotal(t *testing.T) {
	s := mkSched()
	day := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // Monday
	peak := s.PeakTotalForDay(day, []string{"KLGA"})
	if peak != 28+22 { // MON 07:00 has dep=28 arr=22 (largest bucket)
		t.Fatalf("peak: %v", peak)
	}
	mon0700 := time.Date(2026, 5, 4, 7, 0, 0, 0, time.UTC)
	cur := s.CurrentTotalForAirports(mon0700, []string{"KLGA"})
	if cur != 50 {
		t.Fatalf("current: %v", cur)
	}
}
