// pkg/panes/mappane_selection.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
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
