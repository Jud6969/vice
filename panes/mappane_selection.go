// pkg/panes/mappane_selection.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"time"

	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
)

const aircraftHitRadiusPx = 15

// handleSelection processes a click inside the canvas. Sets / clears
// mp.selectedCS based on which aircraft (if any) was hit. Must be called
// AFTER the camera state for the frame is finalized so the screen
// projection matches what the user sees.
func (mp *MapPane) handleSelection(c *client.ControlClient, cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if c == nil || !c.Connected() {
		return
	}
	if !imgui.IsItemHovered() || !imgui.IsMouseClickedBool(imgui.MouseButtonLeft) {
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

const trailCap = 120 // ~2min at 1Hz

func pushTrail(buf []math.Point2LL, p math.Point2LL, cap int) []math.Point2LL {
	buf = append(buf, p)
	if len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	return buf
}

// updateTrails appends the current frame's track positions to the per-aircraft
// trail buffer, gated to ~1Hz so high-frame-rate frames don't fill the buffer.
// Aircraft that have left the sim have their entries pruned.
func (mp *MapPane) updateTrails(c *client.ControlClient) {
	if c == nil || !c.Connected() {
		return
	}

	now := time.Now()
	if !mp.lastTrailUpdate.IsZero() && now.Sub(mp.lastTrailUpdate) < time.Second {
		return
	}
	mp.lastTrailUpdate = now

	if mp.pastTrails == nil {
		mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
	}

	for cs, trk := range c.State.Tracks {
		mp.pastTrails[cs] = pushTrail(mp.pastTrails[cs], trk.Location, trailCap)
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
	screenPts := make([]imgui.Vec2, 0, len(pts))
	for _, p := range pts {
		s := cam.llToScreen(p, canvasOrigin, canvasSize, nmPerLongitude)
		screenPts = append(screenPts, imgui.Vec2{X: s[0], Y: s[1]})
	}
	mp.canvasDrawList.AddPolyline(&screenPts[0], int32(len(screenPts)), color, imgui.DrawFlagsNone, 1.0)
}
