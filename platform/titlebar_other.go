// platform/titlebar_other.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

//go:build !windows && !darwin && !(linux && !wayland)

package platform

import (
	"github.com/mmp/vice/math"
)

// SetCaptionRegions is a no-op on platforms without native title-bar
// integration; callers fall back to software drag.
func (g *glfwPlatform) SetCaptionRegions(rects []math.Extent2D) {}

// BeginNativeWindowDrag returns false, signalling to the caller that
// no native drag was initiated.
func (g *glfwPlatform) BeginNativeWindowDrag() bool { return false }

// installNativeTitleBar is a no-op.
func (g *glfwPlatform) installNativeTitleBar() {}
