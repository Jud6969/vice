// pkg/panes/mappane_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"
	"testing"

	"github.com/mmp/vice/math"
	"github.com/mmp/vice/sim"
)

func TestCameraTransformsRoundtrip(t *testing.T) {
	cam := camera{center: math.Point2LL{-73.78, 40.64}, rangeNM: 50} // KJFK
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: [2]float32{800, 600}}
	xforms := cam.transforms(extent, float32(1.0/gomath.Cos(40.64*gomath.Pi/180.0)*60.0))

	in := math.Point2LL{-73.78, 40.64}
	screen := xforms.WindowFromLatLongP(in)
	back := xforms.LatLongFromWindowP(screen)
	if gomath.Abs(float64(back[0]-in[0])) > 1e-3 || gomath.Abs(float64(back[1]-in[1])) > 1e-3 {
		t.Fatalf("roundtrip drift: in=%v back=%v", in, back)
	}
}

func TestCameraScreenRoundtrip(t *testing.T) {
	cam := camera{center: math.Point2LL{-73.78, 40.64}, rangeNM: 50}
	origin := [2]float32{100, 50}
	size := [2]float32{800, 600}
	const nmPerLon = 45.5

	in := math.Point2LL{-73.5, 40.7}
	screen := cam.llToScreen(in, origin, size, nmPerLon)
	back := cam.screenToLL(screen, origin, size, nmPerLon)
	if gomath.Abs(float64(back[0]-in[0])) > 1e-3 || gomath.Abs(float64(back[1]-in[1])) > 1e-3 {
		t.Fatalf("screen roundtrip drift: in=%v back=%v", in, back)
	}
	centerScreen := cam.llToScreen(cam.center, origin, size, nmPerLon)
	wantX := origin[0] + size[0]/2
	wantY := origin[1] + size[1]/2
	if gomath.Abs(float64(centerScreen[0]-wantX)) > 1 || gomath.Abs(float64(centerScreen[1]-wantY)) > 1 {
		t.Fatalf("center not at canvas center: got=%v want=(%v,%v)", centerScreen, wantX, wantY)
	}
}

func TestCameraApplyZoomFactorClamps(t *testing.T) {
	cam := camera{center: math.Point2LL{0, 0}, rangeNM: 50}
	cam.applyZoomFactor(0.0001) // would go below minimum
	if cam.rangeNM != minRangeNM {
		t.Fatalf("expected clamp to minRangeNM, got %v", cam.rangeNM)
	}
	cam.applyZoomFactor(1e9) // would explode above max
	if cam.rangeNM != maxRangeNM {
		t.Fatalf("expected clamp to maxRangeNM, got %v", cam.rangeNM)
	}
}

func TestCameraApplyPanPixels(t *testing.T) {
	// Panning right by half the canvas width should shift center by -1*range_in_lon.
	cam := camera{center: math.Point2LL{0, 0}, rangeNM: 60}
	size := [2]float32{800, 600}
	const nmPerLon = 60.0
	startLL := cam.center
	cam.applyPanPixels(400, 0, size, nmPerLon)
	if cam.center[0] >= startLL[0] {
		t.Fatalf("expected longitude to decrease after rightward drag, before=%v after=%v", startLL, cam.center)
	}
	// Vertical: drag downward → camera center moves north (lat increases) in imgui frame.
	cam2 := camera{center: math.Point2LL{0, 0}, rangeNM: 60}
	cam2.applyPanPixels(0, 300, size, nmPerLon)
	if cam2.center[1] <= 0 {
		t.Fatalf("expected lat to increase after downward drag, got %v", cam2.center[1])
	}
}

func TestParseGeoJSONLineStrings(t *testing.T) {
	// Minimal GeoJSON covering all four geometry types.
	src := []byte(`{
		"type": "FeatureCollection",
		"features": [
			{"type":"Feature","geometry":{"type":"LineString","coordinates":[[0,0],[1,1],[2,0]]}},
			{"type":"Feature","geometry":{"type":"MultiLineString","coordinates":[[[10,10],[11,11]],[[20,20],[21,21],[22,22]]]}},
			{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[5,5],[6,5],[6,6],[5,6],[5,5]]]}},
			{"type":"Feature","geometry":{"type":"MultiPolygon","coordinates":[[[[30,30],[31,30],[31,31],[30,30]]]]}}
		]
	}`)
	pls, err := parseGeoJSONPolylines(src)
	if err != nil {
		t.Fatal(err)
	}
	// 1 LineString + 2 sub-strings of MultiLineString + 1 polygon ring + 1 multipolygon ring = 5 polylines.
	if len(pls) != 5 {
		t.Fatalf("want 5 polylines, got %d", len(pls))
	}
	if len(pls[0].pts) != 3 || pls[0].pts[1][0] != 1 || pls[0].pts[1][1] != 1 {
		t.Fatalf("first polyline malformed: %+v", pls[0])
	}
	// Bounding box for second polyline (MultiLineString[0]).
	if pls[1].bounds.P0[0] != 10 || pls[1].bounds.P1[0] != 11 {
		t.Fatalf("second polyline bounds wrong: %+v", pls[1].bounds)
	}
	// Bounding box for fifth polyline (MultiPolygon[0][0]).
	if pls[4].bounds.P0[0] != 30 || pls[4].bounds.P1[0] != 31 ||
		pls[4].bounds.P0[1] != 30 || pls[4].bounds.P1[1] != 31 {
		t.Fatalf("multipolygon bounds wrong: %+v", pls[4].bounds)
	}
}

func TestFilterMatch(t *testing.T) {
	mkTrack := func(owner string) *sim.Track {
		if owner == "" {
			return &sim.Track{}
		}
		return &sim.Track{FlightPlan: &sim.NASFlightPlan{OwningTCW: sim.TCW(owner)}}
	}
	// Note: filterTCW with FilterTCWFilter=="" matches nothing — the UI
	// (Task 7) is responsible for setting FilterTCWFilter before allowing
	// Filter == filterTCW.
	cases := []struct {
		name      string
		trk       *sim.Track
		filter    aircraftFilter
		userTCW   sim.TCW
		tcwFilter string
		want      bool
	}{
		{"all-untracked", mkTrack(""), filterAll, "", "", true},
		{"all-tracked", mkTrack("ABC"), filterAll, "", "", true},
		{"untracked-pass", mkTrack(""), filterUntracked, "", "", true},
		{"untracked-block", mkTrack("ABC"), filterUntracked, "", "", false},
		{"tracked-pass", mkTrack("ABC"), filterTracked, "", "", true},
		{"tracked-block", mkTrack(""), filterTracked, "", "", false},
		{"mine-pass", mkTrack("USR"), filterMyTCW, "USR", "", true},
		{"mine-block-other", mkTrack("OTH"), filterMyTCW, "USR", "", false},
		{"mine-block-untracked", mkTrack(""), filterMyTCW, "USR", "", false},
		{"specific-pass", mkTrack("XYZ"), filterTCW, "USR", "XYZ", true},
		{"specific-block", mkTrack("ABC"), filterTCW, "USR", "XYZ", false},
		{"specific-empty-filter", mkTrack("XYZ"), filterTCW, "USR", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := filterMatch(tc.trk, tc.filter, tc.userTCW, tc.tcwFilter)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
