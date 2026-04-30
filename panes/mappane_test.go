// pkg/panes/mappane_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"
	"testing"

	"github.com/mmp/vice/math"
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
