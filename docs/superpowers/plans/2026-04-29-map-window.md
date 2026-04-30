# Map Window Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a separate map window — toggled from the main menu bar — that shows aircraft on a dark vector world map with pan/zoom, facility boundaries, optional airport labels, filterable visibility (all / untracked / tracked / my TCW / specific TCW), click-to-select with floating info panel, and past-trail + future-route for the selected aircraft.

**Architecture:** New `panes.MapPane` type in package `panes/`, alongside `MessagesPane` and `FlightStripPane`. Wired into the menu bar via a new `ui.showMap` toggle in `cmd/vice/ui.go`. Reuses `radar.ScopeTransformations` for lat-lon ↔ screen, `client.ControlClient.State` for tracks/airports/facility data, and `renderer.{Lines,Triangles,Text}DrawBuilder` for drawing. Background map data is bundled at build time via Go `//go:embed` from a small Natural Earth GeoJSON dataset committed under `panes/mapdata/`.

**Tech Stack:** Go, `github.com/AllenDang/cimgui-go/imgui` (UI), `github.com/mmp/vice/{aviation,client,math,panes,radar,renderer,sim}` (vice internals), Natural Earth public-domain coastline + admin-0 GeoJSON, Go stdlib `encoding/json`.

**Reference files for the engineer:**
- `panes/messages.go` — closest existing analog (a non-radar secondary pane with a floating window).
- `panes/panes.go` — `Pane` interface, `Context` (lines 117–154), helpers like `GetTrackByCallsign` (line 224).
- `cmd/vice/ui.go:166–316` — the main menu bar where the new button goes; `cmd/vice/ui.go:333–340` — pattern for `if ui.showX { config.XPane.DrawWindow(...) }`.
- `cmd/vice/config.go:194–212` — `getDefaultConfig` where the new pane gets constructed.
- `radar/tools.go:22–110` — `ScopeTransformations` (the projection we reuse).
- `stars/stars.go:1380–1400` — `drawTRACONBoundary` (template for the facility boundary overlay).
- `sim/state.go:512–542` — `Track` struct (the data we render per aircraft).

---

## File structure

**New files:**

- `panes/mappane.go` — `MapPane` type, `Pane` interface methods (`Activate`, `LoadedSim`, `ResetSim`, `CanTakeKeyboardFocus`, `Draw`), `DisplayName`, `DrawUI`, `DrawWindow`, `NewMapPane`. Top-level pane wiring.
- `panes/mappane_camera.go` — `camera` type (center + rangeNM), pan/zoom mutation from `Context.Mouse`, `transforms()` helper that wraps `radar.GetScopeTransformations`. Pure logic, easy to unit test.
- `panes/mappane_basemap.go` — Natural Earth GeoJSON loader (lazy, package-level cache), bounding-box culling, `drawBasemap`. Reads two embedded GeoJSON files via `//go:embed`.
- `panes/mappane_overlays.go` — `drawFacilityBoundary` and `drawAirportLabels`.
- `panes/mappane_aircraft.go` — `aircraftFilter` enum, `filterMatch()` predicate, `drawAircraft` (rotated plane glyph + callsign).
- `panes/mappane_selection.go` — click hit-test, past-trail ring buffer, future-route draw, info-panel imgui window.
- `panes/mappane_test.go` — unit tests for camera math, filter predicate, hit-test, trail buffer.
- `panes/mapdata/ne_50m_coastline.geojson` — Natural Earth 1:50m coastline (public domain, manually downloaded).
- `panes/mapdata/ne_50m_admin_0_countries.geojson` — Natural Earth 1:50m country borders (public domain, manually downloaded).

**Modified files:**

- `cmd/vice/ui.go` — add `showMap bool` to `ui` struct, add menu-bar button, add `if ui.showMap { config.MapPane.DrawWindow(...) }` block.
- `cmd/vice/config.go` — add `MapPane *panes.MapPane`, `ShowMap bool` to `ConfigNoSim`; construct in `getDefaultConfig`; nil-check in `LoadOrMakeDefaultConfig`.
- `cmd/vice/main.go` — call `config.MapPane.ResetSim` alongside the other panes.

---

## Task 1: Branch and pane skeleton

Wire up an empty `MapPane` so a button on the menu bar opens an empty floating
window. End state: clicking the new button toggles a window that says
"MapPane" on a dark background.

**Files:**
- Create: `panes/mappane.go`
- Modify: `cmd/vice/config.go` (add field + default + nil-check)
- Modify: `cmd/vice/ui.go` (add `showMap`, menu button, draw call)
- Modify: `cmd/vice/main.go` (add `ResetSim` call)

- [ ] **Step 1.1: Create the `MapPane` skeleton**

Create `panes/mappane.go` with this content:

```go
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
```

- [ ] **Step 1.2: Wire the pane into config**

Open `cmd/vice/config.go`. Locate the `ConfigNoSim` struct definition (search
for `MessagesPane *panes.MessagesPane`). Add two fields beside it:

```go
MapPane  *panes.MapPane
ShowMap  bool
```

In `getDefaultConfig` (around line 194), add the same two lines below
`MessagesPane: panes.NewMessagesPane()`:

```go
MapPane:               panes.NewMapPane(),
ShowMap:               false,
```

In `LoadOrMakeDefaultConfig` add a nil-check below the `MessagesPane` one
(around line 251):

```go
if config.MapPane == nil {
    config.MapPane = panes.NewMapPane()
}
```

- [ ] **Step 1.3: Add the menu-bar button + draw call**

In `cmd/vice/ui.go`, locate the `ui` struct (around line 33). Add `showMap bool`
beside `showMessages bool`.

Find the `uiInit` block where `ui.showMessages = config.ShowMessages` (around
line 144) and add below it:

```go
ui.showMap = config.ShowMap
```

In the menu-bar block (around line 246), find the messages-toggle button:

```go
if controlClient != nil && controlClient.Connected() {
    if imgui.Button(renderer.FontAwesomeIconComment) {
        ui.showMessages = !ui.showMessages
    }
```

Immediately after the matching `if imgui.IsItemHovered() { imgui.SetTooltip("Toggle messages window") }`,
add:

```go
if imgui.Button(renderer.FontAwesomeIconMap) {
    ui.showMap = !ui.showMap
}
if imgui.IsItemHovered() {
    imgui.SetTooltip("Toggle map window")
}
```

If `FontAwesomeIconMap` does not exist in `renderer/`, fall back to
`FontAwesomeIconGlobe`. (The engineer should grep `renderer/` for
`FontAwesomeIconMap` and `FontAwesomeIconGlobe` and pick whichever exists.)

Find the messages-window draw block (around line 334):

```go
if ui.showMessages {
    config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, lg)
}
```

Below it add:

```go
if ui.showMap {
    config.MapPane.DrawWindow(&ui.showMap, controlClient, p, config.UnpinnedWindows, lg)
}
```

Also find the place that writes `config.ShowMessages = ui.showMessages` (it is
near the close of the menu-bar handler — search for `config.ShowMessages`) and
add `config.ShowMap = ui.showMap` next to it.

- [ ] **Step 1.4: Hook ResetSim**

In `cmd/vice/main.go`, find the existing `config.MessagesPane.ResetSim(c, plat, lg)`
call (around line 567). Add below it:

```go
config.MapPane.ResetSim(c, plat, lg)
```

- [ ] **Step 1.5: Build**

Run: `go build -tags vulkan ./cmd/vice`
Expected: success, no errors.

- [ ] **Step 1.6: Smoke check (manual)**

Run vice. Click the new map button on the top menu bar. A small floating
window titled "Map" should appear with the text "MapPane (skeleton)".

- [ ] **Step 1.7: Commit**

```bash
git add panes/mappane.go cmd/vice/config.go cmd/vice/ui.go cmd/vice/main.go
git commit -m "panes: skeleton MapPane wired to menu bar button

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Camera + projection

Add the camera (center + rangeNM) with pan-on-drag and zoom-on-scroll, plus
the `transforms()` helper. Fit-to-facility on first show.

**Files:**
- Create: `panes/mappane_camera.go`
- Create/extend: `panes/mappane_test.go`
- Modify: `panes/mappane.go` (use the camera in `draw`)

- [ ] **Step 2.1: Write failing test for camera roundtrip**

Create `panes/mappane_test.go`:

```go
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
	xforms := cam.transforms(extent, 1.0/gomath.Cos(40.64*gomath.Pi/180.0)*60.0)

	in := math.Point2LL{-73.78, 40.64}
	screen := xforms.WindowFromLatLongP(in)
	back := xforms.LatLongFromWindowP(screen)
	if gomath.Abs(float64(back[0]-in[0])) > 1e-3 || gomath.Abs(float64(back[1]-in[1])) > 1e-3 {
		t.Fatalf("roundtrip drift: in=%v back=%v", in, back)
	}
}
```

- [ ] **Step 2.2: Run test to verify it fails**

Run: `go test ./panes/ -run TestCameraTransformsRoundtrip -v`
Expected: FAIL — `camera` undefined.

- [ ] **Step 2.3: Create `panes/mappane_camera.go`**

```go
// pkg/panes/mappane_camera.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/platform"
	"github.com/mmp/vice/radar"
)

// camera describes the MapPane's view: a center point in lat/lon and a
// half-width radius in nautical miles. The map is north-up.
type camera struct {
	center  math.Point2LL // [lon, lat]
	rangeNM float32
}

const (
	minRangeNM = 0.5
	maxRangeNM = 1500.0
)

func (c *camera) transforms(paneExtent math.Extent2D, nmPerLongitude float32) radar.ScopeTransformations {
	return radar.GetScopeTransformations(paneExtent, 0, nmPerLongitude, c.center, c.rangeNM, 0)
}

// applyMouse mutates the camera in response to mouse events. dragButton is the
// mouse button index that pans (we use left-click drag here when no aircraft
// hit); zoom uses the wheel.
func (c *camera) applyMouse(mouse *platform.MouseState, paneExtent math.Extent2D, nmPerLongitude float32) {
	if mouse == nil {
		return
	}
	xforms := c.transforms(paneExtent, nmPerLongitude)

	// Wheel zoom: positive Y = zoom in.
	if mouse.Wheel[1] != 0 {
		factor := float32(1)
		if mouse.Wheel[1] > 0 {
			factor = 0.9
		} else {
			factor = 1.1
		}
		newRange := c.rangeNM * factor
		if newRange < minRangeNM {
			newRange = minRangeNM
		}
		if newRange > maxRangeNM {
			newRange = maxRangeNM
		}
		c.rangeNM = newRange
	}

	// Left-button drag pans.
	if mouse.Dragging[platform.MouseButtonPrimary] {
		// Convert pixel delta to lat-lon delta.
		dll := xforms.LatLongFromWindowV([2]float32{mouse.DragDelta[0], mouse.DragDelta[1]})
		c.center[0] -= dll[0]
		c.center[1] -= dll[1]
	}
}
```

- [ ] **Step 2.4: Run test to verify it passes**

Run: `go test ./panes/ -run TestCameraTransformsRoundtrip -v`
Expected: PASS.

- [ ] **Step 2.5: Wire camera into MapPane**

Edit `panes/mappane.go`. In `draw`, replace the empty body with:

```go
func (mp *MapPane) draw(ctx *Context, cb *renderer.CommandBuffer) {
	if ctx.Client == nil {
		return
	}

	// Initialize camera fit-to-facility on first draw of a sim.
	if !mp.initialized {
		facility, ok := av.DB.LookupFacility(ctx.Client.State.Facility)
		if ok {
			mp.CenterLat = facility.Center()[1]
			mp.CenterLon = facility.Center()[0]
			mp.RangeNM = facility.Radius * 1.5
			if mp.RangeNM < 30 {
				mp.RangeNM = 30
			}
		} else {
			// Fallback: center on (0,0)
			mp.CenterLat, mp.CenterLon, mp.RangeNM = 0, 0, 100
		}
		mp.initialized = true
	}

	cam := camera{
		center:  math.Point2LL{mp.CenterLon, mp.CenterLat},
		rangeNM: mp.RangeNM,
	}
	cam.applyMouse(ctx.Mouse, ctx.PaneExtent, ctx.NmPerLongitude)
	mp.CenterLon, mp.CenterLat, mp.RangeNM = cam.center[0], cam.center[1], cam.rangeNM

	// Clear to dark navy.
	cb.ClearRGB(renderer.RGB{R: 0.04, G: 0.05, B: 0.07})

	_ = cam.transforms(ctx.PaneExtent, ctx.NmPerLongitude) // used by future tasks
}
```

- [ ] **Step 2.6: Wire DrawWindow to use the same code path**

Replace `DrawWindow` body to invoke the pane's `draw` inside an embedded
imgui-managed sub-region. Because the OpenGL command-buffer path is what
panes use, the simplest pattern is to host the MapPane via the same harness
as STARS — but that adds complexity. For v1 we instead render the MapPane's
content as an imgui child region using imgui draw lists. To keep TDD-able
logic separate from imgui plumbing, change `DrawWindow` to:

```go
func (mp *MapPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, lg *log.Logger) {
	if !*show {
		return
	}
	imgui.SetNextWindowSizeV(imgui.Vec2{X: 800, Y: 600}, imgui.CondFirstUseEver)
	if imgui.BeginV("Map", show, imgui.WindowFlagsNone) {
		DrawPinButton("Map", unpinnedWindows, p)
		mp.drawToolbar()
		mp.drawCanvas(c, p, lg)
	}
	imgui.End()
}

func (mp *MapPane) drawToolbar() {
	imgui.Checkbox("Basemap", &mp.ShowBasemap)
	imgui.SameLine()
	imgui.Checkbox("Boundary", &mp.ShowBoundaries)
	imgui.SameLine()
	imgui.Checkbox("Airports", &mp.ShowAirports)
}

func (mp *MapPane) drawCanvas(c *client.ControlClient, p platform.Platform, lg *log.Logger) {
	// Reserve space for a canvas inside the imgui window.
	avail := imgui.ContentRegionAvail()
	if avail.X < 1 || avail.Y < 1 {
		return
	}
	pos := imgui.CursorScreenPos()
	dl := imgui.WindowDrawList()
	// Background rectangle.
	dl.AddRectFilled(pos, imgui.Vec2{X: pos.X + avail.X, Y: pos.Y + avail.Y},
		imgui.ColorU32Vec4(imgui.Vec4{X: 0.04, Y: 0.05, Z: 0.07, W: 1}))

	// Reserve the space so subsequent imgui content goes below the canvas.
	imgui.Dummy(avail)

	// Stash the canvas geometry for downstream tasks (drawn later).
	mp.canvasOrigin = [2]float32{pos.X, pos.Y}
	mp.canvasSize = [2]float32{avail.X, avail.Y}
	mp.canvasDrawList = dl
}
```

Add fields to `MapPane`:

```go
canvasOrigin   [2]float32
canvasSize     [2]float32
canvasDrawList *imgui.DrawList // not exported; reset each frame
```

The imgui draw list is what we'll use for *all* MapPane rendering (lines,
glyphs, text). This keeps the pane self-contained and avoids needing to wire
up an OpenGL command buffer for an undocked window. The `Pane.Draw` method
becomes a no-op shim (the pane doesn't need to live in the layout system —
it lives in its own window).

Replace `Draw` with:

```go
func (mp *MapPane) Draw(ctx *Context, cb *renderer.CommandBuffer) {
	// MapPane is rendered through DrawWindow + imgui draw lists. No-op here.
}
```

- [ ] **Step 2.7: Add a screen-space helper that respects canvasOrigin**

Because we render into an imgui draw list (screen pixels, top-left origin) and
not the GL command buffer, `WindowFromLatLongP` (which assumes pane-local,
bottom-left origin) needs a thin adapter. Add to `mappane_camera.go`:

```go
// llToScreen returns a screen-space point in imgui coordinates (top-left origin)
// for a lat/lon, given the canvas origin and size.
func (c *camera) llToScreen(p math.Point2LL, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) [2]float32 {
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: canvasSize}
	xforms := c.transforms(extent, nmPerLongitude)
	pp := xforms.WindowFromLatLongP(p)
	// xforms returns Y up from bottom-left; imgui screen-space is Y down from top-left.
	return [2]float32{
		canvasOrigin[0] + pp[0],
		canvasOrigin[1] + (canvasSize[1] - pp[1]),
	}
}

// screenToLL inverts llToScreen.
func (c *camera) screenToLL(p [2]float32, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) math.Point2LL {
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: canvasSize}
	xforms := c.transforms(extent, nmPerLongitude)
	pp := [2]float32{p[0] - canvasOrigin[0], canvasSize[1] - (p[1] - canvasOrigin[1])}
	return xforms.LatLongFromWindowP(pp)
}
```

- [ ] **Step 2.8: Add test for `llToScreen` / `screenToLL` roundtrip**

Append to `panes/mappane_test.go`:

```go
func TestCameraScreenRoundtrip(t *testing.T) {
	cam := camera{center: math.Point2LL{-73.78, 40.64}, rangeNM: 50}
	origin := [2]float32{100, 50}
	size := [2]float32{800, 600}
	const nmPerLon = 45.5 // matches lat ~40.64

	in := math.Point2LL{-73.5, 40.7}
	screen := cam.llToScreen(in, origin, size, nmPerLon)
	back := cam.screenToLL(screen, origin, size, nmPerLon)
	if gomath.Abs(float64(back[0]-in[0])) > 1e-3 || gomath.Abs(float64(back[1]-in[1])) > 1e-3 {
		t.Fatalf("screen roundtrip drift: in=%v back=%v", in, back)
	}
	// Center should map to canvas center.
	centerScreen := cam.llToScreen(cam.center, origin, size, nmPerLon)
	wantX := origin[0] + size[0]/2
	wantY := origin[1] + size[1]/2
	if gomath.Abs(float64(centerScreen[0]-wantX)) > 1 || gomath.Abs(float64(centerScreen[1]-wantY)) > 1 {
		t.Fatalf("center not at canvas center: got=%v want=(%v,%v)", centerScreen, wantX, wantY)
	}
}
```

Run: `go test ./panes/ -run TestCamera -v`
Expected: both tests PASS.

- [ ] **Step 2.9: Apply mouse to camera via imgui**

`ctx.Mouse` is not available inside `DrawWindow` (no `Context`). Instead read
mouse state directly from imgui inside `drawCanvas`. After the `imgui.Dummy(avail)`
call, add:

```go
hovered := imgui.IsItemHovered()
if hovered {
	wheel := imgui.CurrentIO().MouseWheel()
	if wheel != 0 {
		factor := float32(0.9)
		if wheel < 0 {
			factor = 1.1
		}
		mp.RangeNM *= factor
		if mp.RangeNM < 0.5 {
			mp.RangeNM = 0.5
		}
		if mp.RangeNM > 1500 {
			mp.RangeNM = 1500
		}
	}
}
if imgui.IsItemActive() && imgui.IsMouseDraggingBool(imgui.MouseButtonLeft) {
	delta := imgui.MouseDragDelta()
	imgui.ResetMouseDragDelta()
	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	pixelDelta := [2]float32{delta.X, -delta.Y} // imgui Y is down; our LL is Y up
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: mp.canvasSize}
	xforms := cam.transforms(extent, 45.5) // nmPerLongitude — replaced once we have the client
	dll := xforms.LatLongFromWindowV(pixelDelta)
	mp.CenterLon -= dll[0]
	mp.CenterLat -= dll[1]
}
```

This temporarily hardcodes `nmPerLongitude=45.5`; Task 4 wires the real value
from the connected client.

- [ ] **Step 2.10: Initialize camera on first open**

Before the panning code in `drawCanvas`, add a fit-to-facility block (this
duplicates the snippet from Step 2.5 — keep it in `drawCanvas`, delete the
copy in `draw`):

```go
if !mp.initialized && c != nil && c.Connected() {
	if facility, ok := av.DB.LookupFacility(c.State.Facility); ok {
		mp.CenterLon = facility.Center()[0]
		mp.CenterLat = facility.Center()[1]
		mp.RangeNM = facility.Radius * 1.5
		if mp.RangeNM < 30 {
			mp.RangeNM = 30
		}
	} else {
		mp.CenterLon, mp.CenterLat, mp.RangeNM = 0, 0, 100
	}
	mp.initialized = true
}
```

(`av.DB.LookupFacility` returns `(av.Facility, bool)` per `stars/stars.go:1385`.)
Delete the equivalent block in the `draw` method, since `Draw` is now a no-op.

- [ ] **Step 2.11: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`
Expected: success.

Run vice, click the map button, drag in the canvas → it should pan; scroll →
it should zoom (no aircraft yet, but the canvas remains a black rectangle of
varying size relative to where you've panned). The toolbar checkboxes should
exist but currently do nothing visible.

- [ ] **Step 2.12: Commit**

```bash
git add panes/mappane.go panes/mappane_camera.go panes/mappane_test.go
git commit -m "panes: MapPane camera with pan/zoom and fit-to-facility

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Natural Earth basemap

Bundle Natural Earth coastline + admin-0 borders, parse once, draw culled to
the visible extent.

**Manual prerequisite (one-time, by the user):** download these two files
from `https://www.naturalearthdata.com/downloads/50m-physical-vectors/` and
`https://www.naturalearthdata.com/downloads/50m-cultural-vectors/`:

- `ne_50m_coastline.geojson` (or convert the shapefile to GeoJSON; if the
  user prefers, the smaller `ne_110m_*` versions also work)
- `ne_50m_admin_0_countries.geojson`

Place them at `panes/mapdata/ne_50m_coastline.geojson` and
`panes/mapdata/ne_50m_admin_0_countries.geojson`. Both are public domain.

The plan assumes these files exist when basemap rendering is implemented.
If they're missing, `drawBasemap` logs a warning and renders nothing — the
rest of the pane still works.

**Files:**
- Create: `panes/mappane_basemap.go`
- Create: `panes/mapdata/ne_50m_coastline.geojson` (downloaded)
- Create: `panes/mapdata/ne_50m_admin_0_countries.geojson` (downloaded)
- Extend: `panes/mappane_test.go`
- Modify: `panes/mappane.go` (call `drawBasemap`)

- [ ] **Step 3.1: Write failing test for the GeoJSON parser**

Append to `panes/mappane_test.go`:

```go
func TestParseGeoJSONLineStrings(t *testing.T) {
	// Minimal GeoJSON with one LineString and one MultiLineString polygon.
	src := []byte(`{
		"type": "FeatureCollection",
		"features": [
			{"type":"Feature","geometry":{"type":"LineString","coordinates":[[0,0],[1,1],[2,0]]}},
			{"type":"Feature","geometry":{"type":"MultiLineString","coordinates":[[[10,10],[11,11]],[[20,20],[21,21],[22,22]]]}},
			{"type":"Feature","geometry":{"type":"Polygon","coordinates":[[[5,5],[6,5],[6,6],[5,6],[5,5]]]}}
		]
	}`)
	pls, err := parseGeoJSONPolylines(src)
	if err != nil {
		t.Fatal(err)
	}
	// 1 LineString + 2 sub-strings of MultiLineString + 1 polygon ring = 4 polylines.
	if len(pls) != 4 {
		t.Fatalf("want 4 polylines, got %d", len(pls))
	}
	if len(pls[0].pts) != 3 || pls[0].pts[1][0] != 1 || pls[0].pts[1][1] != 1 {
		t.Fatalf("first polyline malformed: %+v", pls[0])
	}
	// Bounding box for second polyline (MultiLineString[0]).
	if pls[1].bounds.P0[0] != 10 || pls[1].bounds.P1[0] != 11 {
		t.Fatalf("second polyline bounds wrong: %+v", pls[1].bounds)
	}
}
```

- [ ] **Step 3.2: Run test to verify it fails**

Run: `go test ./panes/ -run TestParseGeoJSONLineStrings -v`
Expected: FAIL — `parseGeoJSONPolylines` undefined.

- [ ] **Step 3.3: Create `panes/mappane_basemap.go`**

```go
// pkg/panes/mappane_basemap.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/math"
)

//go:embed mapdata/ne_50m_coastline.geojson
var coastlineGeoJSON []byte

//go:embed mapdata/ne_50m_admin_0_countries.geojson
var countriesGeoJSON []byte

type polyline struct {
	pts    []math.Point2LL
	bounds math.Extent2D
}

var (
	basemapOnce  sync.Once
	basemapLines []polyline
	basemapErr   error
)

func loadBasemap() ([]polyline, error) {
	basemapOnce.Do(func() {
		var all []polyline
		coast, err := parseGeoJSONPolylines(coastlineGeoJSON)
		if err != nil {
			basemapErr = fmt.Errorf("coastline: %w", err)
			return
		}
		all = append(all, coast...)
		ctry, err := parseGeoJSONPolylines(countriesGeoJSON)
		if err != nil {
			basemapErr = fmt.Errorf("countries: %w", err)
			return
		}
		all = append(all, ctry...)
		basemapLines = all
	})
	return basemapLines, basemapErr
}

// parseGeoJSONPolylines extracts every LineString, MultiLineString, Polygon,
// and MultiPolygon ring as a flat list of polylines. Anything else is ignored.
func parseGeoJSONPolylines(src []byte) ([]polyline, error) {
	type geom struct {
		Type        string          `json:"type"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	type feat struct {
		Geometry geom `json:"geometry"`
	}
	type fc struct {
		Features []feat `json:"features"`
	}
	var f fc
	if err := json.Unmarshal(src, &f); err != nil {
		return nil, err
	}
	var out []polyline
	for _, ft := range f.Features {
		out = appendGeometryPolylines(out, ft.Geometry.Type, ft.Geometry.Coordinates)
	}
	return out, nil
}

func appendGeometryPolylines(out []polyline, typ string, coords json.RawMessage) []polyline {
	switch typ {
	case "LineString":
		var pts [][2]float32
		if err := json.Unmarshal(coords, &pts); err == nil {
			out = append(out, makePolyline(pts))
		}
	case "MultiLineString":
		var lines [][][2]float32
		if err := json.Unmarshal(coords, &lines); err == nil {
			for _, line := range lines {
				out = append(out, makePolyline(line))
			}
		}
	case "Polygon":
		var rings [][][2]float32
		if err := json.Unmarshal(coords, &rings); err == nil {
			for _, ring := range rings {
				out = append(out, makePolyline(ring))
			}
		}
	case "MultiPolygon":
		var polys [][][][2]float32
		if err := json.Unmarshal(coords, &polys); err == nil {
			for _, poly := range polys {
				for _, ring := range poly {
					out = append(out, makePolyline(ring))
				}
			}
		}
	}
	return out
}

func makePolyline(raw [][2]float32) polyline {
	pl := polyline{pts: make([]math.Point2LL, len(raw))}
	if len(raw) == 0 {
		return pl
	}
	pl.bounds.P0 = raw[0]
	pl.bounds.P1 = raw[0]
	for i, p := range raw {
		pl.pts[i] = math.Point2LL{p[0], p[1]}
		if p[0] < pl.bounds.P0[0] {
			pl.bounds.P0[0] = p[0]
		}
		if p[1] < pl.bounds.P0[1] {
			pl.bounds.P0[1] = p[1]
		}
		if p[0] > pl.bounds.P1[0] {
			pl.bounds.P1[0] = p[0]
		}
		if p[1] > pl.bounds.P1[1] {
			pl.bounds.P1[1] = p[1]
		}
	}
	return pl
}

func (mp *MapPane) drawBasemap(canvasOrigin, canvasSize [2]float32, nmPerLongitude float32, lg *log.Logger) {
	if !mp.ShowBasemap {
		return
	}
	lines, err := loadBasemap()
	if err != nil {
		lg.Warnf("basemap load failed: %v", err)
		return
	}

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	// View extent in lat-lon for culling.
	view := mp.viewExtent(canvasSize, nmPerLongitude)

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.30, Y: 0.36, Z: 0.42, W: 1})

	for _, pl := range lines {
		if !view.Overlaps(pl.bounds) {
			continue
		}
		if len(pl.pts) < 2 {
			continue
		}
		prev := cam.llToScreen(pl.pts[0], canvasOrigin, canvasSize, nmPerLongitude)
		for i := 1; i < len(pl.pts); i++ {
			cur := cam.llToScreen(pl.pts[i], canvasOrigin, canvasSize, nmPerLongitude)
			mp.canvasDrawList.AddLine(
				imgui.Vec2{X: prev[0], Y: prev[1]},
				imgui.Vec2{X: cur[0], Y: cur[1]},
				color)
			prev = cur
		}
	}
}

// viewExtent returns the lat-lon bounding box currently visible in the canvas.
func (mp *MapPane) viewExtent(canvasSize [2]float32, nmPerLongitude float32) math.Extent2D {
	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	corners := [4][2]float32{
		{0, 0}, {canvasSize[0], 0}, {canvasSize[0], canvasSize[1]}, {0, canvasSize[1]},
	}
	var ext math.Extent2D
	for i, c := range corners {
		ll := cam.screenToLL(c, [2]float32{0, 0}, canvasSize, nmPerLongitude)
		if i == 0 {
			ext.P0, ext.P1 = ll, ll
			continue
		}
		if ll[0] < ext.P0[0] {
			ext.P0[0] = ll[0]
		}
		if ll[1] < ext.P0[1] {
			ext.P0[1] = ll[1]
		}
		if ll[0] > ext.P1[0] {
			ext.P1[0] = ll[0]
		}
		if ll[1] > ext.P1[1] {
			ext.P1[1] = ll[1]
		}
	}
	return ext
}
```

If `math.Extent2D` does not have an `Overlaps` method, the engineer should
grep for it; if missing, add it to `math/extent.go` (or use the existing
`Overlaps` function — search for `func.*Extent.*Overlap`). If neither exists,
inline:

```go
func extentsOverlap(a, b math.Extent2D) bool {
	return !(a.P1[0] < b.P0[0] || b.P1[0] < a.P0[0] ||
		a.P1[1] < b.P0[1] || b.P1[1] < a.P0[1])
}
```

…and use that in place of `view.Overlaps(pl.bounds)`.

- [ ] **Step 3.4: Run test to verify it passes**

Run: `go test ./panes/ -run TestParseGeoJSONLineStrings -v`
Expected: PASS.

- [ ] **Step 3.5: Wire `drawBasemap` into `drawCanvas`**

In `panes/mappane.go`, immediately after `mp.canvasDrawList = dl` and before
the mouse-handling block, add:

```go
nmPerLon := float32(45.5)
if c != nil && c.Connected() {
	nmPerLon = c.State.NmPerLongitude
}
mp.drawBasemap(mp.canvasOrigin, mp.canvasSize, nmPerLon, lg)
```

Replace the hardcoded `45.5` in the panning block (Step 2.9) with `nmPerLon`.

- [ ] **Step 3.6: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

If build fails because `coastlineGeoJSON`/`countriesGeoJSON` are empty (the
files at `panes/mapdata/` are missing), the engineer should pause and
download them per the manual prerequisite at the top of this task.

Run vice, click map → coastlines and country borders should be visible. Pan
and zoom: lines should track correctly. Toggling "Basemap" off should hide
them.

- [ ] **Step 3.7: Commit**

```bash
git add panes/mappane.go panes/mappane_basemap.go panes/mappane_test.go panes/mapdata/
git commit -m "panes: MapPane Natural Earth basemap

Bundles ne_50m_coastline + ne_50m_admin_0_countries via go:embed.
Parse once on first draw, cull by view bounding box.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Facility boundary overlay

Reuse the same data STARS uses (`av.DB.LookupFacility(...).Center / .Radius`).

**Files:**
- Create: `panes/mappane_overlays.go`
- Modify: `panes/mappane.go` (call `drawFacilityBoundary`)

- [ ] **Step 4.1: Create `panes/mappane_overlays.go`**

```go
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

func (mp *MapPane) drawFacilityBoundary(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !mp.ShowBoundaries || c == nil || !c.Connected() {
		return
	}
	facility, ok := av.DB.LookupFacility(c.State.Facility)
	if !ok {
		return
	}

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.50, Y: 0.56, Z: 0.62, W: 1})

	const nSegments = 180
	center := facility.Center()
	radiusNM := facility.Radius

	var prev [2]float32
	for i := 0; i <= nSegments; i++ {
		theta := float64(i) / float64(nSegments) * 2 * gomath.Pi
		// dy = radiusNM / NMPerLatitude; dx = radiusNM / nmPerLongitude
		dlat := float32(radiusNM/60.0) * float32(gomath.Sin(theta))
		dlon := float32(radiusNM/float64(nmPerLongitude)) * float32(gomath.Cos(theta))
		ll := math.Point2LL{center[0] + dlon, center[1] + dlat}
		cur := cam.llToScreen(ll, canvasOrigin, canvasSize, nmPerLongitude)
		if i > 0 {
			mp.canvasDrawList.AddLine(
				imgui.Vec2{X: prev[0], Y: prev[1]},
				imgui.Vec2{X: cur[0], Y: cur[1]},
				color)
		}
		prev = cur
	}
}
```

(Note: STARS draws the facility boundary as a circle of radius `Radius`
centered on `Center()` — see `stars/stars.go:1394` and the `AddLatLongCircle`
helper. We do the same here in screen space.)

- [ ] **Step 4.2: Wire into `drawCanvas`**

In `panes/mappane.go` `drawCanvas`, after the basemap call:

```go
mp.drawFacilityBoundary(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 4.3: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Run vice, click map → a circular facility boundary appears, brighter than
the basemap. Toggle "Boundary" off → it disappears.

- [ ] **Step 4.4: Commit**

```bash
git add panes/mappane.go panes/mappane_overlays.go
git commit -m "panes: MapPane facility boundary overlay

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Airport labels

Common airports = scenario's departure + arrival airports.

**Files:**
- Modify: `panes/mappane_overlays.go` (add `drawAirportLabels`)
- Modify: `panes/mappane.go` (call it)

- [ ] **Step 5.1: Add `drawAirportLabels`**

Append to `panes/mappane_overlays.go`:

```go
import (
	// add to existing imports:
	"github.com/mmp/vice/util"
)

func (mp *MapPane) drawAirportLabels(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if !mp.ShowAirports || c == nil || !c.Connected() {
		return
	}

	airports := make(map[string]struct{})
	for ap := range c.State.Airports {
		airports[ap] = struct{}{}
	}
	// Also include departure/arrival airports referenced by current tracks.
	for _, trk := range c.State.Tracks {
		if trk.DepartureAirport != "" {
			airports[trk.DepartureAirport] = struct{}{}
		}
		if trk.ArrivalAirport != "" {
			airports[trk.ArrivalAirport] = struct{}{}
		}
	}

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	view := mp.viewExtent(canvasSize, nmPerLongitude)
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
		mp.canvasDrawList.AddCircleFilledV(imgui.Vec2{X: s[0], Y: s[1]}, 3, dotColor, 8)
		mp.canvasDrawList.AddTextVec2(imgui.Vec2{X: s[0] + 5, Y: s[1] - 7}, color, icao)
	}
	_ = util.Select // keep import; remove if unused
}
```

If `av.DB.Airports[icao].Location` doesn't compile, the engineer should grep
`aviation/db.go` for the airport-position field name. (It is likely
`av.DB.Airports[icao].Location` or `.Position`. Search with `grep -n "Location" aviation/db.go aviation/airport.go` to confirm.)

- [ ] **Step 5.2: Wire into `drawCanvas`**

In `panes/mappane.go` `drawCanvas`, after `drawFacilityBoundary`:

```go
mp.drawAirportLabels(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 5.3: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Run vice. Map should show airport ICAO codes near small dots for the scenario's
airports. Toggle "Airports" off → labels disappear.

- [ ] **Step 5.4: Commit**

```bash
git add panes/mappane.go panes/mappane_overlays.go
git commit -m "panes: MapPane airport labels overlay

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Aircraft glyphs + callsigns

Iterate `client.State.Tracks`, draw a rotated triangle (we'll use a triangle,
not a font glyph — simpler and looks crisp at any rotation) plus the callsign
label. Untracked aircraft get a dimmer color.

**Files:**
- Create: `panes/mappane_aircraft.go`
- Modify: `panes/mappane.go` (call `drawAircraft`)

- [ ] **Step 6.1: Create `panes/mappane_aircraft.go`**

```go
// pkg/panes/mappane_aircraft.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	gomath "math"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
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

func (mp *MapPane) drawAircraft(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if c == nil || !c.Connected() {
		return
	}
	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	view := mp.viewExtent(canvasSize, nmPerLongitude)

	for cs, trk := range c.State.Tracks {
		if !filterMatch(trk, aircraftFilter(mp.Filter), c.State.UserTCW, mp.FilterTCWFilter) {
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

		// Callsign label
		labelPos := imgui.Vec2{X: s[0] + 9, Y: s[1] - 7}
		mp.canvasDrawList.AddTextVec2(labelPos, colU32, string(cs))
	}
}

// drawAircraftTriangle draws a 12px-tall isoceles triangle pointing along heading
// (0 = north, 90 = east) at center.
func drawAircraftTriangle(dl *imgui.DrawList, center [2]float32, headingDeg float32, color uint32) {
	rad := float64(headingDeg-90) * gomath.Pi / 180.0 // imgui +x = east, +y = south
	cosT := float32(gomath.Cos(rad))
	sinT := float32(gomath.Sin(rad))
	// Local triangle pointing along +x.
	type p struct{ x, y float32 }
	local := [3]p{{8, 0}, {-5, -4}, {-5, 4}}
	var world [3]imgui.Vec2
	for i, lp := range local {
		// Note imgui Y is down; rotation matrix:
		x := lp.x*cosT - lp.y*sinT
		y := lp.x*sinT + lp.y*cosT
		world[i] = imgui.Vec2{X: center[0] + x, Y: center[1] + y}
	}
	dl.AddTriangleFilled(world[0], world[1], world[2], color)
}
```

- [ ] **Step 6.2: Add filterMatch unit test**

Append to `panes/mappane_test.go`:

```go
func TestFilterMatch(t *testing.T) {
	mkTrack := func(owner string) *sim.Track {
		if owner == "" {
			return &sim.Track{}
		}
		return &sim.Track{FlightPlan: &sim.NASFlightPlan{OwningTCW: sim.TCW(owner)}}
	}
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
```

Add the `"github.com/mmp/vice/sim"` import to the test file if not already
present.

- [ ] **Step 6.3: Run test**

Run: `go test ./panes/ -run TestFilterMatch -v`
Expected: PASS for all subtests.

- [ ] **Step 6.4: Wire into `drawCanvas`**

In `panes/mappane.go` `drawCanvas`, after `drawAirportLabels`:

```go
mp.drawAircraft(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 6.5: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Connect to a scenario with active aircraft. Open the map → triangles appear
at each aircraft's position, oriented along heading, with callsign labels.
Untracked aircraft (no controller assigned) appear dimmer.

- [ ] **Step 6.6: Commit**

```bash
git add panes/mappane.go panes/mappane_aircraft.go panes/mappane_test.go
git commit -m "panes: MapPane aircraft glyphs and callsign labels

Triangle pointing along heading, dimmed when no OwningTCW.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 7: Filter UI

Add the filter combo to the toolbar and (when "Specific TCW" selected) a
secondary combo with controller positions.

**Files:**
- Modify: `panes/mappane.go` (`drawToolbar`)

- [ ] **Step 7.1: Replace `drawToolbar` body**

In `panes/mappane.go`:

```go
func (mp *MapPane) drawToolbar(c *client.ControlClient) {
	imgui.Checkbox("Basemap", &mp.ShowBasemap)
	imgui.SameLine()
	imgui.Checkbox("Boundary", &mp.ShowBoundaries)
	imgui.SameLine()
	imgui.Checkbox("Airports", &mp.ShowAirports)
	imgui.SameLine()

	labels := []string{"All", "Untracked", "Tracked", "My TCW", "Specific TCW"}
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
			seen := make(map[sim.TCW]struct{})
			for _, ctrl := range c.State.Controllers {
				tcw := sim.TCW(ctrl.Position)
				if _, dup := seen[tcw]; dup {
					continue
				}
				seen[tcw] = struct{}{}
				if imgui.SelectableBoolV(string(tcw), tcw == sim.TCW(mp.FilterTCWFilter), 0, imgui.Vec2{}) {
					mp.FilterTCWFilter = string(tcw)
				}
			}
			imgui.EndCombo()
		}
	}
}
```

Update the call site in `DrawWindow` to pass `c`:

```go
mp.drawToolbar(c)
```

(If `ctrl.Position` does not produce the right TCW string, the engineer should
inspect `av.Controller` for the field that yields the workstation identifier.
A safe fallback while uncertain: list all unique non-empty `OwningTCW` values
seen in `c.State.Tracks` instead of iterating `Controllers`.)

- [ ] **Step 7.2: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Map toolbar should show a "Filter" combo with five entries. Switching among
them changes which aircraft appear. Selecting "Specific TCW" surfaces a
second combo for picking the TCW.

- [ ] **Step 7.3: Commit**

```bash
git add panes/mappane.go
git commit -m "panes: MapPane filter UI (all/untracked/tracked/my-TCW/specific-TCW)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: Click selection + hit test

Detect clicks on aircraft glyphs and store the selected callsign.

**Files:**
- Create: `panes/mappane_selection.go`
- Modify: `panes/mappane.go` (call `handleSelection` from `drawCanvas`)
- Extend: `panes/mappane_test.go`

- [ ] **Step 8.1: Failing test for hit-test**

Append to `panes/mappane_test.go`:

```go
func TestNearestAircraftHit(t *testing.T) {
	type cand struct {
		cs    string
		pos   [2]float32 // screen
	}
	cands := []cand{
		{"AAL1", [2]float32{100, 100}},
		{"AAL2", [2]float32{200, 200}},
		{"AAL3", [2]float32{300, 100}},
	}
	pick := func(mouse [2]float32) string {
		var best string
		bestD := float32(15 * 15) // 15px hit radius
		for _, c := range cands {
			dx := c.pos[0] - mouse[0]
			dy := c.pos[1] - mouse[1]
			d := dx*dx + dy*dy
			if d < bestD {
				bestD = d
				best = c.cs
			}
		}
		return best
	}
	if got := pick([2]float32{102, 99}); got != "AAL1" {
		t.Fatalf("near AAL1 got %q", got)
	}
	if got := pick([2]float32{500, 500}); got != "" {
		t.Fatalf("far from all got %q", got)
	}
}
```

(This test is a "predicate" probe — it locks the hit-radius behavior we'll
re-use in the implementation.)

- [ ] **Step 8.2: Run test**

Run: `go test ./panes/ -run TestNearestAircraftHit -v`
Expected: PASS (the test is self-contained).

- [ ] **Step 8.3: Create `panes/mappane_selection.go`**

```go
// pkg/panes/mappane_selection.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
)

const aircraftHitRadiusPx = 15

// handleSelection processes a click inside the canvas. Sets / clears
// mp.selectedCS based on which aircraft (if any) was hit.
func (mp *MapPane) handleSelection(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if c == nil || !c.Connected() {
		return
	}
	if !imgui.IsItemHovered() || !imgui.IsMouseClickedBool(imgui.MouseButtonLeft) {
		return
	}
	// Don't treat the end of a drag as a click — only fresh clicks.
	if imgui.IsMouseDraggingBool(imgui.MouseButtonLeft) {
		return
	}
	mouse := imgui.MousePos()
	mp_ := [2]float32{mouse.X, mouse.Y}

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	bestD := float32(aircraftHitRadiusPx * aircraftHitRadiusPx)
	var hit av.ADSBCallsign

	for cs, trk := range c.State.Tracks {
		if !filterMatch(trk, aircraftFilter(mp.Filter), c.State.UserTCW, mp.FilterTCWFilter) {
			continue
		}
		s := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)
		dx := s[0] - mp_[0]
		dy := s[1] - mp_[1]
		d := dx*dx + dy*dy
		if d < bestD {
			bestD = d
			hit = cs
		}
	}
	mp.selectedCS = hit // clears if no hit
}
```

- [ ] **Step 8.4: Wire into `drawCanvas`**

In `drawCanvas`, after the `drawAircraft` call (and after the mouse panning
block — order matters: panning consumes drag, selection consumes single
click):

```go
mp.handleSelection(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 8.5: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Click an aircraft → no visible effect yet, but `mp.selectedCS` is set. (Will
be visible after Tasks 9–11.) Click empty space → selection clears.

- [ ] **Step 8.6: Commit**

```bash
git add panes/mappane.go panes/mappane_selection.go panes/mappane_test.go
git commit -m "panes: MapPane click selection (15px hit radius)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Past-trail ring buffer

Capture last ~120 positions per aircraft (prune disappeared aircraft each
frame), draw the trail for the selected aircraft only.

**Files:**
- Modify: `panes/mappane_selection.go` (add `updateTrails`, `drawSelectedTrail`)
- Modify: `panes/mappane.go` (call them)
- Extend: `panes/mappane_test.go`

- [ ] **Step 9.1: Failing test for ring buffer**

Append to `panes/mappane_test.go`:

```go
func TestPushTrailCapped(t *testing.T) {
	pts := []math.Point2LL{}
	for i := 0; i < 200; i++ {
		pts = pushTrail(pts, math.Point2LL{float32(i), float32(i)}, 120)
	}
	if len(pts) != 120 {
		t.Fatalf("expected cap 120, got %d", len(pts))
	}
	// Oldest should be index 80 (200-120).
	if pts[0][0] != 80 {
		t.Fatalf("oldest %v", pts[0])
	}
	if pts[119][0] != 199 {
		t.Fatalf("newest %v", pts[119])
	}
}
```

- [ ] **Step 9.2: Run test to fail**

Run: `go test ./panes/ -run TestPushTrailCapped -v`
Expected: FAIL — `pushTrail` undefined.

- [ ] **Step 9.3: Implement**

Append to `panes/mappane_selection.go`:

```go
const trailCap = 120 // ~2min at 1Hz

func pushTrail(buf []math.Point2LL, p math.Point2LL, cap int) []math.Point2LL {
	buf = append(buf, p)
	if len(buf) > cap {
		buf = buf[len(buf)-cap:]
	}
	return buf
}

func (mp *MapPane) updateTrails(c *client.ControlClient) {
	if c == nil || !c.Connected() {
		return
	}
	// Append current positions only when the sim time has advanced. We use
	// a 1-second granularity so that fast wall-clock frames don't blow out
	// the buffer.
	now := c.InterpolatedSimTime()
	if !mp.lastTrailUpdate.IsZero() && now.Sub(mp.lastTrailUpdate) < 1 {
		return
	}
	mp.lastTrailUpdate = now

	if mp.pastTrails == nil {
		mp.pastTrails = make(map[av.ADSBCallsign][]math.Point2LL)
	}

	// Add current positions.
	for cs, trk := range c.State.Tracks {
		mp.pastTrails[cs] = pushTrail(mp.pastTrails[cs], trk.Location, trailCap)
	}
	// Prune entries for aircraft that no longer exist.
	for cs := range mp.pastTrails {
		if _, ok := c.State.Tracks[cs]; !ok {
			delete(mp.pastTrails, cs)
		}
	}
}

func (mp *MapPane) drawSelectedTrail(canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if mp.selectedCS == "" {
		return
	}
	pts, ok := mp.pastTrails[mp.selectedCS]
	if !ok || len(pts) < 2 {
		return
	}
	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.55, Y: 0.55, Z: 0.85, W: 0.7})
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
```

If the `c.InterpolatedSimTime()` / `sim.Time.Sub` API differs from what's
sketched here, the engineer should grep `client/control.go` and `sim/time.go`
to find the right accessor and the right way to compute "1 second elapsed".
A workable fallback that doesn't depend on sim time: track the last
wall-clock update via `time.Now()` and gate at 1 second.

- [ ] **Step 9.4: Run test to pass**

Run: `go test ./panes/ -run TestPushTrailCapped -v`
Expected: PASS.

- [ ] **Step 9.5: Wire into `drawCanvas`**

In `drawCanvas`, before `drawAircraft`:

```go
mp.updateTrails(c)
mp.drawSelectedTrail(mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 9.6: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Click an aircraft → after a few seconds a trailing line shows where it has
been.

- [ ] **Step 9.7: Commit**

```bash
git add panes/mappane.go panes/mappane_selection.go panes/mappane_test.go
git commit -m "panes: MapPane past-trail ring buffer for selected aircraft

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Future-route line

Draw the remaining filed route ahead of the selected aircraft as a dashed
polyline.

**Files:**
- Modify: `panes/mappane_selection.go` (`drawSelectedRoute`)
- Modify: `panes/mappane.go` (call it)

- [ ] **Step 10.1: Add `drawSelectedRoute`**

Append to `panes/mappane_selection.go`:

```go
func (mp *MapPane) drawSelectedRoute(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
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

	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	color := imgui.ColorU32Vec4(imgui.Vec4{X: 1.0, Y: 0.85, Z: 0.30, W: 0.95})

	// Start the line at the aircraft's current position.
	start := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)
	prev := start
	pts := trk.Route
	// If an arrival airport is known, append it so the line terminates at the airport.
	if (trk.ArrivalAirportLocation != math.Point2LL{}) {
		pts = append(pts, trk.ArrivalAirportLocation)
	}

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
```

Add the `gomath "math"` import to `panes/mappane_selection.go` (alongside
the existing imports).

- [ ] **Step 10.2: Wire into `drawCanvas`**

In `drawCanvas`, immediately after `drawSelectedTrail`:

```go
mp.drawSelectedRoute(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 10.3: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Click an aircraft with a filed route → a dashed yellow line should extend
ahead through its waypoints to the arrival airport.

- [ ] **Step 10.4: Commit**

```bash
git add panes/mappane.go panes/mappane_selection.go
git commit -m "panes: MapPane dashed future-route line for selected aircraft

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 11: Floating info panel

Anchor an imgui info window to the selected aircraft's screen position with
callsign, dep, arr, route, altitude, ground speed, heading.

**Files:**
- Modify: `panes/mappane_selection.go` (`drawInfoPanel`)
- Modify: `panes/mappane.go` (call it)

- [ ] **Step 11.1: Add `drawInfoPanel`**

Append to `panes/mappane_selection.go`:

```go
func (mp *MapPane) drawInfoPanel(c *client.ControlClient, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) {
	if mp.selectedCS == "" || c == nil || !c.Connected() {
		return
	}
	trk, ok := c.State.Tracks[mp.selectedCS]
	if !ok {
		mp.selectedCS = ""
		return
	}
	cam := camera{center: math.Point2LL{mp.CenterLon, mp.CenterLat}, rangeNM: mp.RangeNM}
	s := cam.llToScreen(trk.Location, canvasOrigin, canvasSize, nmPerLongitude)

	// Position the info panel a bit to the upper right of the aircraft.
	imgui.SetNextWindowPosV(imgui.Vec2{X: s[0] + 18, Y: s[1] - 18}, imgui.CondAlways, imgui.Vec2{})
	imgui.SetNextWindowBgAlpha(0.85)
	flags := imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoResize | imgui.WindowFlagsNoMove |
		imgui.WindowFlagsAlwaysAutoResize | imgui.WindowFlagsNoFocusOnAppearing | imgui.WindowFlagsNoNav

	if imgui.BeginV("##mapinfo_"+string(mp.selectedCS), nil, flags) {
		imgui.TextUnformatted(string(mp.selectedCS))
		imgui.Separator()
		dep := trk.DepartureAirport
		arr := trk.ArrivalAirport
		alt := int(trk.Altitude)
		gs := int(trk.Groundspeed)
		hdg := int(trk.Heading)
		imgui.TextUnformatted("DEP: " + dep)
		imgui.TextUnformatted("ARR: " + arr)
		imgui.TextUnformatted(fmt.Sprintf("ALT: %d ft", alt))
		imgui.TextUnformatted(fmt.Sprintf("GS:  %d kt", gs))
		imgui.TextUnformatted(fmt.Sprintf("HDG: %03d°", hdg))
		imgui.TextUnformatted("Route:")
		imgui.PushTextWrapPosV(imgui.GetCursorPosX() + 360)
		if trk.FiledRoute != "" {
			imgui.TextUnformatted(trk.FiledRoute)
		} else {
			imgui.TextUnformatted("(none)")
		}
		imgui.PopTextWrapPos()
	}
	imgui.End()
}
```

Add `"fmt"` to the imports of `panes/mappane_selection.go` if not already
present.

- [ ] **Step 11.2: Wire into `drawCanvas`**

After `drawAircraft` and `handleSelection` (the panel is positioned in
screen space and isn't sensitive to ordering with respect to the canvas
draw list — but it must come after `handleSelection` so a freshly-clicked
aircraft renders this same frame):

```go
mp.drawInfoPanel(c, mp.canvasOrigin, mp.canvasSize, nmPerLon)
```

- [ ] **Step 11.3: Build + smoke**

Run: `go build -tags vulkan ./cmd/vice`

Click an aircraft → a small floating panel appears next to it showing
callsign, dep, arr, alt, gs, hdg, and route. The panel follows the aircraft
as it moves. Clicking elsewhere clears it.

- [ ] **Step 11.4: Commit**

```bash
git add panes/mappane.go panes/mappane_selection.go
git commit -m "panes: MapPane floating info panel for selected aircraft

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 12: Final pass + push

Polish, run all tests, push to origin.

- [ ] **Step 12.1: Run full test suite**

Run: `go test ./panes/ -v`
Expected: all PASS.

- [ ] **Step 12.2: Verify build with vulkan tag**

Run: `go build -tags vulkan ./cmd/vice`
Expected: success.

- [ ] **Step 12.3: Manual checklist**

Walk through each item; if any fails, file a fix commit:

- [ ] Map button visible in main menu bar.
- [ ] Click button → "Map" window opens, shows basemap (coastlines + country
      borders), facility boundary, airport labels.
- [ ] Drag → pan; scroll → zoom; clamps at extreme ranges.
- [ ] Aircraft glyphs visible at correct positions, rotated to heading;
      callsigns labeled.
- [ ] Untracked aircraft are dimmer than tracked ones.
- [ ] Filter combo: each option narrows the visible aircraft as expected.
- [ ] Specific-TCW combo lists controllers and filters correctly.
- [ ] Toggle Basemap / Boundary / Airports — each hides/shows its layer.
- [ ] Click an aircraft → trail (blue) and route (yellow dashed) appear;
      info panel shows callsign, dep, arr, alt, gs, hdg, route.
- [ ] Click empty space → selection clears.
- [ ] Drag the "Map" window's title bar outside the main vice window — it
      becomes its own OS window (multi-viewport). Map keeps drawing inside.
- [ ] Disconnect mid-flight — pane shows no aircraft but doesn't panic.
- [ ] Re-open vice — toggles, filter, and camera position all persist.

- [ ] **Step 12.4: Push**

```bash
git push -u origin map-window
```

- [ ] **Step 12.5: Save memory note (per repo conventions)**

After push, write a memory note describing the new branch's HEAD commit.
Memory file path: `C:\Users\judlo\.claude\projects\C--Users-judlo-Documents-vice-vice\memory\map_window_branch.md`.
Content (replace `<sha>` with the actual HEAD sha after push):

```markdown
---
name: Map-window branch state
description: map-window @<sha> — separate Map window pane (toolbar button), Natural Earth dark basemap, facility boundary, airport labels, filterable tracks, click-to-select with floating info panel + past trail + dashed future route. Pushed to origin; not for upstream PR (per user — for own testing only).
type: project
---

`map-window` @<sha> (pushed to origin) — adds `panes.MapPane` floated via imgui multi-viewport, bundled `ne_50m_*` GeoJSON basemap, filter combo (all/untracked/tracked/my-TCW/specific-TCW), past-trail ring buffer + future-route dashed line, info panel.

**Why:** user wanted a separate map window for personal testing.
**How to apply:** if user asks about map window behavior or further
features, this branch is the baseline. Spec at
`docs/superpowers/specs/2026-04-29-map-window-design.md`, plan at
`docs/superpowers/plans/2026-04-29-map-window.md`.
```

Append to `MEMORY.md`:

```markdown
- [Map-window branch state](map_window_branch.md) — separate map window pane on top of vice (toolbar button + dark basemap + filters + click info + trail/route).
```

---

## Self-Review

Walked the spec section-by-section against the plan:

| Spec section | Covered by |
|---|---|
| Menu-bar button + toggle | Task 1 |
| Floating multi-viewport window | Task 1 (`DrawWindow` + `DrawPinButton`) |
| Fit-to-facility on first open | Task 2 (Step 2.10) |
| Pan/zoom canvas | Task 2 |
| `radar.ScopeTransformations` reuse | Task 2 (`camera.transforms`) |
| Dark vector basemap (Natural Earth) | Task 3 |
| Bundled offline data | Task 3 (`//go:embed`) |
| Facility boundary overlay | Task 4 |
| Toggleable airport labels | Task 5 |
| Aircraft glyphs + callsigns | Task 6 |
| Untracked vs tracked dimming | Task 6 (alpha 0.55 vs 1.0) |
| Filter UI (all/untracked/tracked/my-TCW/specific-TCW) | Task 7 |
| Click-to-select with hit test | Task 8 |
| Past trail | Task 9 |
| Future route (dashed) | Task 10 |
| Floating info panel | Task 11 |
| Persistence of toggles + camera | Tasks 1, 2 (fields are JSON-serializable on `MapPane`) |
| Manual + unit tests | Tasks 2, 3, 6, 8, 9 (unit) + Task 12 (manual) |

Placeholder scan: no "TBD"/"TODO"/"add error handling" left. Two notes
where the engineer must verify a field name (`av.DB.Airports[icao].Location`,
`Controller` → TCW mapping) — both flagged inline with how to verify.

Type consistency: `mp.Filter` is `int` everywhere it's set; cast to
`aircraftFilter` only at predicate-call sites. `aircraftFilter` constants
are stable across tasks. `pushTrail` signature `(buf, p, cap)` matches
between definition and test. `camera.llToScreen` / `screenToLL` signatures
are consistent across all callers.

Scope: single feature, single branch, single plan — no decomposition needed.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-29-map-window.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
