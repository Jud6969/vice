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
		{"real on 4K", "real", 3840, 2160, 2048},

		// Mode target larger than monitor's shorter dim → clamp to monitor.
		{"real on 1080p", "real", 1920, 1080, 1080},
		{"real on 1440p", "real", 2560, 1440, 1440},

		// Portrait monitor: clamp to the shorter dim (width here).
		{"real portrait", "real", 1080, 1920, 1080},

		// Monitor smaller than floor → clamp up to floor.
		{"real tiny monitor", "real", 800, 600, SquareScopePaneMinWindow},

		// Legacy mode values that persisted configs may still contain
		// should migrate upstream before this function is called; if
		// they somehow reach it, they're treated as unknown.
		{"legacy stars treated as unknown", "stars", 1920, 1080, SquareScopePaneMinWindow},
		{"legacy eram treated as unknown", "eram", 1920, 1080, SquareScopePaneMinWindow},

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
