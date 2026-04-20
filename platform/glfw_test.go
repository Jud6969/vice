// pkg/platform/glfw_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package platform

import "testing"

func TestComputeSquareSnapSize(t *testing.T) {
	tests := []struct {
		name      string
		mode      string
		monitorW  int
		monitorH  int
		want      int
	}{
		// Mode target smaller than monitor's shorter dim → use target.
		{"stars on 4K", "stars", 3840, 2160, 2075},
		{"eram on 4K", "eram", 3840, 2160, 2160},

		// Mode target larger than monitor's shorter dim → clamp to monitor.
		{"stars on 1080p", "stars", 1920, 1080, 1080},
		{"eram on 1080p", "eram", 1920, 1080, 1080},
		{"stars on 1440p", "stars", 2560, 1440, 1440},
		{"eram on 1440p", "eram", 2560, 1440, 1440},

		// Portrait monitor: clamp to the shorter dim (width here).
		{"stars portrait", "stars", 1080, 1920, 1080},

		// Monitor smaller than floor → clamp up to floor.
		{"stars tiny monitor", "stars", 800, 600, SquareScopePaneMinWindow},

		// Unknown mode → floor (corrupted config fallback).
		{"unknown mode", "xyz", 1920, 1080, SquareScopePaneMinWindow},

		// Empty mode → floor (defensive, should not be called in practice).
		{"empty mode", "", 1920, 1080, SquareScopePaneMinWindow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeSquareSnapSize(tt.mode, tt.monitorW, tt.monitorH)
			if got != tt.want {
				t.Errorf("computeSquareSnapSize(%q, %d, %d) = %d, want %d",
					tt.mode, tt.monitorW, tt.monitorH, got, tt.want)
			}
		})
	}
}
