// pinbutton.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"github.com/mmp/vice/platform"
	"github.com/mmp/vice/renderer"

	"github.com/AllenDang/cimgui-go/imgui"
)

// DrawTitleBar draws a custom title bar at the top of the current imgui
// window. Intended for windows that set WindowFlagsNoTitleBar so imgui
// does not draw its own. Returns true if the user clicked the close (X)
// button this frame.
//
//   - title:          text label shown on the left
//   - pinWindowTitle: key used for the pin-thumbtack's UnpinnedWindows set
//     (usually the same as title; pass "" to skip the pin button)
//   - unpinnedWindows: the set of currently-unpinned window titles
//   - p:              platform handle (pin button uses it for focus state)
//
// Dragging within the title bar's empty space moves the imgui window.
// Must be called immediately after imgui.BeginV.
func DrawTitleBar(title, pinWindowTitle string,
	unpinnedWindows map[string]struct{}, p platform.Platform) (closed bool) {
	style := imgui.CurrentStyle()
	lineHeight := imgui.TextLineHeight() + 2*style.FramePadding().Y
	closeBtnWidth := imgui.CalcTextSize(renderer.FontAwesomeIconTimes).X + 2*style.FramePadding().X

	// Title bar background. Use ImGui's title-bar color so it blends.
	drawList := imgui.WindowDrawList()
	winPos := imgui.WindowPos()
	winSize := imgui.WindowSize()
	barMin := winPos
	barMax := imgui.Vec2{X: winPos.X + winSize.X, Y: winPos.Y + lineHeight}
	barCol := imgui.ColorU32Col(imgui.ColTitleBg)
	if imgui.IsWindowFocused() {
		barCol = imgui.ColorU32Col(imgui.ColTitleBgActive)
	}
	drawList.AddRectFilled(barMin, barMax, barCol)

	// Title text on the left, vertically centered in the bar.
	imgui.SetCursorPos(imgui.Vec2{X: style.FramePadding().X, Y: style.FramePadding().Y})
	imgui.TextUnformatted(title)

	// Right-cluster: optional pin button, then close button.
	// DrawPinButton positions itself via ForegroundDrawList (it ignores
	// the imgui cursor), so we just call it when we need it and position the
	// close button directly from the right edge.
	if pinWindowTitle != "" {
		DrawPinButton(pinWindowTitle, unpinnedWindows, p)
	}
	rightX := winSize.X - closeBtnWidth - style.FramePadding().X
	imgui.SetCursorPos(imgui.Vec2{X: rightX, Y: style.FramePadding().Y})
	imgui.PushStyleColorVec4(imgui.ColButtonHovered, imgui.Vec4{0.85, 0.15, 0.15, 1})
	imgui.PushStyleColorVec4(imgui.ColButtonActive, imgui.Vec4{0.7, 0.1, 0.1, 1})
	if imgui.Button(renderer.FontAwesomeIconTimes) {
		closed = true
	}
	imgui.PopStyleColorV(2)

	// Drag-to-move: if the user clicks-and-drags in the title-bar strip
	// (and not on a button), move the imgui window.
	mouse := imgui.MousePos()
	inBar := mouse.X >= barMin.X && mouse.X <= barMax.X &&
		mouse.Y >= barMin.Y && mouse.Y <= barMax.Y
	if inBar && !imgui.IsAnyItemActive() && imgui.IsMouseDraggingV(imgui.MouseButtonLeft, 0) {
		delta := imgui.MouseDragDeltaV(imgui.MouseButtonLeft, 0)
		imgui.ResetMouseDragDeltaV(imgui.MouseButtonLeft)
		imgui.SetWindowPosVec2(imgui.Vec2{X: winPos.X + delta.X, Y: winPos.Y + delta.Y})
	}

	// Reserve vertical space below the title bar so the caller's content
	// starts below it, not underneath.
	imgui.SetCursorPos(imgui.Vec2{X: 0, Y: lineHeight})
	return closed
}
