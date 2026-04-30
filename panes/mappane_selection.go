// pkg/panes/mappane_selection.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"fmt"
	gomath "math"

	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
)

const aircraftHitRadiusPx = 15

// handleSelection processes a click inside the canvas. Sets / clears
// mp.selectedCS based on which aircraft (if any) was hit. The caller passes
// canvasHovered captured immediately after the canvas Dummy(), since by the
// time this runs many sibling imgui items have been submitted and
// IsItemHovered() no longer references the canvas.
func (mp *MapPane) handleSelection(c *client.ControlClient, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32, canvasHovered bool) {
	if c == nil || !c.Connected() {
		return
	}
	if !canvasHovered || !imgui.IsMouseClickedBool(imgui.MouseButtonLeft) {
		return
	}
	// Don't treat the end of a drag as a click — only fresh clicks.
	if imgui.IsMouseDragging(imgui.MouseButtonLeft) {
		return
	}
	mouse := imgui.MousePos()
	mpos := [2]float32{mouse.X, mouse.Y}

	bestD := float32(aircraftHitRadiusPx * aircraftHitRadiusPx)
	var hit av.ADSBCallsign

	for cs, trk := range c.State.Tracks {
		if !filterMatch(trk, aircraftFilter(mp.Filter), c.State.UserTCW, mp.FilterTCWFilter) {
			continue
		}
		s := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)
		dx := s[0] - mpos[0]
		dy := s[1] - mpos[1]
		d := dx*dx + dy*dy
		if d < bestD {
			bestD = d
			hit = cs
		}
	}
	mp.selectedCS = hit // empty if no hit
}

// trailCap is generous so the trail captures every distinct position the
// server has reported for several minutes of flight. Cheap (8 bytes/point ×
// trailCap × ~50 aircraft = ~600 KB worst case).
const trailCap = 1500

func pushTrail(buf []math.Point2LL, p math.Point2LL, cap int) []math.Point2LL {
	buf = append(buf, p)
	if len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	return buf
}

// updateTrails appends the current track position for each aircraft, but only
// when it differs from the most-recent buffered point. That way the trail
// captures every distinct server-reported position (so the polyline curves
// smoothly through turns instead of stepping at 1Hz wall-clock samples).
// Aircraft that have left the sim have their entries pruned.
func (mp *MapPane) updateTrails(c *client.ControlClient) {
	if c == nil || !c.Connected() {
		return
	}

	if mp.pastTrails == nil {
		mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
	}

	for cs, trk := range c.State.Tracks {
		buf := mp.pastTrails[cs]
		if len(buf) > 0 {
			last := buf[len(buf)-1]
			if last == trk.Location {
				continue // dedupe: position hasn't moved
			}
		}
		mp.pastTrails[cs] = pushTrail(buf, trk.Location, trailCap)
	}
	for cs := range mp.pastTrails {
		if _, ok := c.State.Tracks[cs]; !ok {
			delete(mp.pastTrails, cs)
		}
	}
}

func (mp *MapPane) drawSelectedTrail(cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if mp.selectedCS == "" {
		return
	}
	pts, ok := mp.pastTrails[mp.selectedCS]
	if !ok || len(pts) < 2 {
		return
	}

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.55, Y: 0.55, Z: 0.85, W: 0.7})
	// AddLine per segment — cimgui-go's AddPolyline binding (v1.4.0) is broken;
	// see drawFacilityBoundary for the explanation.
	prev := cam.llToScreen(pts[0], canvasOrigin, canvasSize, nmPerLongitude)
	for i := 1; i < len(pts); i++ {
		cur := cam.llToScreen(pts[i], canvasOrigin, canvasSize, nmPerLongitude)
		mp.canvasDrawList.AddLine(
			imgui.Vec2{X: prev[0], Y: prev[1]},
			imgui.Vec2{X: cur[0], Y: cur[1]},
			color)
		prev = cur
	}
}

func (mp *MapPane) drawSelectedRoute(c *client.ControlClient, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if mp.selectedCS == "" || c == nil || !c.Connected() {
		return
	}
	trk, ok := c.State.Tracks[mp.selectedCS]
	if !ok {
		return
	}
	if len(trk.Route) == 0 {
		return
	}

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.40, Y: 0.85, Z: 1.0, W: 0.95})

	// Defensive copy so we never mutate the shared slice in c.State.Tracks.
	pts := append([]math.Point2LL(nil), trk.Route...)
	if (trk.ArrivalAirportLocation != math.Point2LL{}) {
		pts = append(pts, trk.ArrivalAirportLocation)
	}

	prev := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)
	for _, p := range pts {
		cur := cam.llToScreen(p, canvasOrigin, canvasSize, nmPerLongitude)
		drawDashedLine(mp.canvasDrawList, prev, cur, color, 6, 4)
		prev = cur
	}
}

// drawDashedLine draws a dashed line from a to b in screen space.
func drawDashedLine(dl *imgui.DrawList, a, b [2]float32, color uint32, dashLen, gapLen float32) {
	dx := b[0] - a[0]
	dy := b[1] - a[1]
	totalSq := dx*dx + dy*dy
	if totalSq < 1 {
		return
	}
	totalLen := float32(gomath.Sqrt(float64(totalSq)))
	stepLen := dashLen + gapLen
	t := float32(0)
	for t < totalLen {
		t1 := t + dashLen
		if t1 > totalLen {
			t1 = totalLen
		}
		p0 := imgui.Vec2{X: a[0] + dx*(t/totalLen), Y: a[1] + dy*(t/totalLen)}
		p1 := imgui.Vec2{X: a[0] + dx*(t1/totalLen), Y: a[1] + dy*(t1/totalLen)}
		dl.AddLine(p0, p1, color)
		t += stepLen
	}
}

// drawHoverTooltip shows a small floating box near the cursor for the
// hovered aircraft (callsign, altitude, ground speed). Bails when nothing
// is hovered.
func (mp *MapPane) drawHoverTooltip(c *client.ControlClient) {
	if mp.hoveredCS == "" || c == nil || !c.Connected() {
		return
	}
	trk, ok := c.State.Tracks[mp.hoveredCS]
	if !ok {
		return
	}

	mouse := imgui.MousePos()
	imgui.SetNextWindowPosV(imgui.Vec2{X: mouse.X + 16, Y: mouse.Y + 16}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(0.9)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoFocusOnAppearing | imgui.WindowFlagsNoNav |
		imgui.WindowFlagsNoInputs

	if imgui.BeginV("##maphover", nil, flags) {
		imgui.TextUnformatted(string(mp.hoveredCS))
		imgui.TextUnformatted(fmt.Sprintf("ALT %d  GS %d", int(trk.TrueAltitude), int(trk.Groundspeed)))
	}
	imgui.End()
}

// drawCornerInfoPanel anchors the detailed info panel to the top-right of the
// canvas. Bails when nothing is selected.
func (mp *MapPane) drawCornerInfoPanel(c *client.ControlClient) {
	if mp.selectedCS == "" || c == nil || !c.Connected() {
		return
	}
	trk, ok := c.State.Tracks[mp.selectedCS]
	if !ok {
		mp.selectedCS = ""
		return
	}

	// Anchor at the top-right of the canvas. Pivot {1,0} aligns the window's
	// top-right corner to the anchor point so AlwaysAutoResize grows leftward.
	cornerX := mp.canvasOrigin[0] + mp.canvasSize[0] - 8
	cornerY := mp.canvasOrigin[1] + 8
	imgui.SetNextWindowPosV(imgui.Vec2{X: cornerX, Y: cornerY}, imgui.CondAlways, imgui.Vec2{X: 1, Y: 0})
	imgui.SetNextWindowBgAlpha(0.9)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoFocusOnAppearing | imgui.WindowFlagsNoNav

	if imgui.BeginV("##mapinfo", nil, flags) {
		imgui.TextUnformatted(string(mp.selectedCS))
		imgui.Separator()

		acType := ""
		squawk := ""
		if trk.FlightPlan != nil {
			acType = trk.FlightPlan.AircraftType
			squawk = trk.FlightPlan.AssignedSquawk.String()
		}
		if acType != "" {
			imgui.TextUnformatted("Type:    " + acType)
		}
		if squawk != "" {
			imgui.TextUnformatted("Squawk:  " + squawk)
		}
		imgui.TextUnformatted("DEP:     " + trk.DepartureAirport)
		imgui.TextUnformatted("ARR:     " + trk.ArrivalAirport)
		imgui.TextUnformatted(fmt.Sprintf("ALT:     %d ft", int(trk.TrueAltitude)))
		imgui.TextUnformatted(fmt.Sprintf("GS:      %d kt", int(trk.Groundspeed)))
		imgui.TextUnformatted(fmt.Sprintf("HDG:     %03d°", int(trk.Heading)))
		if trk.SID != "" {
			imgui.TextUnformatted("SID:     " + trk.SID)
		}
		if trk.STAR != "" {
			imgui.TextUnformatted("STAR:    " + trk.STAR)
		}
		if trk.Approach != "" {
			status := "assigned"
			if trk.ClearedForApproach {
				status = "cleared"
			} else if trk.OnApproach {
				status = "on approach"
			}
			imgui.TextUnformatted(fmt.Sprintf("APP:     %s (%s)", trk.Approach, status))
		}
		if trk.HoldForRelease {
			imgui.TextUnformatted("Hold for release")
		}
		if trk.MissingFlightPlan {
			imgui.TextUnformatted("(missing flight plan)")
		}

		imgui.Separator()
		imgui.TextUnformatted("Route:")
		imgui.PushTextWrapPosV(imgui.CursorPosX() + 360)
		if trk.FiledRoute != "" {
			imgui.TextUnformatted(trk.FiledRoute)
		} else {
			imgui.TextUnformatted("(none)")
		}
		imgui.PopTextWrapPos()
	}
	imgui.End()
}
