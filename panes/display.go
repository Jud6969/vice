// panes/display.go
// Copyright(c) 2022-2025 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

// This file handles rendering the main radar scope pane. The main window
// is dedicated to the radar scope (STARS or ERAM); Messages and Flight
// Strips are rendered in their own floating imgui windows.

package panes

import (
	"runtime"

	"github.com/AllenDang/cimgui-go/imgui"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/platform"
	"github.com/mmp/vice/renderer"
	"github.com/mmp/vice/util"
)

var (
	wm struct {
		// Normally the Pane that the mouse is over gets mouse events,
		// though if the user has started a click-drag, then the Pane that
		// received the click keeps getting events until the mouse button
		// is released.  mouseConsumerOverride records such a pane.
		mouseConsumerOverride Pane

		focus KeyboardFocus

		lastAircraftResponse string
	}
)

type KeyboardFocus struct {
	current any
}

func (f *KeyboardFocus) Take(p any) {
	f.current = p
}

func (f *KeyboardFocus) Release() {
	f.current = nil
}

func (f *KeyboardFocus) Current() any {
	return f.current
}

// DrawPanes renders a single radar pane that fills the entire display area
// below the menu bar.
func DrawPanes(pane Pane, p platform.Platform, r renderer.Renderer,
	controlClient *client.ControlClient, menuBarHeight float32, lg *log.Logger) renderer.RendererStats {
	if controlClient == nil {
		commandBuffer := renderer.GetCommandBuffer()
		defer renderer.ReturnCommandBuffer(commandBuffer)
		commandBuffer.ClearRGB(renderer.RGB{})
		return r.RenderCommandBuffer(commandBuffer)
	}

	if wm.focus.Current() == nil || wm.focus.Current() != pane {
		if pane.CanTakeKeyboardFocus() {
			wm.focus.Take(pane)
		}
	}

	fbSize := p.FramebufferSize()
	displaySize := p.DisplaySize()

	// Area left for actually drawing the pane
	paneDisplayExtent := math.Extent2D{
		P0: [2]float32{0, 0},
		P1: [2]float32{displaySize[0], displaySize[1] - menuBarHeight},
	}

	// Scope-square mode: shrink the pane's allocated extent to a centered
	// square. The application window is otherwise free to be any aspect;
	// the area outside the square stays at the framebuffer clear color
	// (black), giving natural letterbox / pillarbox bars.
	if p.SquareScopePane() {
		w := paneDisplayExtent.Width()
		h := paneDisplayExtent.Height()
		side := w
		if h < side {
			side = h
		}
		ox := (w - side) / 2
		oy := (h - side) / 2
		paneDisplayExtent = math.Extent2D{
			P0: [2]float32{ox, oy},
			P1: [2]float32{ox + side, oy + side},
		}
	}

	// Get the mouse position from imgui; convert from screen coordinates
	// to main-window-relative coordinates (with multi-viewport, MousePos
	// returns OS screen coords), then flip y to match our window coords.
	mainViewportPos := imgui.MainViewport().Pos()
	mousePos := [2]float32{
		imgui.MousePos().X - mainViewportPos.X,
		displaySize[1] - 1 - (imgui.MousePos().Y - mainViewportPos.Y),
	}

	io := imgui.CurrentIO()

	// If the user has clicked or is dragging in the pane, record it in
	// mouseConsumerOverride so that we continue to dispatch mouse
	// events until the mouse button is released.
	isDragging := imgui.IsMouseDraggingV(platform.MouseButtonPrimary, 0.) ||
		imgui.IsMouseDraggingV(platform.MouseButtonSecondary, 0.) ||
		imgui.IsMouseDraggingV(platform.MouseButtonTertiary, 0.)
	isClicked := imgui.IsMouseClickedBool(platform.MouseButtonPrimary) ||
		imgui.IsMouseClickedBool(platform.MouseButtonSecondary) ||
		imgui.IsMouseClickedBool(platform.MouseButtonTertiary)
	if !io.WantCaptureMouse() && (isDragging || isClicked) && wm.mouseConsumerOverride == nil {
		wm.mouseConsumerOverride = pane
	} else if io.WantCaptureMouse() {
		wm.mouseConsumerOverride = nil
	}

	p.ClearCursorOverride()

	commandBuffer := renderer.GetCommandBuffer()
	defer renderer.ReturnCommandBuffer(commandBuffer)
	commandBuffer.ClearRGB(renderer.RGB{})

	var keyboard *platform.KeyboardState
	if !imgui.CurrentIO().WantCaptureKeyboard() {
		keyboard = p.GetKeyboard()
	}

	haveFocus := pane == wm.focus.Current() && !imgui.CurrentIO().WantCaptureKeyboard()
	ctx := Context{
		PaneExtent:         paneDisplayExtent,
		ParentPaneExtent:   paneDisplayExtent,
		Platform:           p,
		DrawPixelScale:     util.Select(runtime.GOOS == "windows", p.DPIScale(), float32(1)),
		PixelsPerInch:      util.Select(runtime.GOOS == "windows", 96*p.DPIScale(), float32(72)),
		DPIScale:           p.DPIScale(),
		Renderer:           r,
		Keyboard:           keyboard,
		HaveFocus:          haveFocus,
		SimTime:            controlClient.InterpolatedSimTime(),
		Lg:                 lg,
		MenuBarHeight:      menuBarHeight,
		KeyboardFocus:      &wm.focus,
		Client:             controlClient,
		UserTCW:            controlClient.State.UserTCW,
		NmPerLongitude:     controlClient.State.NmPerLongitude,
		MagneticVariation:  controlClient.State.MagneticVariation,
		FacilityAdaptation: &controlClient.State.FacilityAdaptation,
		displaySize:        p.DisplaySize(),
	}

	ownsMouse := wm.mouseConsumerOverride == pane ||
		(wm.mouseConsumerOverride == nil &&
			!io.WantCaptureMouse() &&
			paneDisplayExtent.Inside(mousePos))
	if ownsMouse {
		ctx.InitializeMouse(p)
	}

	commandBuffer.SetDrawBounds(paneDisplayExtent, p.FramebufferSize()[1]/p.DisplaySize()[1])
	pane.Draw(&ctx, commandBuffer)
	commandBuffer.ResetState()

	// Draw a 1 px white border on the outer edge of the square scope pane.
	// Only runs in scope-square mode — in free-aspect mode the scope fills
	// the framebuffer and there's no "panel" outline to draw.
	if p.SquareScopePane() {
		commandBuffer.SetDrawBounds(
			math.Extent2D{
				P0: [2]float32{0, 0},
				P1: [2]float32{displaySize[0], displaySize[1]},
			},
			p.FramebufferSize()[1]/p.DisplaySize()[1],
		)
		border := renderer.GetLinesDrawBuilder()
		// Inset by 0.5 px so the 1 px line sits fully inside the square.
		x0 := paneDisplayExtent.P0[0] + 0.5
		y0 := paneDisplayExtent.P0[1] + 0.5
		x1 := paneDisplayExtent.P1[0] - 0.5
		y1 := paneDisplayExtent.P1[1] - 0.5
		border.AddLineLoop([][2]float32{
			{x0, y0}, {x1, y0}, {x1, y1}, {x0, y1},
		})
		commandBuffer.SetRGB(renderer.RGB{R: 1, G: 1, B: 1})
		commandBuffer.LineWidth(1, p.DPIScale())
		border.GenerateCommands(commandBuffer)
		renderer.ReturnLinesDrawBuilder(border)
		commandBuffer.ResetState()
	}

	if !isDragging && !isClicked {
		wm.mouseConsumerOverride = nil
	}

	if fbSize[0] > 0 && fbSize[1] > 0 {
		return r.RenderCommandBuffer(commandBuffer)
	}
	return renderer.RendererStats{}
}
