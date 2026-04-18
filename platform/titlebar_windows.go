// platform/titlebar_windows.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package platform

import (
	"sync"
	"syscall"
	"unsafe"

	"github.com/mmp/vice/math"
)

// Windows message / hit-test / style constants we use.
const (
	wmNCCalcSize = 0x0083
	wmNCHitTest  = 0x0084
	htClient     = 1
	htCaption    = 2
	// GWL / GWLP constants expressed via bitwise complement so they fit
	// into a compile-time uintptr on both 32- and 64-bit builds.
	gwlpWndProc = ^uintptr(3) // -4
	gwlStyle    = ^uintptr(15) // -16

	wsThickFrame   = 0x00040000
	wsMaximizeBox  = 0x00010000

	swpNoMove        = 0x0002
	swpNoSize        = 0x0001
	swpNoZOrder      = 0x0004
	swpNoOwnerZOrder = 0x0200
	swpNoActivate    = 0x0010
	swpFrameChanged  = 0x0020

	monitorDefaultToNearest = 0x00000002
)

var (
	tbUser32              = syscall.NewLazyDLL("user32.dll")
	tbSetWindowLongPtrW   = tbUser32.NewProc("SetWindowLongPtrW")
	tbGetWindowLongPtrW   = tbUser32.NewProc("GetWindowLongPtrW")
	tbCallWindowProcW     = tbUser32.NewProc("CallWindowProcW")
	tbScreenToClient      = tbUser32.NewProc("ScreenToClient")
	tbSetWindowPos        = tbUser32.NewProc("SetWindowPos")
	tbIsZoomed            = tbUser32.NewProc("IsZoomed")
	tbMonitorFromWindow   = tbUser32.NewProc("MonitorFromWindow")
	tbGetMonitorInfoW     = tbUser32.NewProc("GetMonitorInfoW")

	tbMu       sync.Mutex
	tbRegions  []math.Extent2D
	tbOrigProc uintptr
	tbHwnd     uintptr
	tbCallback uintptr
)

type tbPoint struct{ x, y int32 }
type tbRect struct{ left, top, right, bottom int32 }
type tbMonitorInfo struct {
	cbSize    uint32
	rcMonitor tbRect
	rcWork    tbRect
	dwFlags   uint32
}

// viceTitleBarWndProc is the subclassed window procedure. It handles
// two messages: WM_NCCALCSIZE (collapse the non-client frame to zero so
// the WS_THICKFRAME we added stays invisible) and WM_NCHITTEST (promote
// declared caption regions to HTCAPTION so Windows handles native
// drag + Aero Snap). Everything else chains to the original proc.
func viceTitleBarWndProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	if hwnd == tbHwnd {
		switch msg {
		case wmNCCalcSize:
			// Remove the thick-frame border from the client-area
			// calculation so the window stays visually borderless.
			// When maximized, clamp the client rect to the monitor's
			// work area; otherwise Windows positions the maximized
			// window ~8px past each edge (the "would-be" border).
			var target *tbRect
			if wparam == 1 { // TRUE: lparam is NCCALCSIZE_PARAMS*
				rects := (*[3]tbRect)(unsafe.Pointer(lparam))
				target = &rects[0]
			} else {
				target = (*tbRect)(unsafe.Pointer(lparam))
			}
			zoomed, _, _ := tbIsZoomed.Call(hwnd)
			if zoomed != 0 {
				hMonitor, _, _ := tbMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
				mi := tbMonitorInfo{cbSize: uint32(unsafe.Sizeof(tbMonitorInfo{}))}
				tbGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi)))
				*target = mi.rcWork
			}
			return 0

		case wmNCHitTest:
			defRet, _, _ := tbCallWindowProcW.Call(tbOrigProc, hwnd, msg, wparam, lparam)
			if defRet != htClient {
				return defRet
			}
			pt := tbPoint{
				x: int32(int16(uint16(lparam & 0xFFFF))),
				y: int32(int16(uint16((lparam >> 16) & 0xFFFF))),
			}
			tbScreenToClient.Call(hwnd, uintptr(unsafe.Pointer(&pt)))

			tbMu.Lock()
			regions := tbRegions
			tbMu.Unlock()
			x, y := float32(pt.x), float32(pt.y)
			for _, r := range regions {
				if x >= r.P0[0] && x < r.P1[0] && y >= r.P0[1] && y < r.P1[1] {
					return htCaption
				}
			}
			return htClient
		}
	}
	ret, _, _ := tbCallWindowProcW.Call(tbOrigProc, hwnd, msg, wparam, lparam)
	return ret
}

// installTitleBarHitTest subclasses the window procedure for hwnd so
// WM_NCHITTEST / WM_NCCALCSIZE can be intercepted, then adds
// WS_THICKFRAME to the window style (required for Aero Snap).
func installTitleBarHitTest(hwnd uintptr) {
	if tbHwnd != 0 || hwnd == 0 {
		return
	}
	tbHwnd = hwnd
	tbCallback = syscall.NewCallback(viceTitleBarWndProc)
	orig, _, _ := tbSetWindowLongPtrW.Call(hwnd, gwlpWndProc, tbCallback)
	tbOrigProc = orig

	// Add WS_THICKFRAME so Windows treats the window as resizable and
	// runs Aero Snap when HTCAPTION is dragged to a screen edge. The
	// WM_NCCALCSIZE override above keeps the thick-frame invisible.
	style, _, _ := tbGetWindowLongPtrW.Call(hwnd, gwlStyle)
	newStyle := style | wsThickFrame | wsMaximizeBox
	if newStyle != style {
		tbSetWindowLongPtrW.Call(hwnd, gwlStyle, newStyle)
		// Force the non-client frame to recompute now.
		tbSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoOwnerZOrder|swpNoActivate|swpFrameChanged)
	}
}

// SetCaptionRegions implements platform.Platform on Windows.
func (g *glfwPlatform) SetCaptionRegions(rects []math.Extent2D) {
	cp := make([]math.Extent2D, len(rects))
	copy(cp, rects)
	tbMu.Lock()
	tbRegions = cp
	tbMu.Unlock()
}

// BeginNativeWindowDrag is a no-op on Windows; SetCaptionRegions + the
// WM_NCHITTEST hook route drags through the OS natively.
func (g *glfwPlatform) BeginNativeWindowDrag() bool {
	return false
}

// installNativeTitleBar subclasses the main window's WndProc so
// WM_NCHITTEST can be intercepted for the custom title bar. Called
// during glfwPlatform construction.
func (g *glfwPlatform) installNativeTitleBar() {
	ghwnd := g.window.GetWin32Window()
	installTitleBarHitTest(uintptr(unsafe.Pointer(ghwnd)))
}
