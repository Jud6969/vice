# Window-Aspect Scope-Square Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Change "Force STARS/ERAM scope square" so it locks the radar window to a 1:1 aspect ratio (filling the whole window with the scope) instead of letterboxing a square pane inside a free-aspect window.

**Architecture:** Extend `SetMainWindowSquare` in `platform/glfw.go` to install a GLFW aspect-ratio lock and snap the window to a target size derived from `WindowScaleTargets` clamped to the monitor. Block fullscreen entry while square mode is active (platform fullscreen entrypoints + Settings UI checkbox disable). Keep the existing draw-time pane-squaring logic in place as a safety net.

**Tech Stack:** Go 1.23+, `github.com/go-gl/glfw/v3.3/glfw`, `cimgui-go` (imgui bindings).

**Branch:** `windowed-border` (already rebased onto `upstream/master @bd826cbb` on 2026-04-20)

---

## File Structure

- **Modify:** `platform/glfw.go` — add `computeSquareSnapSize` helper, extend `SetMainWindowSquare`, update startup `New(...)` to snap and lock aspect when `WindowScaleMode != ""`.
- **Create:** `platform/glfw_test.go` — unit tests for `computeSquareSnapSize`.
- **Modify:** `platform/fullscreen_windows.go`, `platform/fullscreen_linux.go`, `platform/fullscreen_darwin.go` — short-circuit `EnableFullScreen` when `WindowScaleMode != ""`.
- **Modify:** `cmd/vice/ui.go` — disable the "Start in full-screen" checkbox and force-clear `config.StartInFullScreen` (plus drop out of fullscreen) when either square toggle is enabled.

---

### Task 1: Add `computeSquareSnapSize` helper

**Why:** The snap-size computation is the one piece of logic in this feature that is a pure function (mode + monitor dims → target side length). Extracting it lets us unit-test the rule without a real GLFW window.

**Files:**
- Modify: `platform/glfw.go` (add new function after `SquareScopePaneMinWindow` const around line 94)
- Create: `platform/glfw_test.go`

- [ ] **Step 1: Write the failing test**

Create `platform/glfw_test.go`:

```go
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
```

- [ ] **Step 2: Run the test and confirm it fails**

Run: `go test ./platform/ -run TestComputeSquareSnapSize -v`
Expected: compile error — `undefined: computeSquareSnapSize`.

- [ ] **Step 3: Implement the helper**

In `platform/glfw.go`, immediately after the `const SquareScopePaneMinWindow = 1000` line (around line 94), add:

```go
// computeSquareSnapSize returns the target side length for the radar
// window when scope-square mode is active. It picks WindowScaleTargets[mode]
// (STARS=2075, ERAM=2160) clamped to the monitor's shorter dimension, and
// floored at SquareScopePaneMinWindow so the window is never usable-but-
// smaller-than-the-floor. Unknown or empty modes fall back to the floor.
func computeSquareSnapSize(mode string, monitorW, monitorH int) int {
	target, ok := WindowScaleTargets[mode]
	if !ok {
		return SquareScopePaneMinWindow
	}
	shorter := monitorW
	if monitorH < shorter {
		shorter = monitorH
	}
	if target > shorter {
		target = shorter
	}
	if target < SquareScopePaneMinWindow {
		target = SquareScopePaneMinWindow
	}
	return target
}
```

- [ ] **Step 4: Run the test and confirm it passes**

Run: `go test ./platform/ -run TestComputeSquareSnapSize -v`
Expected: all 10 subtests PASS.

- [ ] **Step 5: Commit**

```bash
git add platform/glfw.go platform/glfw_test.go
git commit -m "platform: add computeSquareSnapSize helper

Pure function selecting the radar window's square side length when
scope-square mode is active. Clamps WindowScaleTargets to the shorter
monitor dimension, floored at SquareScopePaneMinWindow."
```

---

### Task 2: Extend `SetMainWindowSquare` to lock aspect and snap size

**Why:** The runtime-toggle path. When the user ticks "Force STARS/ERAM scope square" in Settings, the window should immediately become a 1:1-locked square at the target size. When they untick, the lock clears and size-floor releases (current behavior).

**Files:**
- Modify: `platform/glfw.go:231-250` (the `SetMainWindowSquare` method)

- [ ] **Step 1: Read the current implementation**

Read `platform/glfw.go` lines 231–250 so you understand what's already there. Current body only installs/clears `SetSizeLimits`.

- [ ] **Step 2: Replace the method**

Replace the existing `SetMainWindowSquare` method body with:

```go
// SetMainWindowSquare toggles "scope-square" mode. When enabled, the
// radar window is aspect-locked to 1:1 via GLFW's SetAspectRatio and
// snapped to a target size from WindowScaleTargets clamped to the
// current monitor. A minimum-size floor (SquareScopePaneMinWindow) is
// also installed so the user cannot shrink the window below a usable
// size. When disabled, both the aspect lock and the floor are cleared;
// the window keeps its current size until the user resizes.
func (g *glfwPlatform) SetMainWindowSquare(square bool) {
	g.config.MainWindowSquare = square
	if !square {
		g.config.WindowScaleMode = ""
		g.window.SetAspectRatio(glfw.DontCare, glfw.DontCare)
		g.window.SetSizeLimits(glfw.DontCare, glfw.DontCare, glfw.DontCare, glfw.DontCare)
		return
	}

	g.window.SetSizeLimits(SquareScopePaneMinWindow, SquareScopePaneMinWindow,
		glfw.DontCare, glfw.DontCare)
	g.window.SetAspectRatio(1, 1)

	// Snap the window to the target square size based on the monitor it
	// currently overlaps. MainWindowMonitorWorkArea returns the work
	// area rather than the full monitor rect; that's fine — the work
	// area's shorter dimension is the correct clamp for a visible window.
	_, _, mw, mh := g.MainWindowMonitorWorkArea()
	target := computeSquareSnapSize(g.config.WindowScaleMode, mw, mh)
	cw, ch := g.window.GetSize()
	if cw != target || ch != target {
		g.window.SetSize(target, target)
	}

	// After resizing, make sure the window is still on-screen. If the
	// new size pushes the bottom/right edge past the monitor work area,
	// move the window so it fits.
	wx, wy, ww, wh := g.MainWindowMonitorWorkArea()
	px, py := g.window.GetPos()
	if px+target > wx+ww {
		px = wx + ww - target
	}
	if py+target > wy+wh {
		py = wy + wh - target
	}
	if px < wx {
		px = wx
	}
	if py < wy {
		py = wy
	}
	g.window.SetPos(px, py)
}
```

- [ ] **Step 3: Run the existing test suite to confirm no regressions**

Run: `go test ./platform/ -v`
Expected: all pass, including `TestComputeSquareSnapSize`.

- [ ] **Step 4: Build the package to confirm it compiles**

Run: `go build ./platform/`
Expected: no output, exit 0.

- [ ] **Step 5: Commit**

```bash
git add platform/glfw.go
git commit -m "platform: aspect-lock radar window in scope-square mode

SetMainWindowSquare now installs a GLFW 1:1 aspect-ratio lock and
snaps the window to the computeSquareSnapSize target, adjusting
window position to stay on-screen. Clears the lock on disable."
```

---

### Task 3: Apply aspect lock and snap during startup

**Why:** If the user had `WindowScaleMode = "stars"` persisted from a previous session, the window should open square on startup — not wait for them to re-toggle it. Without this, saved non-square initial sizes would produce a free-aspect window that doesn't get the lock until they open Settings.

**Files:**
- Modify: `platform/glfw.go:197-203` (the `if config.MainWindowSquare { ... SetSizeLimits ... }` block inside `New`)

- [ ] **Step 1: Read the current initialization block**

Read `platform/glfw.go:156-207`. The relevant blocks are:
- Lines 156–163: If scope-square is active, raise `config.InitialWindowSize` floors.
- Lines 197–203: After window creation, install `SetSizeLimits`.

- [ ] **Step 2: Update the pre-creation snap**

Replace the block at lines 156–163 (the `if config.MainWindowSquare { ... }` that just applies the floor) with:

```go
	// If scope-square mode is active, snap the initial window size to
	// the target square derived from the primary monitor and install
	// the minimum-size floor. This covers the case where a user's saved
	// InitialWindowSize was non-square (e.g. from before this feature
	// or a mode change made via hand-edited config).
	if config.MainWindowSquare {
		target := computeSquareSnapSize(config.WindowScaleMode, vm.Width, vm.Height)
		config.InitialWindowSize[0] = target
		config.InitialWindowSize[1] = target
	}
```

- [ ] **Step 3: Update the post-creation lock**

Replace the block at lines 197–203 (the `if config.MainWindowSquare { window.SetSizeLimits(...) }` after window creation) with:

```go
	if config.MainWindowSquare {
		// Aspect-lock the window 1:1 and install the size floor. Both
		// are mirrored by SetMainWindowSquare(true) for the runtime-
		// toggle path; this is the startup counterpart.
		window.SetSizeLimits(SquareScopePaneMinWindow, SquareScopePaneMinWindow,
			glfw.DontCare, glfw.DontCare)
		window.SetAspectRatio(1, 1)
	}
```

- [ ] **Step 4: Build to confirm it compiles**

Run: `go build ./platform/`
Expected: no output, exit 0.

- [ ] **Step 5: Run the platform tests**

Run: `go test ./platform/ -v`
Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add platform/glfw.go
git commit -m "platform: snap radar window square on startup when mode set

New() now uses computeSquareSnapSize to pick the initial window side
length and installs the aspect lock after window creation, matching
the runtime-toggle path."
```

---

### Task 4: Block fullscreen while scope-square mode is active

**Why:** A 1:1 aspect lock is incompatible with fullscreen on any non-square monitor. Rather than let the two settings silently fight each other, the spec disables fullscreen entirely while square mode is on. The Settings UI (Task 5) is the user-visible guard; this task is the belt-and-braces for any other path (keybinding, macOS native gesture, future code).

**Files:**
- Modify: `platform/fullscreen_windows.go:15-47`
- Modify: `platform/fullscreen_linux.go:15-47`
- Modify: `platform/fullscreen_darwin.go:29-32`

- [ ] **Step 1: Update Windows fullscreen entrypoint**

In `platform/fullscreen_windows.go`, replace the `EnableFullScreen` function with:

```go
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
			windowSize[0] = vm.Width - 200
			windowSize[1] = vm.Height - 300
			windowPos = [2]int{100, 100}
		}
		g.window.SetMonitor(nil, windowPos[0], windowPos[1],
			windowSize[0], windowSize[1], glfw.DontCare)
	}
}
```

- [ ] **Step 2: Update Linux fullscreen entrypoint**

In `platform/fullscreen_linux.go`, apply the same change — insert the guard at the top of `EnableFullScreen` and remove the outdated `// Note: scope-square mode no longer forces a square window...` comment in the windowed branch. Final function body:

```go
func (g *glfwPlatform) EnableFullScreen(fullscreen bool) {
	if fullscreen && g.config.WindowScaleMode != "" {
		return
	}

	monitors := glfw.GetMonitors()
	if g.config.FullScreenMonitor >= len(monitors) {
		g.config.FullScreenMonitor = 0
	}

	monitor := monitors[g.config.FullScreenMonitor]
	vm := monitor.GetVideoMode()
	if fullscreen {
		g.window.SetMonitor(monitor, 0, 0, vm.Width, vm.Height, vm.RefreshRate)
	} else {
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
```

- [ ] **Step 3: Update macOS fullscreen entrypoint**

In `platform/fullscreen_darwin.go`, replace `EnableFullScreen` with:

```go
func (g *glfwPlatform) EnableFullScreen(fullscreen bool) {
	if fullscreen && g.config.WindowScaleMode != "" {
		return
	}
	window := g.window.GetCocoaWindow()
	C.makeFullscreenNative(window)
}
```

Note: macOS's `makeFullscreenNative` toggles fullscreen rather than setting it, so the guard only needs to block entry. If the platform is already native-fullscreen when the user enables square mode, Task 5's Settings-UI handler explicitly drops out first.

- [ ] **Step 4: Build to confirm**

Run: `go build ./platform/`
Expected: no output, exit 0.

(You can't build the `_darwin.go` file on Windows, but `go build` will skip it due to build tags.)

- [ ] **Step 5: Commit**

```bash
git add platform/fullscreen_windows.go platform/fullscreen_linux.go platform/fullscreen_darwin.go
git commit -m "platform: block fullscreen entry while scope-square mode is on

Scope-square's 1:1 aspect lock is incompatible with fullscreen on
non-square monitors; EnableFullScreen(true) now early-returns when
WindowScaleMode is set. Removes the stale "scope-square no longer
forces a square window" comments."
```

---

### Task 5: Settings UI — disable fullscreen controls and force-clear on enable

**Why:** User-visible side of Task 4. The "Start in full-screen" checkbox is greyed out when either square toggle is on, and enabling a square toggle while currently fullscreen drops out of fullscreen and clears the persisted `StartInFullScreen` flag.

**Files:**
- Modify: `cmd/vice/ui.go:1017-1036` (the two `Force ... scope square` checkboxes and the `Start in full-screen` checkbox immediately below).

- [ ] **Step 1: Read the current block**

Read `cmd/vice/ui.go:1005-1060` to confirm the exact surrounding context.

- [ ] **Step 2: Replace the two square-toggle handlers and the fullscreen checkbox**

Replace lines 1017–1036 (inclusive of the `Start in full-screen` checkbox line) with:

```go
		starsOn := config.WindowScaleMode == "stars"
		eramOn := config.WindowScaleMode == "eram"
		if imgui.Checkbox("Force STARS scope square", &starsOn) {
			if starsOn {
				// Square mode locks the window 1:1; fullscreen is not
				// compatible. Drop out if currently fullscreen and clear
				// the "Start in full-screen" persisted flag.
				if p.IsFullScreen() {
					p.EnableFullScreen(false)
				}
				config.StartInFullScreen = false
				config.WindowScaleMode = "stars"
				p.SetMainWindowSquare(true)
			} else {
				p.SetMainWindowSquare(false)
			}
		}
		if imgui.Checkbox("Force ERAM scope square", &eramOn) {
			if eramOn {
				if p.IsFullScreen() {
					p.EnableFullScreen(false)
				}
				config.StartInFullScreen = false
				config.WindowScaleMode = "eram"
				p.SetMainWindowSquare(true)
			} else {
				p.SetMainWindowSquare(false)
			}
		}

		imgui.BeginDisabledV(config.WindowScaleMode != "")
		imgui.Checkbox("Start in full-screen", &config.StartInFullScreen)
		if config.WindowScaleMode != "" && imgui.IsItemHovered() {
			imgui.SetTooltip("Disabled while scope-square mode is active")
		}
		imgui.EndDisabled()
```

- [ ] **Step 3: Verify the imgui API names**

The cimgui-go binding uses `BeginDisabledV(disabled bool)` / `EndDisabled()`. If a build error reports `BeginDisabledV` as undefined, search the codebase for the binding used elsewhere:

Run: `rg "imgui\.BeginDisabled" cmd/ panes/ --type go`

Use whatever signature the rest of the codebase uses (likely `BeginDisabledV` in this binding). Adjust the call in Step 2 accordingly.

- [ ] **Step 4: Build cmd/vice**

Run: `go build ./cmd/vice/`
Expected: compiles successfully. (If the cgo linker fails for environmental reasons unrelated to this change — e.g., the `collect2.exe` linker error seen during the 2026-04-20 rebase — verify with `go vet ./cmd/vice/` instead.)

- [ ] **Step 5: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: grey out fullscreen checkbox while scope-square is on

Force STARS/ERAM scope-square toggles now drop out of fullscreen on
enable, clear StartInFullScreen, and disable the fullscreen checkbox
while active with an explanatory tooltip."
```

---

### Task 6: Manual verification

**Why:** The GLFW window calls can't be meaningfully unit-tested. This task runs through the spec's test plan on a real build.

**Files:** none modified.

- [ ] **Step 1: Build and launch vice**

Run: `go build -o vice.exe ./cmd/vice/ && ./vice.exe`
Connect to any scenario so the radar window is shown.

- [ ] **Step 2: Verify windowed-mode snap (primary monitor)**

Open Settings → tick "Force STARS scope square".
Expected:
- Radar window immediately snaps to a square. On a 1080p monitor: ~1080×1080. On 1440p: 1440×1440. On 4K: 2075×2075.
- Window stays on-screen (not pushed off the right/bottom edge).
- "Start in full-screen" checkbox is greyed.

- [ ] **Step 3: Verify aspect lock during resize**

Drag a corner / edge of the radar window.
Expected: width and height stay equal at all times — window grows/shrinks as a square.

- [ ] **Step 4: Verify STARS/ERAM target difference**

Untick "Force STARS scope square", then tick "Force ERAM scope square".
Expected: on a monitor ≥ 2160 tall, the window snaps to 2160×2160 (vs STARS's 2075×2075). On smaller monitors both modes clamp to the same monitor-bound value — this is fine.

- [ ] **Step 5: Verify fullscreen block from UI**

With square mode enabled, try to enter fullscreen via any available path (F11 if bound, menu item, etc.).
Expected: nothing happens; window stays windowed and square.

- [ ] **Step 6: Verify enabling from fullscreen**

Disable square mode. Enter fullscreen normally. Then open Settings and enable "Force STARS scope square".
Expected: application drops out of fullscreen first, then window snaps to square. `StartInFullScreen` checkbox is now greyed and unchecked.

- [ ] **Step 7: Verify disable path**

With square mode on, untick "Force STARS scope square".
Expected: window keeps current size (stays square until you resize), but dragging a corner now resizes freely (non-square allowed). "Start in full-screen" checkbox re-enables.

- [ ] **Step 8: Verify persistence across restart**

Enable STARS square mode. Close vice. Relaunch.
Expected: radar window opens square at target size. "Start in full-screen" is still greyed.

- [ ] **Step 9: Verify custom-title-bar resize interaction (Windows only)**

On Windows, the windowed-border branch has a custom title bar with its own hit-testing. With square mode on, drag the bottom-right corner.
Expected: resize respects the 1:1 lock (stays square). If it does NOT — if you can drag to a non-square shape — this indicates `platform/titlebar_windows.go`'s hit-test/resize path bypasses GLFW's aspect enforcement. File a follow-up: clamp in the resize callback (out of scope for this plan, but note it).

- [ ] **Step 10: Commit any incidental verification notes**

If Step 9 (or any other step) surfaced a follow-up, update the spec's "Open items" section with what was observed.

```bash
git add docs/superpowers/specs/2026-04-20-window-aspect-scope-square-design.md
git commit -m "docs: note verification findings for window-aspect scope-square"
```

If no follow-ups, skip this step.

---

## Self-Review Notes

Coverage check vs spec:
- Spec "Component 1 — Aspect lock and initial snap" → Tasks 1, 2, 3.
- Spec "Component 2 — Fullscreen blocking" → Task 4.
- Spec "Component 3 — Settings UI" → Task 5.
- Spec "Testing" plan → Task 6.
- Spec "Error Handling" (unknown mode fallback) → covered by Task 1's "unknown mode" and "empty mode" test cases.
- Spec "Open items" (title-bar resize) → Task 6 Step 9.

No placeholders. Function and method names match between tasks (`computeSquareSnapSize`, `SetMainWindowSquare`, `EnableFullScreen`). Step 3 of Task 5 provides a guardrail for the `BeginDisabledV` binding-name uncertainty rather than leaving it ambiguous.
