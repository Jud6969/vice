// cmd/vice/schedhist.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/mmp/vice/sim/schedule"

	"github.com/AllenDang/cimgui-go/imgui"
)

// drawScheduleHistogram renders 96 stacked 15-min bars for the picked
// (month, weekday) summed across `airports`. Bottom slice = arrivals,
// top = departures. Hovering a bar shows a per-airport breakdown tooltip.
// Returns the bucket index 0..95 the user clicked, or -1 if no click.
func drawScheduleHistogram(sch *schedule.Schedule, month time.Month, day time.Weekday,
	airports []string, width, height float32) int {

	if sch == nil || len(airports) == 0 {
		imgui.Dummy(imgui.Vec2{X: width, Y: height})
		return -1
	}

	// Build a synthetic date with the given month + weekday so AggregateForDay
	// (which uses time.Date) walks the right buckets.
	year := time.Now().Year()
	candidate := time.Date(year, month, 1, 0, 0, 0, 0, time.UTC)
	for candidate.Weekday() != day {
		candidate = candidate.AddDate(0, 0, 1)
	}
	totals := sch.AggregateForDay(candidate, airports)

	var maxH float32
	for _, b := range totals {
		if h := b.Dep + b.Arr; h > maxH {
			maxH = h
		}
	}
	if maxH < 1 {
		maxH = 1
	}

	// Reserve the canvas first so imgui can auto-size the modal correctly.
	// Then read the FINAL screen rect via GetItemRectMin/Max — this is the
	// only reliable way to know where to draw, since CursorScreenPos
	// captured before the InvisibleButton can be stale relative to the
	// modal's post-layout position.
	imgui.InvisibleButtonV("##schedhist", imgui.Vec2{X: width, Y: height}, imgui.ButtonFlagsMouseButtonLeft)
	hovered := imgui.IsItemHovered()
	clicked := hovered && imgui.IsMouseClickedBool(imgui.MouseButtonLeft)
	rectMin := imgui.ItemRectMin()
	rectMax := imgui.ItemRectMax()
	pos := rectMin
	actualW := rectMax.X - rectMin.X
	actualH := rectMax.Y - rectMin.Y

	// Multi-viewport: the configuration modal may live in its own OS
	// viewport (separate from the main vice window). Draw to the
	// current WINDOW's viewport's foreground DL so bars render on the
	// same surface as the modal — the unparametered ForegroundDrawListViewportPtr()
	// returns the MAIN viewport, which would put bars on the main
	// window underneath.
	dl := imgui.ForegroundDrawListViewportPtrV(imgui.WindowViewport())

	// Background.
	dl.AddRectFilled(pos, rectMax,
		imgui.ColorU32Vec4(imgui.Vec4{X: 0.10, Y: 0.10, Z: 0.12, W: 1}))

	depColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.95, Y: 0.65, Z: 0.30, W: 1}) // departures = orange
	arrColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.30, Y: 0.65, Z: 0.95, W: 1}) // arrivals = blue

	barW := actualW / 96.0

	for i, b := range totals {
		x0 := pos.X + float32(i)*barW
		x1 := x0 + barW - 1
		// Arrivals: stacked at the bottom.
		hArr := actualH * (b.Arr / maxH)
		hDep := actualH * (b.Dep / maxH)
		dl.AddRectFilled(
			imgui.Vec2{X: x0, Y: pos.Y + actualH - hArr},
			imgui.Vec2{X: x1, Y: pos.Y + actualH},
			arrColor)
		dl.AddRectFilled(
			imgui.Vec2{X: x0, Y: pos.Y + actualH - hArr - hDep},
			imgui.Vec2{X: x1, Y: pos.Y + actualH - hArr},
			depColor)
	}

	mouse := imgui.MousePos()
	hoverIdx := -1
	if hovered {
		hoverIdx = int((mouse.X - pos.X) / barW)
		if hoverIdx < 0 || hoverIdx >= 96 {
			hoverIdx = -1
		}
	}

	if hoverIdx >= 0 {
		// Tooltip: bucket label + per-airport breakdown.
		bucketTime := time.Date(candidate.Year(), candidate.Month(), candidate.Day(),
			hoverIdx/4, (hoverIdx%4)*15, 0, 0, time.UTC)
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s %02d:%02d", weekdayShort(day), hoverIdx/4, (hoverIdx%4)*15)
		sb.WriteString("\n")
		for _, icao := range airports {
			dep, arr := sch.RateAt(bucketTime, icao)
			if dep == 0 && arr == 0 {
				continue
			}
			fmt.Fprintf(&sb, "%s  arr %d  dep %d\n", icao, int(arr+0.5), int(dep+0.5))
		}
		imgui.SetTooltip(sb.String())
	}

	if clicked && hoverIdx >= 0 {
		return hoverIdx
	}
	return -1
}

func weekdayShort(d time.Weekday) string {
	switch d {
	case time.Monday:
		return "MON"
	case time.Tuesday:
		return "TUE"
	case time.Wednesday:
		return "WED"
	case time.Thursday:
		return "THU"
	case time.Friday:
		return "FRI"
	case time.Saturday:
		return "SAT"
	case time.Sunday:
		return "SUN"
	}
	return ""
}
