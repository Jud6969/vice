// platform/titlebar_linux.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

//go:build linux && !wayland

package platform

/*
#cgo LDFLAGS: -lX11

#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <string.h>

// Send _NET_WM_MOVERESIZE with direction _NET_WM_MOVERESIZE_MOVE (8) so
// the window manager takes over the drag — this is what gives us
// WM-side edge snapping (KWin, Mutter, i3, etc.).
static int vice_begin_window_move(Display* dpy, Window root, Window win,
                                  int screen_x, int screen_y) {
    if (dpy == NULL) return 0;
    Atom atom = XInternAtom(dpy, "_NET_WM_MOVERESIZE", False);
    if (atom == None) return 0;

    // Release any pointer grab so the WM can grab it for the drag.
    XUngrabPointer(dpy, CurrentTime);

    XEvent ev;
    memset(&ev, 0, sizeof(ev));
    ev.xclient.type = ClientMessage;
    ev.xclient.window = win;
    ev.xclient.message_type = atom;
    ev.xclient.format = 32;
    ev.xclient.data.l[0] = screen_x;
    ev.xclient.data.l[1] = screen_y;
    ev.xclient.data.l[2] = 8;  // _NET_WM_MOVERESIZE_MOVE
    ev.xclient.data.l[3] = 1;  // button 1 (left)
    ev.xclient.data.l[4] = 1;  // source indication: application
    XSendEvent(dpy, root, False,
               SubstructureRedirectMask | SubstructureNotifyMask, &ev);
    XFlush(dpy);
    return 1;
}
*/
import "C"

import (
	"unsafe"

	"github.com/go-gl/glfw/v3.3/glfw"

	"github.com/mmp/vice/math"
)

// SetCaptionRegions is a no-op on Linux; the imperative
// BeginNativeWindowDrag path is used instead.
func (g *glfwPlatform) SetCaptionRegions(rects []math.Extent2D) {}

// BeginNativeWindowDrag asks the X11 window manager to take over the
// drag via _NET_WM_MOVERESIZE, which gives WM-side edge snapping on
// GNOME/Mutter, KDE/KWin, i3, etc.
func (g *glfwPlatform) BeginNativeWindowDrag() bool {
	display := glfw.GetX11Display()
	if display == nil {
		return false
	}
	win := g.window.GetX11Window()
	// Cursor position in screen coordinates.
	winX, winY := g.window.GetPos()
	cx, cy := g.window.GetCursorPos()
	screenX := C.int(winX + int(cx))
	screenY := C.int(winY + int(cy))

	// Determine the root window for the default screen. Cast the opaque
	// glfw Display pointer to X11's Display* via unsafe; GLFW's C.Display
	// and ours are the same underlying X11 type.
	dpy := (*C.Display)(unsafe.Pointer(display))
	root := C.XDefaultRootWindow(dpy)

	return C.vice_begin_window_move(dpy, root, C.Window(win), screenX, screenY) != 0
}

// installNativeTitleBar is a no-op on Linux.
func (g *glfwPlatform) installNativeTitleBar() {}
