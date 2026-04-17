# Integrated STARS Panes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a 1 px white border around the square STARS scope and dock the Messages / Flight Strips panes into the letterbox bars when scope-square mode is active, with per-pane pop-out and a side-layout setting.

**Architecture:** Three isolated changes. (1) Draw a 1 px white line loop around the square scope in `panes/display.go` after the pane renders. (2) Add three `bool` fields to the application `Config` with zero-value-safe defaults (`PopOutMessages`, `PopOutFlightStrips`, `MessagesOnRight`). (3) At the `DrawWindow` call sites in `cmd/vice/ui.go`, when the app is in scope-square mode and the pane is not popped out, compute the letterbox bar geometry, apply a no-auto-merge-cleared window class, and pass the docked flags (`NoMove | NoResize | NoCollapse | NoTitleBar`) down to the pane; otherwise fall through to today's floating path.

**Tech Stack:** Go, GLFW, cimgui-go, in-house OpenGL renderer.

**Spec reference:** `docs/superpowers/specs/2026-04-17-integrated-stars-panes-design.md`

**Testing notes:** This feature is pure layout / rendering and the codebase has no automated UI tests. Each task uses compile checks (`go build -o /dev/null ./...`) for verification, plus a dedicated manual-verification task at the end. "Build" in this repo is hampered by a pre-existing C-linker issue affecting `cmd/vice` full builds on Windows; use `go build -o /dev/null ./<package>/` for per-package compile checks instead.

---

## Task 1: 1 px white border around the square STARS scope

**Files:**
- Modify: `panes/display.go` around lines 83–96 (inside the `if p.SquareScopePane()` branch) and after the `pane.Draw` block at ~line 167.

- [ ] **Step 1: Read the file to confirm current line numbers**

Run: Use the `Read` tool on `panes/display.go` to locate the `if p.SquareScopePane()` block that sets `paneDisplayExtent` and the subsequent `commandBuffer.SetDrawBounds(paneDisplayExtent, ...)` / `pane.Draw(...)` / `commandBuffer.ResetState()` sequence. Line numbers may have drifted from 83/167; all subsequent steps reference semantic locations, not absolute line numbers.

- [ ] **Step 2: Add the border-drawing block**

In `panes/display.go`, immediately after the existing `commandBuffer.ResetState()` call that follows `pane.Draw(&ctx, commandBuffer)`, insert this block:

```go
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
```

- [ ] **Step 3: Verify the package compiles**

Run: `go build -o /dev/null ./panes/`
Expected: exit 0, no output.

- [ ] **Step 4: Commit**

```bash
git add panes/display.go
git commit -m "panes: draw 1 px white border around square scope"
```

---

## Task 2: Add configuration fields for pane integration

**Files:**
- Modify: `cmd/vice/config.go` — add fields to the `ConfigNoSim` struct around line 55 (near `FlightStripPane *panes.FlightStripPane`).

- [ ] **Step 1: Add fields to `ConfigNoSim`**

In `cmd/vice/config.go`, inside the `ConfigNoSim` struct definition, immediately after the `ShowFlightStrips bool` line (currently line ~59), add:

```go
	// Pop-out toggles for panes that can integrate into the letterbox bars
	// when the application is in scope-square mode. Zero value (false) means
	// "integrated"; true means "float as an OS window" (pre-feature behavior).
	PopOutMessages     bool
	PopOutFlightStrips bool

	// Side layout for the integrated panes. Zero value (false) places
	// Messages in the left letterbox bar and Flight Strips in the right.
	// When true, the sides are swapped. The two panes can never share a
	// side: the flight-strip side is always the opposite of the messages
	// side.
	MessagesOnRight bool
```

- [ ] **Step 2: Verify the package compiles**

Run: `go build -o /dev/null ./cmd/vice/`
Expected: exit 0 OR only the pre-existing C-linker error. No new Go compile errors.

(If `./cmd/vice/` cannot be compiled standalone on Windows due to the pre-existing `onnxruntime` / MSYS2 linker issue, fall back to `go vet ./cmd/vice/` and confirm there are no new errors beyond the known `Vec2/Vec4 struct literal uses unkeyed fields` vet warnings in other files.)

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/config.go
git commit -m "config: add pop-out and side-layout fields for integrated panes"
```

---

## Task 3: Add `applyDockedWindowClass` helper

**Files:**
- Modify: `cmd/vice/ui.go` around line 848 (next to the existing `applyPinWindowClass`).

- [ ] **Step 1: Add the helper**

In `cmd/vice/ui.go`, immediately below the existing `applyPinWindowClass` function, add:

```go
// applyDockedWindowClass is the docked counterpart to applyPinWindowClass.
// Used for Messages / Flight Strips when they are integrated into the
// main window's letterbox bar. Unlike applyPinWindowClass it does NOT set
// imgui.ViewportFlagsNoAutoMerge, so the window stays inside the main
// viewport instead of spawning a separate OS window.
func applyDockedWindowClass() {
	wc := imgui.NewWindowClass()
	// Explicitly clear NoAutoMerge so imgui folds this window into the
	// parent viewport even if a previous frame's flags lingered.
	wc.SetViewportFlagsOverrideClear(imgui.ViewportFlagsNoAutoMerge)
	imgui.SetNextWindowClass(wc)
}
```

- [ ] **Step 2: Verify the package compiles**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields"`
Expected: no lines (the only remaining `vet` output would be pre-existing unkeyed-field warnings, which the grep filters out).

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: add applyDockedWindowClass helper for integrated panes"
```

---

## Task 4: Extend `MessagesPane.DrawWindow` to accept docked flags

**Files:**
- Modify: `panes/messages.go` `DrawWindow` method (line ~122) — add `dockedFlags imgui.WindowFlags` parameter.
- Modify: `cmd/vice/ui.go` call site (line ~395) — pass `0`.

- [ ] **Step 1: Change the pane signature and wire the flags through**

In `panes/messages.go`, replace the existing `DrawWindow` signature and body-top with this version:

```go
func (mp *MessagesPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, dockedFlags imgui.WindowFlags, lg *log.Logger) {
	// Only play sounds if the window has been continuously visible. If
	// more than 250ms have elapsed since the last DrawWindow call, we
	// must have missed frames (window was hidden), so drain accumulated
	// events silently to avoid spamming audio for the backlog.
	now := time.Now()
	playSound := !mp.lastDrawTime.IsZero() && now.Sub(mp.lastDrawTime) < 250*time.Millisecond
	mp.lastDrawTime = now
	mp.processEvents(playSound, c, p, lg)

	if dockedFlags == 0 {
		imgui.SetNextWindowSizeConstraints(imgui.Vec2{300, 100}, imgui.Vec2{4096, 4096})
	}
	if mp.font != nil {
		mp.font.ImguiPush()
	}
	imgui.BeginV("Messages", show, dockedFlags)
	if dockedFlags == 0 {
		DrawPinButton("Messages", unpinnedWindows, p)
	}
	if imgui.BeginChildStrV("##messages_scroll", imgui.Vec2{}, 0, 0) {
```

(The rest of the function — from `for _, msg := range mp.messages {` through `imgui.End()` and the `imgui.PopFont()` — stays unchanged.)

- [ ] **Step 2: Update the single call site**

In `cmd/vice/ui.go`, replace the `config.MessagesPane.DrawWindow(...)` call with:

```go
		config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, 0, lg)
```

(The `0` is the new `dockedFlags` argument — preserves today's floating behavior. Task 6 will compute a non-zero value conditionally.)

- [ ] **Step 3: Verify the packages compile**

Run: `go build -o /dev/null ./panes/`
Expected: exit 0.
Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields"`
Expected: no lines.

- [ ] **Step 4: Commit**

```bash
git add panes/messages.go cmd/vice/ui.go
git commit -m "panes: thread dockedFlags through MessagesPane.DrawWindow"
```

---

## Task 5: Extend `FlightStripPane.DrawWindow` to accept docked flags

**Files:**
- Modify: `panes/flightstrip.go` `DrawWindow` method (line ~180 region) — add `dockedFlags imgui.WindowFlags` parameter.
- Modify: `cmd/vice/ui.go` call site (line ~399) — pass `0`.

- [ ] **Step 1: Read `panes/flightstrip.go` to locate the signature**

Run: Use `Read` or `Grep` on `panes/flightstrip.go` to find the full signature and body of `DrawWindow`. The existing pattern is identical to `MessagesPane.DrawWindow` — same approach applies.

- [ ] **Step 2: Change the pane signature**

In `panes/flightstrip.go`, update the `DrawWindow` signature to add `dockedFlags imgui.WindowFlags` immediately before `lg *log.Logger`:

```go
func (fsp *FlightStripPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, dockedFlags imgui.WindowFlags, lg *log.Logger) {
```

Inside the body:
- Replace the existing `imgui.BeginV("Flight Strips", show, <flags>)` call with `imgui.BeginV("Flight Strips", show, dockedFlags)`. (If the pane already passes non-zero flags unconditionally, OR them with `dockedFlags`.)
- Guard the existing `DrawPinButton("Flight Strips", unpinnedWindows, p)` call with `if dockedFlags == 0 {` / `}` so the pin thumbtack is hidden when docked.
- If the function sets `imgui.SetNextWindowSizeConstraints(...)`, guard that with `if dockedFlags == 0 {` / `}` as well — size constraints make no sense when we're externally setting `SetNextWindowSize` in Task 6.

- [ ] **Step 3: Update the single call site**

In `cmd/vice/ui.go`, replace the `config.FlightStripPane.DrawWindow(...)` call with:

```go
		config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config.UnpinnedWindows, 0, lg)
```

- [ ] **Step 4: Verify the packages compile**

Run: `go build -o /dev/null ./panes/`
Expected: exit 0.
Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields"`
Expected: no lines.

- [ ] **Step 5: Commit**

```bash
git add panes/flightstrip.go cmd/vice/ui.go
git commit -m "panes: thread dockedFlags through FlightStripPane.DrawWindow"
```

---

## Task 6: Dock panes into letterbox bars when appropriate

**Files:**
- Modify: `cmd/vice/ui.go` — the block that currently draws Messages and Flight Strips (lines ~393–400).

- [ ] **Step 1: Replace the existing draw block with the docking-aware version**

In `cmd/vice/ui.go`, replace the block:

```go
		if ui.showMessages {
			applyPinWindowClass("Messages", config, p)
			config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, 0, lg)
		}
		if ui.showFlightStrips {
			applyPinWindowClass("Flight Strips", config, p)
			config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config.UnpinnedWindows, 0, lg)
		}
```

with:

```go
		// Layout math for docked (integrated) panes. Each pane's target
		// letterbox bar is computed from the main window's current size
		// and the scope-square side length. If scope-square mode is off,
		// or the user has popped the pane out, or there is no usable
		// letterbox (barWidth <= 0), fall through to today's floating
		// OS-window behavior.
		displaySize := p.DisplaySize()
		squareActive := p.SquareScopePane()
		side := displaySize[0]
		if displaySize[1] < side {
			side = displaySize[1]
		}
		barWidth := (displaySize[0] - side) / 2
		barTop := menuBarHeight
		barHeight := displaySize[1] - menuBarHeight

		dockPane := func(onRight bool) (imgui.WindowFlags, bool) {
			if !squareActive || barWidth <= 0 {
				return 0, false
			}
			x := float32(0)
			if onRight {
				x = displaySize[0] - barWidth
			}
			imgui.SetNextWindowPos(imgui.Vec2{X: x, Y: barTop})
			imgui.SetNextWindowSize(imgui.Vec2{X: barWidth, Y: barHeight})
			applyDockedWindowClass()
			return imgui.WindowFlagsNoMove | imgui.WindowFlagsNoResize |
				imgui.WindowFlagsNoCollapse | imgui.WindowFlagsNoTitleBar, true
		}

		if ui.showMessages {
			messagesOnRight := config.MessagesOnRight
			var flags imgui.WindowFlags
			var docked bool
			if !config.PopOutMessages {
				flags, docked = dockPane(messagesOnRight)
			}
			if !docked {
				applyPinWindowClass("Messages", config, p)
			}
			config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, flags, lg)
		}
		if ui.showFlightStrips {
			// Flight strips sit on the opposite side of Messages.
			stripsOnRight := !config.MessagesOnRight
			var flags imgui.WindowFlags
			var docked bool
			if !config.PopOutFlightStrips {
				flags, docked = dockPane(stripsOnRight)
			}
			if !docked {
				applyPinWindowClass("Flight Strips", config, p)
			}
			config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config.UnpinnedWindows, flags, lg)
		}
```

- [ ] **Step 2: Verify types — `menuBarHeight` and `displaySize`**

Confirm by reading the surrounding function context: `menuBarHeight` must already be a `float32` in scope at this point in `cmd/vice/ui.go` (it is used elsewhere in the same function, including the `ui.go:1253` and `ui.go:1376` references located earlier). If it is not, compute it the same way the surrounding code does (e.g., by reading `ui.menuBarHeight` from the UI state struct). `displaySize := p.DisplaySize()` is a fresh local and safe to shadow any earlier value.

- [ ] **Step 3: Verify the package compiles**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields"`
Expected: no lines.

- [ ] **Step 4: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: dock messages and flight strips into letterbox bars"
```

---

## Task 7: Settings UI

**Files:**
- Modify: `cmd/vice/ui.go` — add controls immediately below the two "Force {STARS,ERAM} scope square" checkboxes around line 951.

- [ ] **Step 1: Add the new controls**

In `cmd/vice/ui.go`, immediately after the closing brace of the `"Force ERAM scope square"` checkbox block (current line 951, `}`) and before `imgui.Checkbox("Start in full-screen", &config.StartInFullScreen)`, insert:

```go
		// Integrated-pane settings. Only meaningful while scope-square
		// mode is active, but we leave them enabled regardless so the
		// user can configure them ahead of toggling scope-square on.
		imgui.Separator()
		imgui.TextUnformatted("Integrated panes (STARS/ERAM scaling):")
		imgui.Checkbox("Pop out Messages", &config.PopOutMessages)
		imgui.Checkbox("Pop out Flight Strips", &config.PopOutFlightStrips)

		messagesLeft := !config.MessagesOnRight
		if imgui.RadioButtonBool("Messages left / Flight Strips right", messagesLeft) {
			config.MessagesOnRight = false
		}
		imgui.SameLine()
		if imgui.RadioButtonBool("Messages right / Flight Strips left", !messagesLeft) {
			config.MessagesOnRight = true
		}
		imgui.Separator()
```

- [ ] **Step 2: Verify the package vets clean**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields"`
Expected: no lines.

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: add integrated-pane pop-out and side-layout settings"
```

---

## Task 8: Manual verification

Build and run the application end-to-end. Because the codebase has no automated UI tests, this is the only coverage for the feature.

- [ ] **Step 1: Build**

Run: `go build -o vice.exe ./cmd/vice/`
Expected: successful build. If the pre-existing C-linker error blocks the full build, resolve that first (DLLs in `PATH`, MSYS2 present, etc.) — it is not part of this feature's scope.

- [ ] **Step 2: STARS scaling + default layout**

Launch the app. Open Settings → "Force STARS scope square" → enable. Open Messages (M) and Flight Strips (F).
Expected:
- The scope renders as a centered square with a visible 1 px white border on all four sides.
- Messages fills the entire left letterbox bar (no title bar, no pin thumbtack, cannot be dragged).
- Flight Strips fills the entire right letterbox bar (no title bar, no pin thumbtack, cannot be dragged).
- Neither pane is a separate OS window.

- [ ] **Step 3: Pop-out toggles**

In Settings, toggle "Pop out Messages" on.
Expected: Messages leaves the left bar and becomes a floating OS window with its title bar and pin thumbtack restored. The left bar is empty black.

Toggle "Pop out Flight Strips" on. Expected: symmetric behavior on the right bar. Toggle both off again. Expected: both panes re-dock.

- [ ] **Step 4: Side-layout swap**

Select "Messages right / Flight Strips left".
Expected: the two panes swap bars. No overlap in any state. Toggle back and confirm the reverse.

- [ ] **Step 5: Scaling mode off**

In Settings, uncheck both "Force STARS scope square" and "Force ERAM scope square".
Expected:
- The scope fills the full display area; there is NO border (border is square-mode-only per the spec).
- Messages and Flight Strips revert to floating OS windows regardless of the pop-out settings.

- [ ] **Step 6: Resize while docked**

Re-enable STARS square mode, with both panes docked. Drag the main-window edge to resize.
Expected: the letterbox bars (and the docked panes) track the new size smoothly; no visual tearing or lingering at the old position.

- [ ] **Step 7: Persistence across restart**

Quit and relaunch.
Expected: pop-out and side-layout settings persist. Previously-docked panes remain docked; previously-popped-out panes remain popped out.

- [ ] **Step 8: Degenerate case — exactly square window**

Resize the main window so width == height (a square app window with no letterbox).
Expected: because `barWidth <= 0`, both panes fall back to floating OS windows even with pop-out off. No zero-width ImGui windows are created.

- [ ] **Step 9: Final commit if any fixes were needed**

If any verification step surfaced a defect, fix it in a follow-up commit per task. Otherwise, no additional commit is required.

---

## Self-review checklist (for plan author)

- Spec §1 (STARS border): Task 1. ✓
- Spec §2 (docked layout math): Task 6. ✓
- Spec §3 (config fields + settings UI): Tasks 2 and 7. ✓
- Spec §4 (behavior matrix): covered by Task 6's conditional fall-through and verified in Task 8 steps 2/3/5. ✓
- Spec §5 (defaults): Task 2 uses zero-value-safe field naming (`MessagesOnRight`); spec's wording of "`MessagesOnLeft` default true" is implemented by inverting the field name — design intent preserved. ✓
- Spec "Error handling" (`barWidth <= 0` fallback): Task 6's `dockPane` returns `docked=false` when `barWidth <= 0`. Verified in Task 8 step 8. ✓
- Spec "Testing": Task 8 implements every manual checklist item. ✓
- Spec "Rejected alternatives": no implementation needed. ✓

No placeholders; no inter-task type drift (`dockedFlags imgui.WindowFlags` is used identically in Tasks 4, 5, 6; `MessagesOnRight` is used identically in Tasks 2, 6, 7).
