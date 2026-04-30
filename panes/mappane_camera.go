// pkg/panes/mappane_camera.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"github.com/mmp/vice/math"
)

const (
	minRangeNM = 0.5
	maxRangeNM = 1500.0

	// defaultNmPerLongitude is the fallback nm-per-longitude used before a sim
	// connects. Calibrated for ~40°N (60 * cos(40°) ≈ 45.96 nm/° lon); the real
	// value from the connected sim's State.NmPerLongitude is used as soon as
	// available.
	defaultNmPerLongitude = float32(45.5)
)

type camera struct {
	center  math.Point2LL
	rangeNM float32
}

// scopeXforms holds the two matrices needed for lat/lon <-> window
// coordinate conversion. It mirrors the relevant subset of
// radar.ScopeTransformations without importing the radar package (which
// itself imports panes, creating a cycle).
//
// ndcFromLatLong and ndcFromWindow are intentionally omitted: the map pane
// renders into an imgui draw list (screen pixels), not a GL command buffer,
// so the NDC matrices aren't needed.
type scopeXforms struct {
	latLongFromWindow math.Matrix3
	windowFromLatLong math.Matrix3
}

// WindowFromLatLongP transforms a lat/lon point to window (pane) coordinates.
func (st *scopeXforms) WindowFromLatLongP(p math.Point2LL) [2]float32 {
	return st.windowFromLatLong.TransformPoint(p)
}

// LatLongFromWindowP transforms window coordinates to lat/lon.
func (st *scopeXforms) LatLongFromWindowP(p [2]float32) math.Point2LL {
	return st.latLongFromWindow.TransformPoint(p)
}

// LatLongFromWindowV transforms a window-space vector to a lat/lon vector.
func (st *scopeXforms) LatLongFromWindowV(v [2]float32) math.Point2LL {
	return st.latLongFromWindow.TransformVector(v)
}

// transforms builds the coordinate-system matrices for this camera state,
// matching the math in radar.GetScopeTransformations (north-up, no rotation).
func (c *camera) transforms(paneExtent math.Extent2D, nmPerLongitude float32) scopeXforms {
	width, height := paneExtent.Width(), paneExtent.Height()
	aspect := width / height

	ndcFromLatLong := math.Identity3x3().
		Ortho(-aspect, aspect, -1, 1).
		Scale(nmPerLongitude/c.rangeNM, math.NMPerLatitude/c.rangeNM).
		Translate(-c.center[0], -c.center[1])

	ndcFromWindow := math.Identity3x3().
		Translate(-1, -1).
		Scale(2/width, 2/height)

	latLongFromNDC := ndcFromLatLong.Inverse()
	latLongFromWindow := latLongFromNDC.PostMultiply(ndcFromWindow)
	windowFromLatLong := latLongFromWindow.Inverse()

	return scopeXforms{
		latLongFromWindow: latLongFromWindow,
		windowFromLatLong: windowFromLatLong,
	}
}

// applyZoomFactor multiplies the camera's range by `factor` and clamps to
// [minRangeNM, maxRangeNM]. Use factor < 1 to zoom in, > 1 to zoom out.
func (c *camera) applyZoomFactor(factor float32) {
	c.rangeNM *= factor
	if c.rangeNM < minRangeNM {
		c.rangeNM = minRangeNM
	}
	if c.rangeNM > maxRangeNM {
		c.rangeNM = maxRangeNM
	}
}

// applyPanPixels shifts the camera center to compensate for a screen-space
// drag of (dx, dy) pixels (imgui convention: +y is down). After the call,
// the lat/lon under the original mouse position is at the new mouse position.
func (c *camera) applyPanPixels(dx, dy float32, canvasSize [2]float32, nmPerLongitude float32) {
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: canvasSize}
	xforms := c.transforms(extent, nmPerLongitude)
	dll := xforms.LatLongFromWindowV([2]float32{dx, -dy}) // imgui Y down → ll Y up
	c.center[0] -= dll[0]
	c.center[1] -= dll[1]
}

// llToScreen returns a screen-space point in imgui coordinates (top-left origin)
// for a lat/lon, given the canvas origin and size.
func (c *camera) llToScreen(p math.Point2LL, canvasOrigin, canvasSize [2]float32, nmPerLongitude float32) [2]float32 {
	extent := math.Extent2D{P0: [2]float32{0, 0}, P1: canvasSize}
	xforms := c.transforms(extent, nmPerLongitude)
	pp := xforms.WindowFromLatLongP(p)
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
