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
