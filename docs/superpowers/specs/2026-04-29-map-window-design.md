# Map Window — Design

**Branch:** `map-window`
**Status:** Spec — pending implementation plan

## Goal

Add a separate, optionally-floated "map" view that lets the user see all aircraft
in the sim on a world map. Toggled from a button in the top menu bar. Provides
pan/zoom, a dark vector basemap, facility boundaries, toggleable airport labels,
filterable aircraft, click-to-inspect, and per-aircraft past-trail / future-route
visualization.

This is for the user's own testing — not destined for upstream PR.

## Non-Goals

- No real satellite raster tiles, no tile cache, no network fetcher.
- Not replacing or duplicating STARS scope functionality (no datablocks, no CA/MSAW,
  no commands, no track history dots beyond the simple trail described below).
- No new persisted user state beyond the standard pane config (window pos / zoom /
  toggles can persist via the existing pane-config mechanism if cheap, otherwise
  reset on app restart).

## User-facing summary

1. A new map-icon button appears in the main menu bar (`cmd/vice/ui.go`), beside
   the existing toggle buttons (messages, flight strips, etc.). Clicking it toggles
   `ui.showMap`.
2. When shown, an imgui window titled "Map" appears containing the `MapPane`. The
   window is undockable / draggable out of the main window via imgui multi-viewport
   (already enabled at `cmd/vice/ui.go:84`), so it can live on a second monitor.
3. The map opens fit-to-facility on first show. Drag-to-pan, scroll-to-zoom.
4. The user sees aircraft as rotated plane glyphs with callsign labels. Untracked
   vs tracked aircraft are visually distinguished. A small filter dropdown in the
   pane's header narrows the visible set.
5. Clicking an aircraft anchors a floating info panel to it (callsign, dep, arr,
   route, altitude, ground speed, heading) and overlays its past trail (dim solid)
   plus its remaining route (bright dashed). Clicking elsewhere clears selection.
6. A small toolbar in the pane header has toggles for: airport labels on/off,
   facility boundaries on/off, world basemap on/off.

## Architecture

### Components

```
cmd/vice/ui.go
  └─ adds ui.showMap, map-button in menu bar, drives MapPane visibility
     via the same pattern used by Messages / FlightStrips windows.

panes/mappane/                 (new package — kept out of panes/ root because
   ├─ mappane.go                MapPane is self-contained and doesn't share
   │      type MapPane struct   internal helpers with messages/flightstrip)
   │      Activate / Draw / etc.
   ├─ camera.go                 pan/zoom state + lat-lon ⇄ screen helpers
   ├─ basemap.go                Natural Earth coastlines/borders loader + draw
   ├─ aircraft.go               filter logic + plane-glyph + label draw
   ├─ selection.go              click hit-test, info panel, trail + route draw
   ├─ overlays.go               airport labels + facility boundaries
   └─ config.go                 persisted toggles (labels on/off, etc.)

resources/mapdata/             (new — bundled offline vector data)
   ├─ ne_coastline.bin          packed binary form of Natural Earth 1:50m
   ├─ ne_admin0.bin              coastlines + country borders + admin-1 (states)
   └─ ne_admin1.bin
```

### Data flow

Per frame:

1. `MapPane.Draw(ctx, cb)` reads `ctx.ControlClient.State` for the current
   `Tracks` map (already has `Location`, `Heading`, `Groundspeed`, owner) and
   `Aircraft` map for the underlying flight plans / nav state used for the
   future-route line.
2. Camera state (pane-local `center [2]float32` (lat,lon), `zoom` nm/pixel) is
   mutated by mouse events drained from `ctx`.
3. Render order, all to the single `renderer.CommandBuffer`:
   1. Clear to dark navy/black background.
   2. Basemap polylines (coastlines, admin borders) — culled to view extent.
   3. Facility boundaries (from `controlClient.State` airspace volumes — same
      data STARS already uses for `drawTRACONBoundary` in
      `stars/stars.go:1380`).
   4. Airport labels (if enabled) — iterate `controlClient.State.Airports`
      using existing facility metadata; "common" = airports referenced by the
      scenario's departures/arrivals.
   5. Selected aircraft past-trail + future-route (if any).
   6. Aircraft glyphs + callsign labels for the filtered set.
   7. Selected aircraft info panel (imgui floating popup anchored to aircraft
      screen coord, drawn via `imgui.SetNextWindowPos` so it follows the
      target).

### Coordinate system

`MapPane` uses its own simple equirectangular projection rather than reusing
`radar.ScopeTransformations`, because:

- `ScopeTransformations` is parameterized for STARS-style window coords with
  magnetic variation, nm-per-longitude, and a center anchored to facility
  origin. It works, but the API is wider than we need and assumes a single
  STARS reference frame.
- A standalone equirectangular projection (lon→x, lat→y, scaled by
  `cos(centerLat)` for x) is ~30 lines and is what every web map (including
  vatsim-radar) uses at this zoom range.

Camera state in `MapPane`:

```go
type camera struct {
    center [2]float32 // lon, lat
    nmPerPixel float32
}
func (c *camera) ll2screen(p math.Point2LL, paneExtent math.Extent2D) [2]float32
func (c *camera) screen2ll([2]float32, paneExtent math.Extent2D) math.Point2LL
```

### Filter UI

Filter state on `MapPane`:

```go
type filter int
const (
    filterAll filter = iota
    filterUntracked
    filterTrackedAny
    filterMyTCP
    filterTCP // pairs with filterTCPCallsign
)
type MapPane struct {
    ...
    filter         filter
    filterTCPID    string // controller position id when filter == filterTCP
    showAirports   bool   // default true
    showBoundaries bool   // default true
    showBasemap    bool   // default true
}
```

A small imgui combo at the top of the pane drives `filter`. When `filterTCP` is
selected, a second combo lists controller positions from
`controlClient.State.Controllers`.

Filter predicates evaluated per-aircraft per-frame:

| filter | predicate |
|---|---|
| All | always true |
| Untracked | track has no associated flight plan, or its tracking-controller field is empty |
| Tracked (any) | track has a flight plan with a non-empty tracking-controller |
| My TCP | tracked && tracking-controller == user's TCP |
| Specific TCP | tracked && tracking-controller == `filterTCPID` |

(Exact field name on `sim.Track` / `NASFlightPlan` to be confirmed during planning —
the relevant accessor is whatever STARS uses today to decide track ownership.)

Untracked aircraft (when shown) are drawn dimmer than tracked ones — same glyph,
~50% alpha — so the visual distinction holds even with `filterAll`.

### Aircraft rendering

Each visible aircraft is one drawcall set:

- A plane-shaped glyph (FontAwesome `FontAwesomeIconPlane` or a hand-rolled
  triangle if rotation against a font glyph proves ugly) drawn rotated so its
  nose points along `track.Heading`.
- Callsign text rendered in `ctx.Font` immediately to the right of the glyph.

Reuses existing `renderer.TexturedTrianglesDrawBuilder` for the glyph (same
mechanism the STARS pane uses for rotated track symbols) and
`renderer.TextDrawBuilder` for the label.

### Selection

Mouse click → hit-test against each aircraft's glyph bounding box (in screen
space). If a hit:

1. Set `selectedCallsign = track.ADSBCallsign`.
2. On every subsequent frame, draw:
   - **Past trail:** ring-buffer of last `N` track positions (`N = ~120` =
     ~2 minutes at vice's 1Hz radar tick). Stored on `MapPane` as
     `pastTrails map[av.ADSBCallsign][]math.Point2LL` and updated each
     frame for *all* visible aircraft so the trail is already populated by
     the time the user clicks.
   - **Future route:** poll `aircraft.Nav.Waypoints` (already computed by
     the nav engine) plus the arrival airport position; draw as a dashed
     polyline.
   - **Info panel:** an imgui window with `NoTitleBar | NoResize | NoMove |
     AlwaysAutoResize` set, positioned via `SetNextWindowPos` near the
     aircraft's screen coord (offset so it doesn't overlap the glyph).
     Closes on next click that hits empty space.

Click outside any aircraft → `selectedCallsign = ""`.

### Basemap

Natural Earth 1:50m physical (coastline) + cultural (admin-0, admin-1) data,
preprocessed at build time into a flat binary of `[]float32` lon/lat pairs
grouped by polyline, with a small per-polyline bounding box for view-frustum
culling. Loaded once at app start and cached in package-level state.

Why not GeoJSON at runtime: the JSON parse is ~100ms+ on first paint and the
data never changes — flat binary is faster and simpler.

A `cmd/buildmapdata/main.go` tool (small, one-off) converts Natural Earth
GeoJSON into the bundled binary. Run manually; the binaries get checked into
`resources/mapdata/`.

Rendered as thin (1px) lines:
- Coastlines: light gray (e.g. `#5a6470`).
- Admin-0 (country borders): same.
- Admin-1 (state/province): dimmer (`#3a4048`), shown only when zoomed in
  enough that they're meaningful (cutoff at e.g. `nmPerPixel < 0.5`).

### Facility boundaries

Reuse `controlClient.State.STARSFacilityAdaptation` (or equivalent) — the
same data STARS already iterates in `stars/stars.go:1380`
(`drawTRACONBoundary`). Render as a single-color polyline overlay, brighter
than basemap (e.g. `#8090a0`).

### Airport labels

"Common airports for the scenario" = the union of:
- `controlClient.State.DepartureAirports`
- `controlClient.State.ArrivalAirports`
- any airport that appears as an arrival fix in active flight plans.

For each, look up its lat/lon in the existing aviation DB (`aviation/db.go`)
and render the 4-letter ICAO code as a label at that position. Toggle via the
`showAirports` checkbox in the pane header.

## Error handling

- If basemap binary is missing at startup, log a warning and render the map
  without a basemap (everything else still works). No fatal.
- If the user opens the map window before `controlClient` is connected, the
  pane renders the basemap + a "Not connected" overlay; aircraft list is
  empty.
- Selected aircraft disappears (deleted from sim): clear selection silently.

## Testing

- **Manual:** start a scenario, click the map button, verify:
  1. Map opens fit-to-facility, basemap and facility boundary visible.
  2. Aircraft glyphs appear at correct positions, rotated to heading.
  3. Pan/zoom works; airports labels appear when toggled on.
  4. Filter combo narrows the visible set as documented above.
  5. Clicking an aircraft shows the info panel with correct dep/arr/route/
     alt/speed/heading. Past trail + dashed future route appear.
  6. Drag the imgui window outside the main window → it becomes its own OS
     window (multi-viewport).
  7. Disconnect mid-flight → "Not connected" appears, no panic.
- **Unit:** projection roundtrip (`screen2ll(ll2screen(p)) ≈ p` within tolerance);
  filter predicate matrix; basemap binary loader on a small fixture file.
- **Build:** `go build -tags vulkan ./cmd/vice` must succeed (per machine memory).

## Open items / risks

- **Plane glyph rotation against a font glyph** may look pixelated. If the
  FontAwesome plane doesn't rotate cleanly, fall back to a 3-vertex triangle
  drawn in `LinesDrawBuilder` / `TrianglesDrawBuilder`.
- **Multi-viewport on Windows** is enabled but rarely exercised by this app —
  if the floating window misbehaves (e.g. fonts not loading in the secondary
  viewport), document the workaround and keep the pane usable while docked.
- **Past-trail memory growth:** capped at `N=120` points × ~50 active aircraft
  × 8 bytes/coord ≈ ~50 KB. Negligible. Aircraft removed from the sim must
  have their entries pruned from `pastTrails` each frame.
- **Building the Natural Earth binary** is a one-time chore. The conversion
  tool is small but not tested broadly. Will validate output by-eye on the
  first run.
