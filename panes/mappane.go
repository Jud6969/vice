// pkg/panes/mappane.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"sort"
	"time"

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
	// CameraSet is true after the first fit-to-facility. Persisted so that
	// reloading the sim preserves the user's pan/zoom; only LoadedSim /
	// ResetSim clear it to force a new fit.
	CameraSet bool

	// Runtime-only state (not persisted)
	font            *renderer.Font
	selectedCS      av.ADSBCallsign
	pastTrails      map[av.ADSBCallsign][]math.Point2LL
	lastTrailUpdate time.Time

	// runtime canvas state (not persisted)
	canvasOrigin   [2]float32
	canvasSize     [2]float32
	canvasDrawList *imgui.DrawList
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
	mp.CameraSet = false
	mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
}

func (mp *MapPane) ResetSim(c *client.ControlClient, pl platform.Platform, lg *log.Logger) {
	mp.CameraSet = false
	mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
	mp.selectedCS = ""
}

func (mp *MapPane) CanTakeKeyboardFocus() bool { return false }

func (mp *MapPane) DisplayName() string { return "Map" }

// DrawUI renders the pane's settings panel for the layout-config modal.
// The same checkboxes are duplicated in drawToolbar for the floating-window
// toolbar; both are intentional.
func (mp *MapPane) DrawUI(p platform.Platform, config *platform.Config) {
	imgui.Checkbox("Show world basemap", &mp.ShowBasemap)
	imgui.Checkbox("Show facility boundary", &mp.ShowBoundaries)
	imgui.Checkbox("Show airport labels", &mp.ShowAirports)
}

// Draw is called when the pane is embedded in a layout. MapPane is rendered
// through DrawWindow + imgui draw lists. No-op here.
func (mp *MapPane) Draw(ctx *Context, cb *renderer.CommandBuffer) {
}

// DrawWindow draws the MapPane inside a floating imgui window.
func (mp *MapPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, lg *log.Logger) {
	if !*show {
		return
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 800, Y: 600}, imgui.CondFirstUseEver)
	if imgui.BeginV("Map", show, imgui.WindowFlagsNone) {
		DrawPinButton("Map", unpinnedWindows, p)
		mp.drawToolbar(c)
		mp.drawCanvas(c, p, lg)
	}
	imgui.End()
}

// drawToolbar renders the floating-window toolbar. Mirrors DrawUI's
// checkboxes — both are intentional (different surfaces).
func (mp *MapPane) drawToolbar(c *client.ControlClient) {
	imgui.Checkbox("Basemap", &mp.ShowBasemap)
	imgui.SameLine()
	imgui.Checkbox("Boundary", &mp.ShowBoundaries)
	imgui.SameLine()
	imgui.Checkbox("Airports", &mp.ShowAirports)
	imgui.SameLine()

	labels := []string{"All", "Untracked", "Tracked", "My TCW", "Specific TCW"}
	if mp.Filter < 0 || mp.Filter >= len(labels) {
		mp.Filter = 0 // fallback to All
	}
	imgui.SetNextItemWidth(140)
	if imgui.BeginCombo("Filter", labels[mp.Filter]) {
		for i, l := range labels {
			if imgui.SelectableBoolV(l, mp.Filter == i, 0, imgui.Vec2{}) {
				mp.Filter = i
			}
		}
		imgui.EndCombo()
	}

	if aircraftFilter(mp.Filter) == filterTCW && c != nil && c.Connected() {
		imgui.SameLine()
		current := mp.FilterTCWFilter
		if current == "" {
			current = "(pick)"
		}
		imgui.SetNextItemWidth(80)
		if imgui.BeginCombo("TCW", current) {
			seen := make(map[string]struct{})
			for _, trk := range c.State.Tracks {
				if trk.FlightPlan != nil && trk.FlightPlan.OwningTCW != "" {
					seen[string(trk.FlightPlan.OwningTCW)] = struct{}{}
				}
			}
			keys := make([]string, 0, len(seen))
			for k := range seen {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, tcw := range keys {
				if imgui.SelectableBoolV(tcw, tcw == mp.FilterTCWFilter, 0, imgui.Vec2{}) {
					mp.FilterTCWFilter = tcw
				}
			}
			imgui.EndCombo()
		}
	}
}

func (mp *MapPane) drawCanvas(c *client.ControlClient, p platform.Platform, lg *log.Logger) {
	avail := imgui.ContentRegionAvail()
	if avail.X < 1 || avail.Y < 1 {
		return
	}
	pos := imgui.CursorScreenPos()
	dl := imgui.WindowDrawList()
	dl.AddRectFilled(pos, imgui.Vec2{X: pos.X + avail.X, Y: pos.Y + avail.Y},
		imgui.ColorU32Vec4(imgui.Vec4{X: 0.04, Y: 0.05, Z: 0.07, W: 1}))

	imgui.Dummy(avail)
	// Capture hover/active state immediately: IsItemHovered/IsItemActive
	// reference the most-recently submitted imgui item, and many items get
	// submitted between here and the mouse-handling block below (the info
	// panel calls BeginV/End on a sibling window, etc.).
	canvasHovered := imgui.IsItemHovered()
	canvasActive := imgui.IsItemActive()

	mp.canvasOrigin = [2]float32{pos.X, pos.Y}
	mp.canvasSize = [2]float32{avail.X, avail.Y}
	mp.canvasDrawList = dl

	// Fit-to-facility on first open (CameraSet stays true across reloads so
	// the user's pan/zoom is preserved; only LoadedSim/ResetSim clear it).
	if !mp.CameraSet && c != nil && c.Connected() {
		if facility, ok := av.DB.LookupFacility(c.State.Facility); ok {
			mp.CenterLon = facility.Center()[0]
			mp.CenterLat = facility.Center()[1]
			mp.RangeNM = facility.Radius * 1.5
			// Floor so tiny ATCTs (tower-only facilities) still show surrounding context.
			if mp.RangeNM < 30 {
				mp.RangeNM = 30
			}
		} else {
			mp.CenterLon, mp.CenterLat, mp.RangeNM = 0, 0, 100
		}
		// After first frame this stays true so reloads preserve the user's pan/zoom.
		mp.CameraSet = true
	}

	nmPerLon := defaultNmPerLongitude
	if c != nil && c.Connected() {
		nmPerLon = c.State.NmPerLongitude
	}

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}

	mp.drawBasemap(cam, mp.canvasOrigin, mp.canvasSize, nmPerLon, lg)

	mp.drawFacilityBoundary(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)
	mp.drawAirportLabels(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)
	mp.updateTrails(c)
	mp.drawSelectedTrail(cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)
	mp.drawSelectedRoute(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)
	mp.drawAircraft(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)
	mp.handleSelection(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon, canvasHovered)
	mp.drawInfoPanel(c, cam, mp.canvasOrigin, mp.canvasSize, nmPerLon)

	// Mouse: zoom on scroll inside canvas. canvasHovered was captured right
	// after the canvas Dummy() so it reflects the canvas item, not whatever
	// was submitted last by the overlays / info panel.
	if canvasHovered {
		if wheel := imgui.CurrentIO().MouseWheel(); wheel != 0 {
			factor := float32(0.9)
			if wheel < 0 {
				factor = 1.1
			}
			cam.applyZoomFactor(factor)
		}
	}

	// Mouse: pan on left-drag inside canvas.
	if canvasActive && imgui.IsMouseDragging(platform.MouseButtonPrimary) {
		delta := imgui.MouseDragDeltaV(platform.MouseButtonPrimary, 0.)
		imgui.ResetMouseDragDeltaV(platform.MouseButtonPrimary)
		cam.applyPanPixels(delta.X, delta.Y, mp.canvasSize, nmPerLon)
	}

	mp.CenterLon, mp.CenterLat, mp.RangeNM = cam.center[0], cam.center[1], cam.rangeNM
}
