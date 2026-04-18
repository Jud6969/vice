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
//   - lockedWindows:  set of windows whose position is locked; when non-nil
//     a lock toggle is drawn left of the close button and drag is skipped
//     when the window's pinWindowTitle is in the set. Pass nil to omit.
//   - p:              platform handle (pin button uses it for focus state)
//
// Dragging within the title bar's empty space moves the imgui window
// (unless locked). Must be called immediately after imgui.BeginV.
func DrawTitleBar(title, pinWindowTitle string,
	unpinnedWindows, lockedWindows map[string]struct{}, p platform.Platform) (closed bool) {
	style := imgui.CurrentStyle()
	lineHeight := imgui.TextLineHeight() + 2*style.FramePadding().Y

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

	// Optional pin button, left of the close button. Positions itself via
	// ForegroundDrawList.
	if pinWindowTitle != "" {
		DrawPinButton(pinWindowTitle, unpinnedWindows, p)
	}

	// Close button: draw via ForegroundDrawList + manual hit-test so it
	// doesn't contribute to imgui's ItemMax. Using imgui.Button here with
	// WindowFlagsAlwaysAutoResize would feed back into winSize.X each
	// frame and grow the window indefinitely.
	closeIcon := renderer.FontAwesomeIconTimes
	closeIconSize := imgui.CalcTextSize(closeIcon)
	closeX := winPos.X + winSize.X - closeIconSize.X - style.FramePadding().X
	closeY := winPos.Y + (lineHeight-closeIconSize.Y)*0.5
	hitPad := float32(4)
	closeMin := imgui.Vec2{X: closeX - hitPad, Y: closeY - hitPad}
	closeMax := imgui.Vec2{X: closeX + closeIconSize.X + hitPad, Y: closeY + closeIconSize.Y + hitPad}

	mouse := imgui.MousePos()
	// Gate the close-button hit-test on IsWindowHovered so a click on a
	// different window (whose global screen position happens to land
	// inside this window's close rect after a viewport transition)
	// doesn't spuriously fire closed=true here. IsWindowHovered is imgui's
	// own per-window hover tracking and handles viewport topology.
	windowHovered := imgui.IsWindowHovered()
	closeHovered := windowHovered &&
		mouse.X >= closeMin.X && mouse.X <= closeMax.X &&
		mouse.Y >= closeMin.Y && mouse.Y <= closeMax.Y

	fgDraw := imgui.ForegroundDrawListViewportPtr()
	if closeHovered {
		hoverCol := imgui.ColorU32Vec4(imgui.Vec4{X: 0.85, Y: 0.15, Z: 0.15, W: 1})
		fgDraw.AddRectFilled(closeMin, closeMax, hoverCol)
	}
	fgDraw.AddTextVec2(imgui.Vec2{X: closeX, Y: closeY}, imgui.ColorU32Col(imgui.ColText), closeIcon)

	if closeHovered && imgui.IsMouseClickedBool(0) {
		closed = true
	}

	// Optional lock toggle, drawn left of the pin thumbtack. Same
	// ForegroundDrawList + manual hit-test pattern as the close button
	// for the same reason (keep it out of imgui's ItemMax so it doesn't
	// feed back into AlwaysAutoResize). Position matches the formula in
	// DrawPinButton (panes.go) — pin sits at winW - titleBarH -
	// thumbtackIconSize.X - FramePadding.X; lock goes one icon + padding
	// further left so the two don't overlap.
	locked := false
	lockHovered := false
	var lockMin, lockMax imgui.Vec2
	if lockedWindows != nil && pinWindowTitle != "" {
		_, locked = lockedWindows[pinWindowTitle]
		lockIcon := renderer.FontAwesomeIconLockOpen
		if locked {
			lockIcon = renderer.FontAwesomeIconLock
		}
		lockIconSize := imgui.CalcTextSize(lockIcon)
		pinIconSize := imgui.CalcTextSize(renderer.FontAwesomeIconThumbtack)
		titleBarH := imgui.FrameHeight() + style.FramePadding().Y
		pinX := winPos.X + winSize.X - titleBarH - pinIconSize.X - style.FramePadding().X
		// Mirror the pin↔close gap for the lock↔pin gap so spacing is uniform.
		lockX := 2*pinX - closeX
		lockY := winPos.Y + (lineHeight-lockIconSize.Y)*0.5
		lockMin = imgui.Vec2{X: lockX - hitPad, Y: lockY - hitPad}
		lockMax = imgui.Vec2{X: lockX + lockIconSize.X + hitPad, Y: lockY + lockIconSize.Y + hitPad}
		lockHovered = windowHovered &&
			mouse.X >= lockMin.X && mouse.X <= lockMax.X &&
			mouse.Y >= lockMin.Y && mouse.Y <= lockMax.Y
		if lockHovered {
			hoverCol := imgui.ColorU32Col(imgui.ColButtonHovered)
			fgDraw.AddRectFilled(lockMin, lockMax, hoverCol)
		}
		fgDraw.AddTextVec2(imgui.Vec2{X: lockX, Y: lockY}, imgui.ColorU32Col(imgui.ColText), lockIcon)
		if lockHovered && imgui.IsMouseClickedBool(0) {
			if locked {
				delete(lockedWindows, pinWindowTitle)
				locked = false
			} else {
				lockedWindows[pinWindowTitle] = struct{}{}
				locked = true
			}
		}
	}

	// Drag-to-move: if the user clicks-and-drags in the title-bar strip
	// (and not on a button), move the imgui window. Skipped when locked.
	inBar := windowHovered &&
		mouse.X >= barMin.X && mouse.X <= barMax.X &&
		mouse.Y >= barMin.Y && mouse.Y <= barMax.Y
	if !locked && inBar && !closeHovered && !lockHovered && !imgui.IsAnyItemActive() &&
		imgui.IsMouseDraggingV(imgui.MouseButtonLeft, 0) {
		delta := imgui.MouseDragDeltaV(imgui.MouseButtonLeft, 0)
		imgui.ResetMouseDragDeltaV(imgui.MouseButtonLeft)
		imgui.SetWindowPosVec2(imgui.Vec2{X: winPos.X + delta.X, Y: winPos.Y + delta.Y})
	}

	// Reserve the title bar row as a layout item so imgui's content
	// extent matches what we drew via the draw-lists. Without this, a
	// subsequent imgui.End() asserts when no further items are submitted
	// (e.g., FlightStripPane right after scenario launch, before any
	// strips exist) because SetCursorPos was used to extend boundaries
	// without a following item. 1 px wide so it can't feed back into
	// AlwaysAutoResize; height = lineHeight advances the cursor past the
	// title bar for the caller's content.
	imgui.SetCursorPos(imgui.Vec2{X: 0, Y: 0})
	imgui.Dummy(imgui.Vec2{X: 1, Y: lineHeight})
	return closed
}
