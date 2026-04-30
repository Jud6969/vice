# Real-World Schedule Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in per-airport, 15-min-bucket schedule files (CSV) that drive vice's IFR spawn rates, plus a connect-dialog picker (month + day-of-week + time) with an aggregated traffic histogram. Sim time advances naturally from the picked moment.

**Architecture:** A new leaf package `sim/schedule/` parses `schedules.json` + per-airport CSVs and exposes `RateAt(simTime, icao)`. `LaunchConfig` grows a non-persisted `*schedule.Schedule` field; the existing arrivals/departures spawn paths consult it instead of static rates when set. The scenario configuration screen gains a "Use real-world schedule" checkbox, three sliders (month/day/time), and a 96-bar stacked histogram. Sim `StartTime` is overridden at connect time to match the picked moment.

**Tech Stack:** Go, `encoding/json`, `encoding/csv`, cimgui-go for the picker UI. No new external dependencies.

**Branch:** `schedule-traffic` (already off `upstream/master` @ `6aedbfbc`).

**Reference files for the engineer:**
- `sim/spawn.go:85` — `LaunchConfig` struct (the integration target).
- `sim/spawn.go:159–230` — rate accessors (`TotalDepartureRate`, `TotalInboundFlowRate`, etc.) — useful pattern for `RateAt`.
- `sim/spawn.go:269` (departure spawn) and `:488` (arrival spawn) — the loops we override when schedule is non-nil.
- `sim/sim.go:168` — `Sim.StartTime` field; `:210` and `:223` etc. — where it's used.
- `cmd/vice/simconfig.go:1253` — `NewSimConfiguration.DrawConfigurationUI` (the configuration modal screen).
- `cmd/vice/simconfig.go:2304` — where `StartTime` is set from sampled METAR. Schedule-mode override goes near this.

---

## File structure

**New files:**
- `sim/schedule/format.go` — `Schedule`, `AirportSchedule`, `Bucket` types; JSON marshaling.
- `sim/schedule/loader.go` — `LoadARTCC(dir, lg)` walks `schedules.json` and per-airport CSVs.
- `sim/schedule/loader_test.go` — round-trip + edge-case tests.
- `sim/schedule/rate.go` — `RateAt(simTime, icao)`, `HasAirport(icao)`, `Aggregate(...)` for histogram.
- `sim/schedule/rate_test.go` — bucket lookup, monthly multiplier, missing data, time-walk.
- `cmd/vice/schedhist.go` — histogram drawer (96 stacked bars + per-bar hover tooltip).
- `resources/configurations/ZNY/schedules.json` — sample manifest.
- `resources/configurations/ZNY/KLGA-schedule.csv` — sample airport CSV (one full week).

**Modified files:**
- `sim/spawn.go` — `LaunchConfig` gets `Schedule *schedule.Schedule` (non-persisted, JSON-skip).
- `sim/spawn_arrivals.go` — when `Schedule != nil`, override the static rate per airport.
- `sim/spawn_departures.go` — same.
- `cmd/vice/simconfig.go` — `NewSimConfiguration` gets schedule fields + UI hooks; `DrawConfigurationUI` renders the new controls.
- `server/scenario.go` (or wherever scenarios load) — surface the loaded `*Schedule` from the ARTCC's folder so `NewSimConfiguration` can find it.

---

## Task 1: Schedule package format types + JSON parser

End state: a self-contained `sim/schedule/` package that parses `schedules.json` round-trip with passing tests. No CSV yet, no integration.

**Files:**
- Create: `sim/schedule/format.go`
- Create: `sim/schedule/loader_test.go` (initial form — extended in later tasks)

- [ ] **Step 1.1: Create `sim/schedule/format.go`**

```go
// pkg/sim/schedule/format.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

// Package schedule loads and queries per-airport, per-15-min-bucket
// schedule data used to drive IFR spawn rates from a recorded weekly
// pattern instead of constant Poisson rates.
package schedule

// Schedule is a fully-loaded set of per-airport schedules for one ARTCC.
type Schedule struct {
	// Airports is keyed by ICAO (e.g., "KLGA").
	Airports map[string]*AirportSchedule
}

// AirportSchedule is one airport's schedule.
type AirportSchedule struct {
	// MonthlyMultiplier scales rates per calendar month (1=Jan ... 12=Dec).
	// Missing months default to 1.0.
	MonthlyMultiplier map[int]float32

	// Buckets is keyed by Weekday + ":" + "HH:MM" (e.g., "MON:07:15").
	// Each entry holds the per-hour rate for departures and arrivals
	// during that 15-min window. Missing keys imply zero rate.
	Buckets map[string]Bucket
}

// Bucket is one 15-min schedule entry.
type Bucket struct {
	Dep float32 // departures per hour
	Arr float32 // arrivals per hour
}

// scheduleManifest is the JSON form of schedules.json (CSV filenames per
// airport plus monthly multipliers).
type scheduleManifest struct {
	Airports map[string]airportManifest `json:"airports"`
}

type airportManifest struct {
	CSV               string             `json:"csv"`
	MonthlyMultiplier map[string]float32 `json:"monthlyMultiplier,omitempty"`
}
```

- [ ] **Step 1.2: Failing test for manifest JSON parsing**

Create `sim/schedule/loader_test.go`:

```go
// pkg/sim/schedule/loader_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package schedule

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestManifestRoundtrip(t *testing.T) {
	src := []byte(`{
		"airports": {
			"KLGA": {
				"csv": "KLGA-schedule.csv",
				"monthlyMultiplier": {"1": 0.9, "7": 1.25, "12": 0.85}
			},
			"KJFK": {"csv": "KJFK-schedule.csv"}
		}
	}`)
	var m scheduleManifest
	if err := json.Unmarshal(src, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Airports) != 2 {
		t.Fatalf("want 2 airports, got %d", len(m.Airports))
	}
	if m.Airports["KLGA"].CSV != "KLGA-schedule.csv" {
		t.Fatalf("KLGA csv: %q", m.Airports["KLGA"].CSV)
	}
	if m.Airports["KLGA"].MonthlyMultiplier["7"] != 1.25 {
		t.Fatalf("KLGA July multiplier: %v", m.Airports["KLGA"].MonthlyMultiplier["7"])
	}
	if m.Airports["KJFK"].MonthlyMultiplier != nil && len(m.Airports["KJFK"].MonthlyMultiplier) != 0 {
		t.Fatalf("KJFK should have empty/nil multipliers, got %v", m.Airports["KJFK"].MonthlyMultiplier)
	}

	// Round-trip
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(m); err != nil {
		t.Fatal(err)
	}
	var m2 scheduleManifest
	if err := json.Unmarshal(buf.Bytes(), &m2); err != nil {
		t.Fatal(err)
	}
	if len(m2.Airports) != 2 {
		t.Fatalf("roundtrip: want 2 airports, got %d", len(m2.Airports))
	}
}
```

- [ ] **Step 1.3: Run tests**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v
rm sched_test.exe
```
Expected: PASS.

- [ ] **Step 1.4: Commit**

```bash
git add sim/schedule/format.go sim/schedule/loader_test.go
git commit -m "sim/schedule: format types + manifest JSON roundtrip test

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: CSV loader

End state: `LoadARTCC(dir)` reads `schedules.json` + per-airport CSVs from the given directory and returns a populated `*Schedule`.

**Files:**
- Create: `sim/schedule/loader.go`
- Extend: `sim/schedule/loader_test.go`

- [ ] **Step 2.1: Failing test for LoadARTCC**

Append to `sim/schedule/loader_test.go`:

```go
import (
	"os"
	"path/filepath"
)

func TestLoadARTCC(t *testing.T) {
	dir := t.TempDir()

	manifest := []byte(`{
		"airports": {
			"KLGA": {"csv": "KLGA-schedule.csv", "monthlyMultiplier": {"7": 1.25}},
			"KEWR": {"csv": "KEWR-schedule.csv"}
		}
	}`)
	if err := os.WriteFile(filepath.Join(dir, "schedules.json"), manifest, 0o644); err != nil {
		t.Fatal(err)
	}

	klga := []byte("day,bucket,dep,arr\nMON,07:00,28,22\nMON,07:15,28,8\nTUE,07:00,28,22\n")
	if err := os.WriteFile(filepath.Join(dir, "KLGA-schedule.csv"), klga, 0o644); err != nil {
		t.Fatal(err)
	}
	kewr := []byte("day,bucket,dep,arr\nWED,12:00,30,30\n")
	if err := os.WriteFile(filepath.Join(dir, "KEWR-schedule.csv"), kewr, 0o644); err != nil {
		t.Fatal(err)
	}

	sch, err := LoadARTCC(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sch == nil {
		t.Fatal("expected non-nil Schedule")
	}
	if len(sch.Airports) != 2 {
		t.Fatalf("want 2 airports, got %d", len(sch.Airports))
	}
	la := sch.Airports["KLGA"]
	if la == nil {
		t.Fatal("KLGA missing")
	}
	if la.MonthlyMultiplier[7] != 1.25 {
		t.Fatalf("KLGA July multiplier: %v", la.MonthlyMultiplier[7])
	}
	if got := la.Buckets["MON:07:15"]; got.Dep != 28 || got.Arr != 8 {
		t.Fatalf("KLGA MON 07:15: %+v", got)
	}
	if got := la.Buckets["TUE:07:00"]; got.Dep != 28 || got.Arr != 22 {
		t.Fatalf("KLGA TUE 07:00: %+v", got)
	}
	if got := sch.Airports["KEWR"].Buckets["WED:12:00"]; got.Dep != 30 || got.Arr != 30 {
		t.Fatalf("KEWR WED 12:00: %+v", got)
	}
}

func TestLoadARTCC_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	sch, err := LoadARTCC(dir, nil)
	if err != nil {
		t.Fatalf("expected nil error for missing manifest, got %v", err)
	}
	if sch != nil {
		t.Fatalf("expected nil Schedule for missing manifest, got %+v", sch)
	}
}
```

- [ ] **Step 2.2: Run test to fail**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v -test.run TestLoadARTCC
rm sched_test.exe
```
Expected: FAIL — `LoadARTCC` undefined.

- [ ] **Step 2.3: Implement `sim/schedule/loader.go`**

```go
// pkg/sim/schedule/loader.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package schedule

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/mmp/vice/log"
)

// LoadARTCC reads dir/schedules.json plus the referenced per-airport CSVs and
// returns the loaded Schedule. Returns (nil, nil) when schedules.json is
// absent (the ARTCC simply doesn't have schedule data — that's fine).
// Per-row CSV errors and per-airport CSV-missing errors are logged and the
// offending entry is skipped; only manifest-level parse errors fail loudly.
func LoadARTCC(dir string, lg *log.Logger) (*Schedule, error) {
	manifestPath := filepath.Join(dir, "schedules.json")
	mb, err := os.ReadFile(manifestPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %q: %w", manifestPath, err)
	}
	var m scheduleManifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, fmt.Errorf("parse %q: %w", manifestPath, err)
	}

	out := &Schedule{Airports: make(map[string]*AirportSchedule)}
	for icao, am := range m.Airports {
		csvPath := filepath.Join(dir, am.CSV)
		buckets, err := loadAirportCSV(csvPath)
		if err != nil {
			if lg != nil {
				lg.Warnf("schedule: %s: %v (skipping airport)", icao, err)
			}
			continue
		}
		mm := make(map[int]float32, len(am.MonthlyMultiplier))
		for k, v := range am.MonthlyMultiplier {
			n, err := strconv.Atoi(k)
			if err != nil || n < 1 || n > 12 {
				if lg != nil {
					lg.Warnf("schedule: %s: bad monthlyMultiplier key %q (skipping)", icao, k)
				}
				continue
			}
			mm[n] = v
		}
		out.Airports[icao] = &AirportSchedule{
			MonthlyMultiplier: mm,
			Buckets:           buckets,
		}
	}
	return out, nil
}

var validDays = map[string]bool{
	"MON": true, "TUE": true, "WED": true, "THU": true,
	"FRI": true, "SAT": true, "SUN": true,
}

func loadAirportCSV(path string) (map[string]Bucket, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // tolerate trailing-comma rows
	header, err := r.Read()
	if err != nil {
		return nil, fmt.Errorf("header: %w", err)
	}
	colIdx := map[string]int{}
	for i, h := range header {
		colIdx[h] = i
	}
	for _, want := range []string{"day", "bucket", "dep", "arr"} {
		if _, ok := colIdx[want]; !ok {
			return nil, fmt.Errorf("missing column %q in CSV header", want)
		}
	}

	out := make(map[string]Bucket)
	for row := 2; ; row++ {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("row %d: %w", row, err)
		}
		day := rec[colIdx["day"]]
		bucket := rec[colIdx["bucket"]]
		if !validDays[day] {
			continue
		}
		if !validBucketStr(bucket) {
			continue
		}
		dep, err := strconv.ParseFloat(rec[colIdx["dep"]], 32)
		if err != nil || dep < 0 {
			continue
		}
		arr, err := strconv.ParseFloat(rec[colIdx["arr"]], 32)
		if err != nil || arr < 0 {
			continue
		}
		out[day+":"+bucket] = Bucket{Dep: float32(dep), Arr: float32(arr)}
	}
	return out, nil
}

// validBucketStr accepts only "HH:MM" with MM in {00,15,30,45} and HH in
// 00..23.
func validBucketStr(s string) bool {
	if len(s) != 5 || s[2] != ':' {
		return false
	}
	hh, err := strconv.Atoi(s[:2])
	if err != nil || hh < 0 || hh > 23 {
		return false
	}
	mm, err := strconv.Atoi(s[3:])
	if err != nil {
		return false
	}
	return mm == 0 || mm == 15 || mm == 30 || mm == 45
}
```

- [ ] **Step 2.4: Run tests to pass**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v
rm sched_test.exe
```
Expected: all 3 tests pass.

- [ ] **Step 2.5: Commit**

```bash
git add sim/schedule/loader.go sim/schedule/loader_test.go
git commit -m "sim/schedule: LoadARTCC for schedules.json + per-airport CSVs

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: RateAt + HasAirport + Aggregate

End state: query helpers used both by the spawn engine and the histogram.

**Files:**
- Create: `sim/schedule/rate.go`
- Create: `sim/schedule/rate_test.go`

- [ ] **Step 3.1: Failing tests for rate lookups**

Create `sim/schedule/rate_test.go`:

```go
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
	if dep != 28*0.85 {
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
```

- [ ] **Step 3.2: Run tests to fail**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v -test.run "TestHas|TestRate|TestAggregate"
rm sched_test.exe
```
Expected: FAIL — undefined.

- [ ] **Step 3.3: Implement `sim/schedule/rate.go`**

```go
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
```

- [ ] **Step 3.4: Run tests to pass**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v
rm sched_test.exe
```
Expected: all tests pass.

- [ ] **Step 3.5: Commit**

```bash
git add sim/schedule/rate.go sim/schedule/rate_test.go
git commit -m "sim/schedule: RateAt, HasAirport, AggregateForDay + tests

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Wire Schedule into spawn engine

End state: the existing arrivals and departures spawn loops consult `RateAt` when `LaunchConfig.Schedule != nil`, otherwise behave exactly as today.

**Files:**
- Modify: `sim/spawn.go`
- Modify: `sim/spawn_arrivals.go`
- Modify: `sim/spawn_departures.go`

- [ ] **Step 4.1: Add the field on LaunchConfig**

In `sim/spawn.go`, find the `LaunchConfig` struct (around line 85). Add a field at the bottom:

```go
// Schedule, when non-nil, overrides the static rates above with
// per-15-min-bucket rates from authored CSVs. Skipped from JSON
// because it's a runtime-derived pointer.
Schedule *schedule.Schedule `json:"-"`
```

Add the import at the top of the file:

```go
"github.com/mmp/vice/sim/schedule"
```

(`sim/schedule` is a subpackage of `sim`, so this works without cycle.)

- [ ] **Step 4.2: Override departure rates when Schedule is set**

Find `sim/spawn_departures.go`. The Poisson rate per airport-runway-category is read from `s.State.LaunchConfig.DepartureRates[airport][runway][category]`. When `Schedule != nil`, override the *per-airport total* with `Schedule.RateAt(s.State.SimTime.Time(), airport).dep`, then distribute it across the airport's runways/categories using the *existing proportional weights* from `DepartureRates`.

The simplest place to plug this in is in the rate-accessor — but `DepartureRates` is read in many places. Instead, add a helper method on `LaunchConfig`:

```go
// effectiveDepartureRate returns the departure rate for the given
// airport/runway/category, consulting the schedule if set.
func (lc *LaunchConfig) effectiveDepartureRate(simTime time.Time, airport string, runway av.RunwayID, category string) float32 {
	staticRate := lc.DepartureRates[airport][runway][category]
	if lc.Schedule == nil || !lc.Schedule.HasAirport(airport) {
		return staticRate
	}
	scheduledTotal, _ := lc.Schedule.RateAt(simTime, airport)
	// Proportional distribution: scheduledTotal × (this slot's static
	// rate / sum of static rates for the airport).
	var staticTotal float32
	for _, runwayRates := range lc.DepartureRates[airport] {
		for _, r := range runwayRates {
			staticTotal += r
		}
	}
	if staticTotal == 0 {
		return 0
	}
	return scheduledTotal * (staticRate / staticTotal)
}
```

Place this method in `sim/spawn.go` next to the other accessors (around line 159). Now find every read of `lc.DepartureRates[airport][runway][category]` and replace with `lc.effectiveDepartureRate(s.State.SimTime.Time(), airport, runway, category)` — but only inside `spawn_departures.go` (the actual spawn loop). Don't touch the launch-control UI which displays the static rates.

(Note: this is several call-site replacements. Implementer should grep `lc.DepartureRates\[` inside `sim/spawn_departures.go` and update each lookup.)

- [ ] **Step 4.3: Override arrival rates when Schedule is set**

Same pattern in `sim/spawn_arrivals.go`. Arrivals are accessed via `lc.InboundFlowRates[flowName][airport]`. Add the method:

```go
// effectiveInboundFlowRate returns the per-(flow,airport) arrival rate,
// consulting the schedule if set.
func (lc *LaunchConfig) effectiveInboundFlowRate(simTime time.Time, flow, airport string) float32 {
	staticRate := lc.InboundFlowRates[flow][airport]
	if lc.Schedule == nil || !lc.Schedule.HasAirport(airport) {
		return staticRate
	}
	_, scheduledTotal := lc.Schedule.RateAt(simTime, airport)
	// Proportional distribution across all flows that feed this airport.
	var staticTotal float32
	for _, flowRates := range lc.InboundFlowRates {
		staticTotal += flowRates[airport]
	}
	if staticTotal == 0 {
		return 0
	}
	return scheduledTotal * (staticRate / staticTotal)
}
```

Replace `lc.InboundFlowRates[flow][airport]` reads inside `sim/spawn_arrivals.go` with `lc.effectiveInboundFlowRate(s.State.SimTime.Time(), flow, airport)`.

- [ ] **Step 4.4: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 4.5: Run sim tests**

```
go test -c -tags vulkan -o sim_test.exe ./sim/
./sim_test.exe -test.v -test.short
rm sim_test.exe
```
Expected: existing tests still pass (the schedule path is opt-in; with `Schedule == nil`, behavior is identical).

- [ ] **Step 4.6: Commit**

```bash
git add sim/spawn.go sim/spawn_arrivals.go sim/spawn_departures.go
git commit -m "sim: schedule-driven IFR rates when LaunchConfig.Schedule is set

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Wire Schedule into NewSimConfiguration + scenario loader

End state: when an ARTCC has a `schedules.json`, the loaded `*Schedule` is exposed on the configuration screen so the UI can decide whether to enable the checkbox.

**Files:**
- Modify: `cmd/vice/simconfig.go`
- Modify: `server/scenario.go` (or wherever scenarios load — verify the location)

- [ ] **Step 5.1: Surface Schedule on NewSimConfiguration**

In `cmd/vice/simconfig.go` find `type NewSimConfiguration struct {` (around line 34). Add fields at the bottom:

```go
// Schedule is the loaded ARTCC schedule for the current facility (nil if
// the ARTCC has no schedules.json or the file failed to load). Used by
// the configuration UI to enable the "Use real-world schedule" checkbox
// and drive the histogram.
Schedule *schedule.Schedule

// UseSchedule is the user's checkbox state. Only meaningful when
// Schedule != nil and at least one of the scenario's airports is in it.
UseSchedule bool

// SchedulePicked is the (month, day-of-week, hour:minute) the user
// chose. Used to compute StartTime when the sim launches.
SchedulePickedMonth   time.Month
SchedulePickedDay     time.Weekday
SchedulePickedMinutes int // minutes-of-day, 0..1425 in 15-min steps
```

Add the import: `"github.com/mmp/vice/sim/schedule"`.

- [ ] **Step 5.2: Load the ARTCC schedule when scenarios are read**

Vice loads scenarios via `server.LoadScenarioGroups` — find it. After scenario groups are loaded, walk the scenario directories (each ARTCC has a folder under `resources/configurations/`) and for each ARTCC call `schedule.LoadARTCC(<artccDir>, lg)`. Cache results on the `ConnectionManager` or wherever `NewSimConfiguration` reads from.

Concretely, the simplest hook: when `MakeNewSimConfiguration(mgr, &lastTRACON, lg)` runs in `simconfig.go:241`, after the scenario data is in hand, look up the ARTCC for the selected facility and call `LoadARTCC`. Store the result on `c.Schedule`. Re-run the load when the user changes facility.

Since `MakeNewSimConfiguration` may not have the ARTCC dir handy directly, the cleanest plumbing is:

- In `MakeNewSimConfiguration`, after the scenario group is selected, derive the ARTCC directory from `resources/configurations/<ARTCC>/` and call `schedule.LoadARTCC(dir, lg)`.
- Cache one `*Schedule` per ARTCC name in a package-level map so we don't re-read the files every time the user toggles facility.

**Important — verify directory resolution:** vice may not directly know the on-disk path of the loaded ARTCC because resources can be embedded. Check `cmd/vice/resources_local.go` and `cmd/vice/resources_download.go` to see how scenarios are sourced. If they're embedded, the schedule files need to be embedded too — in which case `LoadARTCC` should take a `fs.FS` instead of a string path. Adapt accordingly.

If the resource path is a real filesystem directory at runtime (e.g., `os.UserConfigDir()/vice/configurations/<ARTCC>/`), `LoadARTCC` works as-is.

- [ ] **Step 5.3: Sane defaults for the picker**

In `MakeNewSimConfiguration` (or right after it returns), default the picker fields to "now":

```go
now := time.Now()
c.SchedulePickedMonth = now.Month()
c.SchedulePickedDay = now.Weekday()
mins := now.Hour()*60 + now.Minute()
c.SchedulePickedMinutes = (mins / 15) * 15
```

- [ ] **Step 5.4: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 5.5: Commit**

```bash
git add cmd/vice/simconfig.go server/scenario.go
git commit -m "cmd/vice,server: surface ARTCC Schedule on NewSimConfiguration

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Histogram drawer

End state: a self-contained drawer that renders 96 stacked bars (arrivals bottom, departures top) with hover tooltip showing per-airport breakdown.

**Files:**
- Create: `cmd/vice/schedhist.go`

- [ ] **Step 6.1: Create `cmd/vice/schedhist.go`**

```go
// cmd/vice/schedhist.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmp/vice/sim/schedule"

	"github.com/AllenDang/cimgui-go/imgui"
)

// drawScheduleHistogram renders 96 stacked 15-min bars for the picked
// (month, weekday) summed across `airports`. Bottom slice = arrivals,
// top = departures. Hovering a bar shows a per-airport breakdown tooltip.
// Returns the bucket index 0..95 the user clicked, or -1 if no click.
func drawScheduleHistogram(sch *schedule.Schedule, month time.Month, day time.Weekday,
	airports []string, width, height float32) int {

	if sch == nil || len(airports) == 0 {
		imgui.Dummy(imgui.Vec2{X: width, Y: height})
		return -1
	}

	// Build a synthetic date with the given month + weekday so AggregateForDay
	// (which uses time.Date) walks the right buckets.
	year := time.Now().Year()
	candidate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	for candidate.Weekday() != day {
		candidate = candidate.AddDate(0, 0, 1)
	}
	totals := sch.AggregateForDay(candidate, airports)

	var maxH float32
	for _, b := range totals {
		if h := b.Dep + b.Arr; h > maxH {
			maxH = h
		}
	}
	if maxH < 1 {
		maxH = 1
	}

	// Reserve canvas.
	pos := imgui.CursorScreenPos()
	imgui.InvisibleButtonV("##schedhist", imgui.Vec2{X: width, Y: height}, imgui.ButtonFlagsMouseButtonLeft)
	hovered := imgui.IsItemHovered()
	clicked := hovered && imgui.IsMouseClickedBool(imgui.MouseButtonLeft)
	dl := imgui.WindowDrawList()

	// Background.
	dl.AddRectFilled(pos, imgui.Vec2{X: pos.X + width, Y: pos.Y + height},
		imgui.ColorU32Vec4(imgui.Vec4{X: 0.10, Y: 0.10, Z: 0.12, W: 1}))

	depColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.95, Y: 0.65, Z: 0.30, W: 1}) // departures = orange
	arrColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.30, Y: 0.65, Z: 0.95, W: 1}) // arrivals = blue

	barW := width / 96.0
	mouse := imgui.MousePos()
	hoverIdx := -1
	if hovered {
		hoverIdx = int((mouse.X - pos.X) / barW)
		if hoverIdx < 0 || hoverIdx >= 96 {
			hoverIdx = -1
		}
	}

	for i, b := range totals {
		x0 := pos.X + float32(i)*barW
		x1 := x0 + barW - 1
		// Arrivals: stacked at the bottom.
		hArr := height * (b.Arr / maxH)
		hDep := height * (b.Dep / maxH)
		dl.AddRectFilled(
			imgui.Vec2{X: x0, Y: pos.Y + height - hArr},
			imgui.Vec2{X: x1, Y: pos.Y + height},
			arrColor)
		dl.AddRectFilled(
			imgui.Vec2{X: x0, Y: pos.Y + height - hArr - hDep},
			imgui.Vec2{X: x1, Y: pos.Y + height - hArr},
			depColor)
	}

	if hoverIdx >= 0 {
		// Tooltip: bucket label + per-airport breakdown.
		bucketTime := time.Date(candidate.Year(), candidate.Month(), candidate.Day(),
			hoverIdx/4, (hoverIdx%4)*15, 0, 0, time.UTC)
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %02d:%02d", weekdayShort(day), hoverIdx/4, (hoverIdx%4)*15)
		sb.WriteString("\n")
		for _, icao := range airports {
			dep, arr := sch.RateAt(bucketTime, icao)
			if dep == 0 && arr == 0 {
				continue
			}
			fmt.Fprintf(&sb, "%s  arr %d  dep %d\n", icao, int(arr+0.5), int(dep+0.5))
		}
		imgui.SetTooltip(sb.String())
	}

	if clicked && hoverIdx >= 0 {
		return hoverIdx
	}
	return -1
}

func weekdayShort(d time.Weekday) string {
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
```

- [ ] **Step 6.2: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 6.3: Commit**

```bash
git add cmd/vice/schedhist.go
git commit -m "cmd/vice: schedule histogram drawer (stacked bars + per-airport tooltip)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Configuration screen UI

End state: the "Use real-world schedule" checkbox + month/day/time sliders + histogram appear on the scenario configuration screen. Disabled-checkbox-with-tooltip when the scenario has no eligible airports.

**Files:**
- Modify: `cmd/vice/simconfig.go`

- [ ] **Step 7.1: Add UI block at the end of `DrawConfigurationUI`**

Find `func (c *NewSimConfiguration) DrawConfigurationUI(p platform.Platform, config *Config) bool` (around line 1253). Walk to the bottom of the function (just before the final `return ...`). Add a separator and the schedule block:

```go
imgui.Separator()

// Determine which of this scenario's airports are present in the
// loaded schedule. The checkbox is enabled iff at least one is.
scenarioAirports := c.scenarioAirportICAOs() // helper added in 7.2
var eligible []string
for _, icao := range scenarioAirports {
	if c.Schedule.HasAirport(icao) {
		eligible = append(eligible, icao)
	}
}
disabled := len(eligible) == 0
if disabled {
	imgui.BeginDisabled()
}
imgui.Checkbox("Use real-world schedule", &c.UseSchedule)
if disabled {
	imgui.EndDisabled()
	c.UseSchedule = false
	if imgui.IsItemHovered() {
		imgui.SetTooltip("No schedules specified for this scenario")
	}
}

if c.UseSchedule {
	// Month slider 1..12
	mo := int32(c.SchedulePickedMonth)
	if imgui.SliderInt("Month", &mo, 1, 12) {
		c.SchedulePickedMonth = time.Month(mo)
	}
	// Day slider: 1=Mon..7=Sun
	dayIdx := int32(c.SchedulePickedDay)
	if dayIdx == int32(time.Sunday) {
		dayIdx = 7
	}
	if imgui.SliderInt("Day (1=Mon..7=Sun)", &dayIdx, 1, 7) {
		if dayIdx == 7 {
			c.SchedulePickedDay = time.Sunday
		} else {
			c.SchedulePickedDay = time.Weekday(dayIdx)
		}
	}
	// Time slider: 0..1425 in 15-min steps
	mins := int32(c.SchedulePickedMinutes)
	if imgui.SliderIntV("Time (15-min steps)", &mins, 0, 1425, fmt.Sprintf("%02d:%02d", mins/60, mins%60), 0) {
		mins = (mins / 15) * 15
		c.SchedulePickedMinutes = int(mins)
	}

	// Histogram (stacked bars, 96 buckets), 600px wide × 80px tall.
	if hit := drawScheduleHistogram(c.Schedule, c.SchedulePickedMonth, c.SchedulePickedDay,
		eligible, 600, 80); hit >= 0 {
		c.SchedulePickedMinutes = hit * 15
	}
}
```

Add `"fmt"` and `"time"` to the imports of `simconfig.go` if missing.

- [ ] **Step 7.2: Add `scenarioAirportICAOs` helper**

Add to `cmd/vice/simconfig.go`:

```go
// scenarioAirportICAOs returns the list of airport ICAOs the currently
// selected scenario operates on (departures + arrivals).
func (c *NewSimConfiguration) scenarioAirportICAOs() []string {
	seen := map[string]struct{}{}
	for ap := range c.ScenarioSpec.LaunchConfig.DepartureRates {
		seen[ap] = struct{}{}
	}
	for _, flowRates := range c.ScenarioSpec.LaunchConfig.InboundFlowRates {
		for ap := range flowRates {
			seen[ap] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for ap := range seen {
		out = append(out, ap)
	}
	return out
}
```

(If `c.ScenarioSpec` isn't the right field name, grep `simconfig.go` for whichever field holds the selected scenario's `LaunchConfig`. The name may be slightly different.)

- [ ] **Step 7.3: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 7.4: Commit**

```bash
git add cmd/vice/simconfig.go
git commit -m "cmd/vice: schedule picker UI (checkbox + month/day/time + histogram)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Override StartTime + plumb Schedule into LaunchConfig at sim start

End state: when `UseSchedule` is on, the sim launches at the picked moment and the LaunchConfig carries the Schedule pointer.

**Files:**
- Modify: `cmd/vice/simconfig.go`

- [ ] **Step 8.1: Compute concrete StartTime + attach Schedule**

In `cmd/vice/simconfig.go`, find where `c.StartTime` is set (around line 2304). After the existing METAR-based assignment, add:

```go
if c.UseSchedule && c.Schedule != nil {
	c.StartTime = c.scheduleStartTime()
	c.ScenarioSpec.LaunchConfig.Schedule = c.Schedule
}
```

Add the `scheduleStartTime` helper:

```go
// scheduleStartTime returns a concrete date/time matching the user's
// picked (month, weekday, time-of-day). Picks the most recent occurrence
// in the current calendar year, or the next-future one if "current" is
// before the year started.
func (c *NewSimConfiguration) scheduleStartTime() time.Time {
	year := time.Now().Year()
	hh := c.SchedulePickedMinutes / 60
	mm := c.SchedulePickedMinutes % 60
	// Walk back from year's end to find the most recent matching weekday.
	for d := time.Date(year, c.SchedulePickedMonth, 28, hh, mm, 0, 0, time.UTC); d.Month() == c.SchedulePickedMonth; d = d.AddDate(0, 0, -1) {
		if d.Weekday() == c.SchedulePickedDay {
			return d
		}
	}
	// Fallback: first day of the picked month even if the weekday doesn't
	// land — shouldn't happen since every weekday lands in every month.
	return time.Date(year, c.SchedulePickedMonth, 1, hh, mm, 0, 0, time.UTC)
}
```

- [ ] **Step 8.2: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 8.3: Commit**

```bash
git add cmd/vice/simconfig.go
git commit -m "cmd/vice: override StartTime + attach Schedule when picker is used

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Sample data for ZNY/KLGA

End state: a small but realistic schedule for one airport so the feature is testable end to end.

**Files:**
- Create: `resources/configurations/ZNY/schedules.json`
- Create: `resources/configurations/ZNY/KLGA-schedule.csv`

- [ ] **Step 9.1: Create the manifest**

```bash
mkdir -p resources/configurations/ZNY
```

Create `resources/configurations/ZNY/schedules.json`:

```json
{
  "airports": {
    "KLGA": {
      "csv": "KLGA-schedule.csv",
      "monthlyMultiplier": {
        "1": 0.90, "2": 0.92, "3": 1.00, "4": 1.05,
        "5": 1.10, "6": 1.20, "7": 1.25, "8": 1.20,
        "9": 1.05, "10": 1.00, "11": 1.05, "12": 0.85
      }
    }
  }
}
```

- [ ] **Step 9.2: Create the CSV**

Create `resources/configurations/ZNY/KLGA-schedule.csv` with weekday and weekend rows. This is a coarse first cut — real data can replace it later. Provide the full Monday day in 15-min increments (96 rows), then alias-style: every other weekday duplicates Monday's data; SAT and SUN have different lower-volume data.

```csv
day,bucket,dep,arr
MON,00:00,0,0
MON,00:15,0,0
MON,00:30,0,0
MON,00:45,0,0
MON,01:00,0,0
MON,01:15,0,0
MON,01:30,0,0
MON,01:45,0,0
MON,02:00,0,0
MON,02:15,0,0
MON,02:30,0,0
MON,02:45,0,0
MON,03:00,0,0
MON,03:15,0,0
MON,03:30,0,0
MON,03:45,0,0
MON,04:00,0,0
MON,04:15,0,0
MON,04:30,0,0
MON,04:45,0,0
MON,05:00,2,1
MON,05:15,4,2
MON,05:30,4,3
MON,05:45,6,4
MON,06:00,12,6
MON,06:15,18,10
MON,06:30,18,14
MON,06:45,18,16
MON,07:00,28,22
MON,07:15,28,24
MON,07:30,26,26
MON,07:45,26,26
MON,08:00,30,28
MON,08:15,30,28
MON,08:30,28,28
MON,08:45,28,28
MON,09:00,26,28
MON,09:15,26,28
MON,09:30,24,28
MON,09:45,22,28
MON,10:00,22,28
MON,10:15,22,28
MON,10:30,22,28
MON,10:45,22,28
MON,11:00,22,30
MON,11:15,22,30
MON,11:30,22,30
MON,11:45,22,30
MON,12:00,24,30
MON,12:15,24,30
MON,12:30,26,30
MON,12:45,26,30
MON,13:00,26,30
MON,13:15,26,30
MON,13:30,28,30
MON,13:45,28,30
MON,14:00,28,30
MON,14:15,30,30
MON,14:30,30,28
MON,14:45,30,28
MON,15:00,30,28
MON,15:15,32,28
MON,15:30,32,26
MON,15:45,32,26
MON,16:00,32,26
MON,16:15,30,26
MON,16:30,30,26
MON,16:45,30,28
MON,17:00,30,28
MON,17:15,28,28
MON,17:30,26,28
MON,17:45,26,28
MON,18:00,26,28
MON,18:15,24,26
MON,18:30,22,26
MON,18:45,22,26
MON,19:00,22,26
MON,19:15,18,24
MON,19:30,18,22
MON,19:45,18,22
MON,20:00,18,22
MON,20:15,16,20
MON,20:30,14,18
MON,20:45,12,16
MON,21:00,12,16
MON,21:15,10,14
MON,21:30,8,12
MON,21:45,6,10
MON,22:00,6,8
MON,22:15,4,6
MON,22:30,4,4
MON,22:45,2,4
MON,23:00,2,4
MON,23:15,2,2
MON,23:30,0,2
MON,23:45,0,0
```

Add the same rows for TUE, WED, THU, FRI by copying the Monday block. Add a quieter SAT/SUN — maybe 60% rates, peak shifted to 0930-1130. The implementer can use a script or a spreadsheet to drag-fill this; the exact numbers are placeholders.

A complete weekday + reduced-weekend file is too long to inline here, so include the Monday block above and document the rest as authoring work the implementer (or future user) does. The minimum viable test data is just the Monday block — the rest can be empty rows (treated as zero rate) and the system still works.

**Decision: only include MON in the sample CSV.** The other days will be zero-rate buckets, which is fine for v1 testing. Real users will author the rest.

- [ ] **Step 9.3: Verify the CSV loads**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v
rm sched_test.exe
```
Expected: existing tests still pass (this step doesn't add tests; the sample data isn't tested directly).

Then a quick smoke load — write a tiny Go test in `sim/schedule/loader_test.go`:

```go
func TestLoadZNYSample(t *testing.T) {
	dir := "../../resources/configurations/ZNY"
	if _, err := os.Stat(filepath.Join(dir, "schedules.json")); os.IsNotExist(err) {
		t.Skip("ZNY schedules.json not present")
	}
	sch, err := LoadARTCC(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !sch.HasAirport("KLGA") {
		t.Fatal("KLGA missing from sample data")
	}
	mon0700 := time.Date(2026, 5, 4, 7, 0, 0, 0, time.UTC) // Monday
	dep, arr := sch.RateAt(mon0700, "KLGA")
	if dep < 20 || arr < 18 {
		t.Fatalf("MON 07:00 KLGA rates look wrong: dep=%v arr=%v", dep, arr)
	}
}
```

Run: `go test -c -tags vulkan -o sched_test.exe ./sim/schedule/ && ./sched_test.exe -test.v && rm sched_test.exe`. Expected: all tests pass including `TestLoadZNYSample`.

- [ ] **Step 9.4: Commit**

```bash
git add resources/configurations/ZNY/schedules.json resources/configurations/ZNY/KLGA-schedule.csv sim/schedule/loader_test.go
git commit -m "resources/ZNY: sample schedules.json + KLGA-schedule.csv

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Final pass + push

- [ ] **Step 10.1: Full test sweep**

```
go test -c -tags vulkan -o sched_test.exe ./sim/schedule/
./sched_test.exe -test.v
rm sched_test.exe
go test -c -tags vulkan -o sim_test.exe ./sim/
./sim_test.exe -test.short
rm sim_test.exe
go build -tags vulkan ./cmd/vice
```
Expected: all green.

- [ ] **Step 10.2: Manual smoke**

- [ ] Launch vice. Pick an N90 scenario covering KLGA. The configuration screen shows "Use real-world schedule" enabled.
- [ ] Toggle on. Sliders + histogram appear. Histogram shows the Monday push at KLGA (peaking 0700–1500).
- [ ] Pick Tue 0700, July, click Connect. Sim launches. The IFR rate ramps as authored. Disconnecting at sim-time 0900 should match the histogram's 0900 bar.
- [ ] Pick a scenario covering an airport NOT in `schedules.json` (e.g., a PHL scenario). Checkbox is disabled with tooltip "No schedules specified for this scenario."
- [ ] Toggle off. Scenario reverts to constant rates per existing LaunchConfig.

- [ ] **Step 10.3: Push**

```bash
git push -u origin schedule-traffic
```

- [ ] **Step 10.4: Save memory note**

Path: `C:\Users\judlo\.claude\projects\C--Users-judlo-Documents-vice-vice\memory\schedule_traffic_branch.md`

```markdown
---
name: Schedule-traffic branch state
description: schedule-traffic @<sha> — opt-in per-airport 15-min-bucket schedule files (CSV) drive IFR spawn rates; connect-dialog checkbox + month/day/time picker + 96-bar stacked histogram. Sim starts at picked moment. Local-only (not for upstream PR).
type: project
---

`schedule-traffic` @<sha> (pushed to origin). Branched fresh from upstream/master @ 6aedbfbc.

What's in it:
- `sim/schedule/` package: Schedule + AirportSchedule types, LoadARTCC reading schedules.json + per-airport CSVs, RateAt, AggregateForDay.
- LaunchConfig.Schedule field; effective{Departure,InboundFlow}Rate methods that override static rates with proportional-distribution-of-scheduled-totals when Schedule is set.
- NewSimConfiguration carries Schedule + UseSchedule + picker fields. UI on configuration screen: checkbox + month/day/time sliders + drawScheduleHistogram (96 stacked bars, hover tooltip per-airport).
- StartTime override at sim launch when UseSchedule is on.
- Sample data: resources/configurations/ZNY/{schedules.json, KLGA-schedule.csv} (Monday only; other days zero-rate).

Why: user wanted real-world-shaped IFR traffic and ability to pick a "shift to work."

How to apply: spec at docs/superpowers/specs/2026-04-30-real-world-schedule-design.md, plan at docs/superpowers/plans/2026-04-30-real-world-schedule.md.

v2 ideas (in spec):
- Per-month curve-shape changes (per-season CSVs).
- Holiday overrides.
- cmd/eats2schedule importer that reads ATSim2020/eATS flight-plan templates and emits our CSVs.
```

Append to `MEMORY.md`:

```markdown
- [Schedule-traffic branch state](schedule_traffic_branch.md) — `schedule-traffic` @<sha> (pushed) — opt-in real-world-schedule-driven IFR spawn rates with picker + histogram.
```

---

## Self-review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| File layout (`schedules.json` + per-airport CSVs per ARTCC) | Tasks 1, 2, 9 |
| 15-min bucket CSV schema | Task 2 (CSV reader) |
| MonthlyMultiplier | Tasks 1, 3 |
| LoadARTCC behavior (missing manifest → nil; per-airport errors logged + skipped) | Task 2 |
| RateAt + HasAirport + AggregateForDay | Task 3 |
| Schedule overrides static rates in arrivals/departures | Task 4 |
| NewSimConfiguration carries Schedule + picker fields | Task 5 |
| Configuration UI: checkbox (disabled tooltip), sliders, histogram | Tasks 6, 7 |
| StartTime override at sim launch | Task 8 |
| Sample data | Task 9 |
| Manual + unit tests | Tasks 1–3 (unit), 10 (manual) |

**Placeholder scan:** No "TBD"/"TODO". Two items flagged as engineer-verifies (resource path resolution in Task 5 — could be embedded vs filesystem; field-name match in Task 7's `c.ScenarioSpec`). Both have explicit verification instructions.

**Type consistency:** `*Schedule`, `*AirportSchedule`, `Bucket` are used consistently. `Schedule.Airports` is a `map[string]*AirportSchedule`. Picker fields (`SchedulePickedMonth time.Month`, `SchedulePickedDay time.Weekday`, `SchedulePickedMinutes int`) are used consistently across Tasks 5, 7, 8.

**Scope:** single feature, single branch, single plan.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-30-real-world-schedule.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
