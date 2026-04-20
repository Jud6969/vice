// pkg/platform/fullscreen_linux.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package platform

import (
	"github.com/go-gl/glfw/v3.3/glfw"
)

func (g *glfwPlatform) IsFullScreen() bool {
	return g.window.GetMonitor() != nil
}

func (g *glfwPlatform) EnableFullScreen(fullscreen bool) {
	// Scope-square mode locks the radar window to a 1:1 aspect ratio,
	// which is incompatible with fullscreen on any non-square monitor.
	// Honor the user's only-one-at-a-time contract by refusing to enter
	// fullscreen while that mode is active. Settings UI disables the
	// checkbox; this is the belt-and-braces guard.
	if fullscreen && g.config.WindowScaleMode != "" {
		return
	}

	monitors := glfw.GetMonitors()
	if g.config.FullScreenMonitor >= len(monitors) {
		// Shouldn't happen, but just to be sure
		g.config.FullScreenMonitor = 0
	}

	monitor := monitors[g.config.FullScreenMonitor]
	vm := monitor.GetVideoMode()
	if fullscreen {
		g.window.SetMonitor(monitor, 0, 0, vm.Width, vm.Height, vm.RefreshRate)
	} else {
		// Restore to a strictly windowed size. If the saved size matches
		// the monitor (e.g. the previous shutdown captured a fullscreen
		// or maximized state), the borderless window would land at
		// monitor-size at (0,0) — visually identical to fullscreen, with
		// no usable chrome — so fall back to a sensible default and
		// re-anchor the position.
		windowSize := [2]int{g.config.InitialWindowSize[0], g.config.InitialWindowSize[1]}
		windowPos := [2]int{g.config.InitialWindowPosition[0], g.config.InitialWindowPosition[1]}
		if windowSize[0] == 0 || windowSize[1] == 0 ||
			windowSize[0] >= vm.Width || windowSize[1] >= vm.Height {
			windowSize[0] = vm.Width - 150
			windowSize[1] = vm.Height - 150
			windowPos = [2]int{75, 75}
		}
		g.window.SetMonitor(nil, windowPos[0], windowPos[1],
			windowSize[0], windowSize[1], glfw.DontCare)
	}
}
