// pkg/panes/mappane.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/platform"
	"github.com/mmp/vice/renderer"
	"github.com/mmp/vice/sim"

	"github.com/AllenDang/cimgui-go/imgui"
)

// MapPane is a separate "world map" view of the simulation. Unlike the STARS
// scope it is north-up, has its own pan/zoom, and is meant to be floated out
// to a second monitor via imgui multi-viewport.
type MapPane struct {
	// Persisted toggles
	ShowBasemap     bool
	ShowBoundaries  bool
	ShowAirports    bool
	Filter          int    // aircraftFilter; int for JSON stability
	FilterTCWFilter string // populated when Filter == filterTCW

	// Camera (persisted center+range so the user keeps their view)
	CenterLat float32
	CenterLon float32
	RangeNM   float32

	// Runtime-only state (not persisted)
	font            *renderer.Font
	selectedCS      av.ADSBCallsign
	pastTrails      map[av.ADSBCallsign][]math.Point2LL
	lastTrailUpdate sim.Time
	initialized     bool // first Draw() fits view to facility
}

func NewMapPane() *MapPane {
	return &MapPane{
		ShowBasemap:    true,
		ShowBoundaries: true,
		ShowAirports:   true,
		RangeNM:        50,
	}
}

func (mp *MapPane) Activate(r renderer.Renderer, p platform.Platform, eventStream *sim.EventStream, lg *log.Logger) {
	mp.font = renderer.GetFont(renderer.FontIdentifier{Name: renderer.RobotoRegular, Size: 14})
	if mp.font == nil {
		mp.font = renderer.GetDefaultFont()
	}
	mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
}

func (mp *MapPane) LoadedSim(c *client.ControlClient, pl platform.Platform, lg *log.Logger) {
	mp.initialized = false
	mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
}

func (mp *MapPane) ResetSim(c *client.ControlClient, pl platform.Platform, lg *log.Logger) {
	mp.initialized = false
	mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
	mp.selectedCS = ""
}

func (mp *MapPane) CanTakeKeyboardFocus() bool { return false }

func (mp *MapPane) DisplayName() string { return "Map" }

func (mp *MapPane) DrawUI(p platform.Platform, config *platform.Config) {
	imgui.Checkbox("Show world basemap", &mp.ShowBasemap)
	imgui.Checkbox("Show facility boundary", &mp.ShowBoundaries)
	imgui.Checkbox("Show airport labels", &mp.ShowAirports)
}

// Draw is called when the pane is embedded in a layout. The map is normally
// shown via DrawWindow; Draw is here to satisfy the Pane interface.
func (mp *MapPane) Draw(ctx *Context, cb *renderer.CommandBuffer) {
	mp.draw(ctx, cb)
}

// DrawWindow draws the MapPane inside a floating imgui window. Wire this from
// the main UI when ui.showMap is true.
func (mp *MapPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, lg *log.Logger) {
	if !*show {
		return
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 800, Y: 600}, imgui.CondFirstUseEver)
	if imgui.BeginV("Map", show, imgui.WindowFlagsNone) {
		DrawPinButton("Map", unpinnedWindows, p)
		imgui.TextUnformatted("MapPane (skeleton)")
	}
	imgui.End()
}

// draw is the eventual entry point for OpenGL-rendered map content. For now
// it's a no-op so the build is happy.
func (mp *MapPane) draw(ctx *Context, cb *renderer.CommandBuffer) {
}
