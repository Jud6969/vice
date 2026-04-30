// pkg/panes/mappane_overlays.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"

	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/math"
)

// drawFacilityBoundary draws the facility's boundary circle (same data STARS
// uses) on the map. STARS draws a 1px red circle in lat-lon via
// AddLatLongCircle (stars/stars.go:1380); we draw the equivalent in
// screen-space through the imgui draw list because MapPane uses imgui rendering,
// not the GL command buffer.
func (mp *MapPane) drawFacilityBoundary(src TrackSource, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !mp.ShowBoundaries || !src.Connected() {
		return
	}
	facility, ok := av.DB.LookupFacility(src.Facility())
	if !ok {
		return
	}

	const nSegments = 180
	center := facility.Center()
	radiusNM := float64(facility.Radius)

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.50, Y: 0.56, Z: 0.62, W: 1})

	// Build segment-by-segment via AddLine; cimgui-go's AddPolyline binding
	// (v1.4.0) calls internal.Wrap on the points pointer, which converts only
	// the first element and leaves the rest pointing at uninitialized memory.
	var prev imgui.Vec2
	for i := 0; i <= nSegments; i++ {
		theta := float64(i) / float64(nSegments) * 2 * gomath.Pi
		// dy = radiusNM / NMPerLatitude; dx = radiusNM / nmPerLongitude
		dlat := float32(radiusNM/60.0) * float32(gomath.Sin(theta))
		dlon := float32(radiusNM/float64(nmPerLongitude)) * float32(gomath.Cos(theta))
		ll := math.Point2LL{center[0] + dlon, center[1] + dlat}
		s := cam.llToScreen(ll, canvasOrigin, canvasSize, nmPerLongitude)
		cur := imgui.Vec2{X: s[0], Y: s[1]}
		if i > 0 {
			mp.canvasDrawList.AddLine(prev, cur, color)
		}
		prev = cur
	}
}

func (mp *MapPane) drawAirportLabels(src TrackSource, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !mp.ShowAirports || !src.Connected() {
		return
	}

	airports := make(map[string]struct{})
	for ap := range src.Airports() {
		airports[ap] = struct{}{}
	}
	// Also include departure/arrival airports referenced by current tracks.
	for _, trk := range src.Tracks() {
		if trk.DepartureAirport != "" {
			airports[trk.DepartureAirport] = struct{}{}
		}
		if trk.ArrivalAirport != "" {
			airports[trk.ArrivalAirport] = struct{}{}
		}
	}

	view := mp.viewExtent(cam, canvasSize, nmPerLongitude)
	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.85, Y: 0.85, Z: 0.40, W: 1})
	dotColor := imgui.ColorU32Vec4(imgui.Vec4{X: 1.0, Y: 1.0, Z: 0.5, W: 1})

	for icao := range airports {
		ap, ok := av.DB.Airports[icao]
		if !ok {
			continue
		}
		loc := ap.Location
		if loc[0] < view.P0[0] || loc[0] > view.P1[0] || loc[1] < view.P0[1] || loc[1] > view.P1[1] {
			continue
		}
		s := cam.llToScreen(loc, canvasOrigin, canvasSize, nmPerLongitude)
		mp.canvasDrawList.AddCircleFilled(imgui.Vec2{X: s[0], Y: s[1]}, 3, dotColor)
		mp.canvasDrawList.AddTextVec2(imgui.Vec2{X: s[0] + 5, Y: s[1] - 7}, color, icao)
	}
}
