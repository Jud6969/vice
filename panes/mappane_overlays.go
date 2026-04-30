// pkg/panes/mappane_overlays.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"

	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
)

// drawFacilityBoundary draws the facility's boundary circle (same data STARS
// uses) on the map. STARS draws a 1px red circle in lat-lon via
// AddLatLongCircle (stars/stars.go:1380); we draw the equivalent in
// screen-space through the imgui draw list because MapPane uses imgui rendering,
// not the GL command buffer.
func (mp *MapPane) drawFacilityBoundary(c *client.ControlClient, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !mp.ShowBoundaries || c == nil || !c.Connected() {
		return
	}
	facility, ok := av.DB.LookupFacility(c.State.Facility)
	if !ok {
		return
	}

	const nSegments = 180
	center := facility.Center()
	radiusNM := float64(facility.Radius)

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.50, Y: 0.56, Z: 0.62, W: 1})

	pts := make([]imgui.Vec2, 0, nSegments+1)
	for i := 0; i <= nSegments; i++ {
		theta := float64(i) / float64(nSegments) * 2 * gomath.Pi
		// dy = radiusNM / NMPerLatitude; dx = radiusNM / nmPerLongitude
		dlat := float32(radiusNM/60.0) * float32(gomath.Sin(theta))
		dlon := float32(radiusNM/float64(nmPerLongitude)) * float32(gomath.Cos(theta))
		ll := math.Point2LL{center[0] + dlon, center[1] + dlat}
		s := cam.llToScreen(ll, canvasOrigin, canvasSize, nmPerLongitude)
		pts = append(pts, imgui.Vec2{X: s[0], Y: s[1]})
	}
	mp.canvasDrawList.AddPolyline(&pts[0], int32(len(pts)), color, imgui.DrawFlagsNone, 1.0)
}
