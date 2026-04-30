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
// (month, weekday) summed across `airports`. Bottom slice = arrivals
// (blue), top = departures (orange). Hovering a bar shows a per-airport
// breakdown tooltip. Returns the bucket index 0..95 the user clicked,
// or -1 if no click.
func drawScheduleHistogram(sch *schedule.Schedule, month time.Month, day time.Weekday,
	airports []string, width, height float32) int {

	if sch == nil || len(airports) == 0 {
		imgui.Dummy(imgui.Vec2{X: width, Y: height})
		return -1
	}

	// Build a synthetic date with the given month + weekday so
	// AggregateForDay walks the right buckets.
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

	// Reserve canvas first so the modal auto-sizes; then read the actual
	// rect via ItemRectMin/Max and draw to the current window's viewport
	// foreground draw list (multi-viewport-safe).
	imgui.InvisibleButtonV("##schedhist", imgui.Vec2{X: width, Y: height}, imgui.ButtonFlagsMouseButtonLeft)
	hovered := imgui.IsItemHovered()
	clicked := hovered && imgui.IsMouseClickedBool(imgui.MouseButtonLeft)
	rectMin := imgui.ItemRectMin()
	rectMax := imgui.ItemRectMax()
	pos := rectMin
	actualW := rectMax.X - rectMin.X
	actualH := rectMax.Y - rectMin.Y

	dl := imgui.WindowDrawList()

	// Background.
	dl.AddRectFilled(pos, rectMax,
		imgui.ColorU32Vec4(imgui.Vec4{X: 0.10, Y: 0.10, Z: 0.12, W: 1}))

	depColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.95, Y: 0.65, Z: 0.30, W: 1}) // orange
	arrColor := imgui.ColorU32Vec4(imgui.Vec4{X: 0.30, Y: 0.65, Z: 0.95, W: 1}) // blue

	barW := actualW / 96.0

	for i, b := range totals {
		x0 := pos.X + float32(i)*barW
		x1 := x0 + barW - 1
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
	if hovered && barW > 0 {
		hoverIdx = int((mouse.X - pos.X) / barW)
		if hoverIdx < 0 || hoverIdx >= 96 {
			hoverIdx = -1
		}
	}

	if hoverIdx >= 0 {
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
