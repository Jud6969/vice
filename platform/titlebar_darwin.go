// platform/titlebar_darwin.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package platform

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework Cocoa

#import <Cocoa/Cocoa.h>

static int vice_begin_window_drag(void* nswindow) {
    if (nswindow == NULL) return 0;
    NSWindow* win = (__bridge NSWindow*)nswindow;
    NSEvent* ev = [NSApp currentEvent];
    if (ev == nil) return 0;
    [win performWindowDragWithEvent:ev];
    return 1;
}
*/
import "C"

import (
	"unsafe"

	"github.com/mmp/vice/math"
)

// SetCaptionRegions is a no-op on macOS; the imperative
// BeginNativeWindowDrag path is used instead.
func (g *glfwPlatform) SetCaptionRegions(rects []math.Extent2D) {}

// BeginNativeWindowDrag asks AppKit to take over the drag, which also
// enables macOS 15+ window tiling.
func (g *glfwPlatform) BeginNativeWindowDrag() bool {
	ptr := g.window.GetCocoaWindow()
	if ptr == nil {
		return false
	}
	return C.vice_begin_window_drag(unsafe.Pointer(ptr)) != 0
}

// installNativeTitleBar is a no-op on macOS.
func (g *glfwPlatform) installNativeTitleBar() {}
