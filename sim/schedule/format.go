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
