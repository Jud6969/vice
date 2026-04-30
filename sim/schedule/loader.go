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
