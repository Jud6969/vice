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

// drawScheduleHistogram renders 96 15-min bars (combined arr+dep) for the
// picked (month, weekday) summed across `airports`. Hovering a bar shows a
// per-airport breakdown tooltip. Returns the bucket index 0..95 the user
// clicked, or -1 if no click.
//
// Implementation note: we use imgui.PlotHistogramFloatPtrV (the built-in
// imgui histogram) rather than custom draw-list calls. Earlier custom
// approaches via WindowDrawList / ForegroundDrawListViewportPtr / etc.
// failed to render bars inside vice's modal popup (probably a multi-
// viewport draw-channel interaction). The built-in widget Just Works
// across viewports and clip rects. We trade dual-color stacking for
// reliability — the tooltip still breaks down arr vs dep per airport.
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

	// Build a flat float32 array of combined arr+dep totals for PlotHistogram.
	values := make([]float32, 96)
	var maxH float32
	for i, b := range totals {
		v := b.Arr + b.Dep
		values[i] = v
		if v > maxH {
			maxH = v
		}
	}
	if maxH < 1 {
		maxH = 1
	}

	imgui.PlotHistogramFloatPtrV("##schedhist", &values[0], 96, 0, "",
		0, maxH, imgui.Vec2{X: width, Y: height}, 4)

	hovered := imgui.IsItemHovered()
	clicked := hovered && imgui.IsMouseClickedBool(imgui.MouseButtonLeft)
	rectMin := imgui.ItemRectMin()
	rectMax := imgui.ItemRectMax()

	mouse := imgui.MousePos()
	barW := (rectMax.X - rectMin.X) / 96.0
	hoverIdx := -1
	if hovered && barW > 0 {
		hoverIdx = int((mouse.X - rectMin.X) / barW)
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
