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

func (mp *MapPane) drawBasemap(cam camera, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32, lg *log.Logger) {
	if !mp.ShowBasemap {
		return
	}
	lines, err := loadBasemap()
	if err != nil {
		lg.Warnf("basemap load failed: %v", err)
		return
	}

	view := mp.viewExtent(cam, canvasSize, nmPerLongitude)

	color := imgui.ColorU32Vec4(imgui.Vec4{X: 0.30, Y: 0.36, Z: 0.42, W: 1})

	// Reuse a per-call buffer to avoid allocating per polyline; basemap polylines
	// are drawn many times per frame.
	var screenPts []imgui.Vec2

	for _, pl := range lines {
		if !math.Overlaps(view, pl.bounds) {
			continue
		}
		if len(pl.pts) < 2 {
			continue
		}
		screenPts = screenPts[:0]
		for _, p := range pl.pts {
			s := cam.llToScreen(p, canvasOrigin, canvasSize, nmPerLongitude)
			screenPts = append(screenPts, imgui.Vec2{X: s[0], Y: s[1]})
		}
		mp.canvasDrawList.AddPolyline(&screenPts[0], int32(len(screenPts)), color, imgui.DrawFlagsNone, 1.0)
	}
}

// viewExtent returns the lat-lon bounding box currently visible in the canvas.
func (mp *MapPane) viewExtent(cam camera, canvasSize [2]float32, nmPerLongitude float32) math.Extent2D {
	corners := [4][2]float32{
		{0, 0}, {canvasSize[0], 0}, {canvasSize[0], canvasSize[1]}, {0, canvasSize[1]},
	}
	var ext math.Extent2D
	for i, c := range corners {
		// canvas-local coords intentionally pair with zero origin in screenToLL —
		// the Y-flip and origin offset are inverses of each other, so passing the
		// actual canvasOrigin would double-cancel.
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
