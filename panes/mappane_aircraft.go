// pkg/panes/mappane_aircraft.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/mmp/vice/sim"
)

type aircraftFilter int

const (
	filterAll aircraftFilter = iota
	filterUntracked
	filterTracked
	filterMyTCW
	filterTCW
)

// filterMatch returns true iff the track passes the current filter.
func filterMatch(trk *sim.Track, f aircraftFilter, userTCW sim.TCW, filterTCWFilter string) bool {
	// A track with a flight plan but no owning TCW (pre-coordination) is
	// treated as untracked for filter purposes.
	owned := trk.FlightPlan != nil && trk.FlightPlan.OwningTCW != ""
	switch f {
	case filterAll:
		return true
	case filterUntracked:
		return !owned
	case filterTracked:
		return owned
	case filterMyTCW:
		return owned && trk.FlightPlan.OwningTCW == userTCW
	case filterTCW:
		return owned && string(trk.FlightPlan.OwningTCW) == filterTCWFilter
	}
	return true
}

func (mp *MapPane) drawAircraft(src TrackSource, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !src.Connected() {
		return
	}
	view := mp.viewExtent(cam, canvasSize, nmPerLongitude)

	for cs, trk := range src.Tracks() {
		if !filterMatch(trk, aircraftFilter(mp.Filter), src.UserTCW(), mp.FilterTCWFilter) {
			continue
		}
		loc := trk.Location
		if loc[0] < view.P0[0] || loc[0] > view.P1[0] || loc[1] < view.P0[1] || loc[1] > view.P1[1] {
			continue
		}
		s := cam.llToScreen(loc, canvasOrigin, canvasSize, nmPerLongitude)
		owned := trk.FlightPlan != nil && trk.FlightPlan.OwningTCW != ""
		alpha := float32(0.55)
		if owned {
			alpha = 1.0
		}
		col := imgui.Vec4{X: 0.95, Y: 0.95, Z: 0.95, W: alpha}
		colU32 := imgui.ColorU32Vec4(col)

		drawAircraftTriangle(mp.canvasDrawList, s, float32(trk.Heading), colU32)

		if cs == mp.selectedCS {
			ring := imgui.ColorU32Vec4(imgui.Vec4{X: 0.55, Y: 0.85, Z: 1.0, W: 1.0})
			mp.canvasDrawList.AddCircle(imgui.Vec2{X: s[0], Y: s[1]}, 11, ring)
		} else if cs == mp.hoveredCS {
			// Yellow halo on hover (separate from the cyan selection ring so
			// the user can see both the current and the prospective selection).
			ring := imgui.ColorU32Vec4(imgui.Vec4{X: 1.0, Y: 0.85, Z: 0.30, W: 0.95})
			mp.canvasDrawList.AddCircle(imgui.Vec2{X: s[0], Y: s[1]}, 11, ring)
		}

		// Offset 9px right (1px past the triangle nose at +8) and 7px up,
		// placing the label flush with the top of the glyph.
		// Callsign label
		labelPos := imgui.Vec2{X: s[0] + 9, Y: s[1] - 7}
		mp.canvasDrawList.AddTextVec2(labelPos, colU32, string(cs))
	}
}

// findHoveredAircraft updates mp.hoveredCS to the closest visible aircraft
// within aircraftHitRadiusPx of the cursor, or "" if nothing is close. Must be
// called after the camera has been finalized for the frame; the canvasHovered
// flag short-circuits when the cursor isn't over the canvas.
func (mp *MapPane) findHoveredAircraft(src TrackSource, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32, canvasHovered bool) {
	mp.hoveredCS = ""
	if !src.Connected() || !canvasHovered {
		return
	}
	mouse := imgui.MousePos()
	mpos := [2]float32{mouse.X, mouse.Y}

	bestD := float32(aircraftHitRadiusPx * aircraftHitRadiusPx)
	for cs, trk := range src.Tracks() {
		if !filterMatch(trk, aircraftFilter(mp.Filter), src.UserTCW(), mp.FilterTCWFilter) {
			continue
		}
		s := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)
		dx := s[0] - mpos[0]
		dy := s[1] - mpos[1]
		d := dx*dx + dy*dy
		if d < bestD {
			bestD = d
			mp.hoveredCS = cs
		}
	}
}

// drawAircraftTriangle draws a 12px-tall isosceles triangle pointing along
// heading (0 = north, 90 = east) at center.
func drawAircraftTriangle(dl *imgui.DrawList, center [2]float32, headingDeg float32, color uint32) {
	rad := float64(headingDeg-90) * gomath.Pi / 180.0 // imgui +x = east, +y = south
	cosT := float32(gomath.Cos(rad))
	sinT := float32(gomath.Sin(rad))
	type p struct{ x, y float32 }
	local := [3]p{{8, 0}, {-5, -4}, {-5, 4}}
	var world [3]imgui.Vec2
	for i, lp := range local {
		// Rotate local triangle about origin, translate to center.
		x := lp.x*cosT - lp.y*sinT
		y := lp.x*sinT + lp.y*cosT
		world[i] = imgui.Vec2{X: center[0] + x, Y: center[1] + y}
	}
	dl.AddTriangleFilled(world[0], world[1], world[2], color)
}
