// pkg/sim/schedule/loader_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package schedule

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
