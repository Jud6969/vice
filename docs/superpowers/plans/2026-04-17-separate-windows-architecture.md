# Separate-Windows Architecture Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the always-visible main window with a multi-window architecture where a standalone home dialog (scenario manager with "Launch Previous Scenario") shows at launch, the radar window appears only while connected, and Messages / Flight Strips / Settings / Launch Control / Scenario Info each render as their own OS-level window with custom title bars.

**Architecture:** One GLFW window remains (the radar). It is hidden at startup and while disconnected. All other windows are imgui windows promoted to standalone OS viewports via `ViewportFlagsNoAutoMerge | ViewportFlagsNoDecoration`, each drawing a shared custom title bar inside the window.

**Tech Stack:** Go, GLFW (via `platform/glfw.go`), cimgui-go (imgui + multi-viewport), existing in-house OpenGL renderer.

**Spec reference:** `docs/superpowers/specs/2026-04-17-separate-windows-architecture.md`

**Testing notes:** The repo has no automated UI tests. Each task uses compile checks (`go build -o /dev/null ./<package>/` or `go vet ./cmd/vice/`). The full `cmd/vice` link has a pre-existing MSYS2/onnxruntime issue that is out of scope; per-package compile verification is authoritative. A dedicated manual-verification task (Task 11) exercises every behavior in the spec.

---

## Task 1: Revert integrated-panes code (clean slate)

The prior plan landed 6 commits (`1d46e36a`…`2bfc63ea`) that built letterbox docking. This plan replaces that approach. Direct edits — no `git revert` — because several of these files are also touched by later tasks and intermediate revert commits would pollute history.

**Files:**
- Modify: `cmd/vice/config.go` — remove `PopOutMessages`, `PopOutFlightStrips`, `MessagesOnRight` fields.
- Modify: `cmd/vice/ui.go` — remove `applyDockedWindowClass` helper, the `dockPane` closure and letterbox math block, the integrated-panes settings UI.
- Modify: `panes/messages.go` — revert `DrawWindow` to its pre-integrated-panes signature (no `dockedFlags`).
- Modify: `panes/flightstrip.go` — revert `DrawWindow` to its pre-integrated-panes signature (no `dockedFlags`).

- [ ] **Step 1: Remove config fields**

In `cmd/vice/config.go`, delete the three-block insertion (the one that starts with the comment `// Pop-out toggles for panes that can integrate into the letterbox bars`). The block is:

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

Delete it entirely. The surrounding `ShowFlightStrips bool` line (above) and `AskedDiscordOptIn bool` line (below) should become adjacent again.

- [ ] **Step 2: Remove `applyDockedWindowClass` helper from `cmd/vice/ui.go`**

Find the function and its block comment (immediately below `applyPinWindowClass`). Delete:

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

- [ ] **Step 3: Replace the docking draw block with the pre-feature floating version**

In `cmd/vice/ui.go`, locate the block that starts with the comment `// Layout math for docked (integrated) panes.` (inside the `if controlClient != nil && !hasActiveModalDialogs()` body). Replace the entire block — from the `displaySize := p.DisplaySize()` line through the closing brace of the `if ui.showFlightStrips {...}` block — with this restored pre-feature version:

```go
		if ui.showMessages {
			applyPinWindowClass("Messages", config, p)
			config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, lg)
		}
		if ui.showFlightStrips {
			applyPinWindowClass("Flight Strips", config, p)
			config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config.UnpinnedWindows, lg)
		}
```

- [ ] **Step 4: Remove the integrated-panes settings UI**

In `cmd/vice/ui.go`, find the block that begins with the comment `// Integrated-pane settings. Only meaningful while scope-square` (inside `uiDrawSettingsWindow`). Delete everything from that comment through the trailing `imgui.Separator()`, inclusive:

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

The `imgui.Checkbox("Start in full-screen", ...)` call that follows should now sit directly below the `"Force ERAM scope square"` block's closing brace, as it did before Task 7 of the prior plan.

- [ ] **Step 5: Revert `MessagesPane.DrawWindow` signature**

In `panes/messages.go`, replace the current signature block:

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
```

with:

```go
func (mp *MessagesPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, lg *log.Logger) {
	// Only play sounds if the window has been continuously visible. If
	// more than 250ms have elapsed since the last DrawWindow call, we
	// must have missed frames (window was hidden), so drain accumulated
	// events silently to avoid spamming audio for the backlog.
	now := time.Now()
	playSound := !mp.lastDrawTime.IsZero() && now.Sub(mp.lastDrawTime) < 250*time.Millisecond
	mp.lastDrawTime = now
	mp.processEvents(playSound, c, p, lg)

	imgui.SetNextWindowSizeConstraints(imgui.Vec2{300, 100}, imgui.Vec2{4096, 4096})
	if mp.font != nil {
		mp.font.ImguiPush()
	}
	imgui.BeginV("Messages", show, 0)
	DrawPinButton("Messages", unpinnedWindows, p)
```

- [ ] **Step 6: Revert `FlightStripPane.DrawWindow` signature**

In `panes/flightstrip.go`, replace:

```go
func (fsp *FlightStripPane) DrawWindow(show *bool, c *client.ControlClient,
	p platform.Platform, unpinnedWindows map[string]struct{}, dockedFlags imgui.WindowFlags, lg *log.Logger) {

	fsp.reconcileOrder(c.State.FlightStripACIDs)

	if dockedFlags == 0 {
		imgui.SetNextWindowSizeConstraints(imgui.Vec2{X: 400, Y: 200}, imgui.Vec2{X: 4096, Y: 4096})
	}
	imgui.BeginV("Flight Strips", show, dockedFlags)
	if dockedFlags == 0 {
		DrawPinButton("Flight Strips", unpinnedWindows, p)
	}
```

with:

```go
func (fsp *FlightStripPane) DrawWindow(show *bool, c *client.ControlClient,
	p platform.Platform, unpinnedWindows map[string]struct{}, lg *log.Logger) {

	fsp.reconcileOrder(c.State.FlightStripACIDs)

	imgui.SetNextWindowSizeConstraints(imgui.Vec2{X: 400, Y: 200}, imgui.Vec2{X: 4096, Y: 4096})
	imgui.BeginV("Flight Strips", show, 0)
	DrawPinButton("Flight Strips", unpinnedWindows, p)
```

- [ ] **Step 7: Verify compilation**

Run: `go build -o /dev/null ./panes/`
Expected: exit 0, no output.

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 8: Commit**

```bash
git add cmd/vice/config.go cmd/vice/ui.go panes/messages.go panes/flightstrip.go
git commit -m "revert: undo integrated-panes letterbox approach

Superseded by separate-windows architecture (spec
2026-04-17-separate-windows-architecture.md).

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 2: Add `ShowWindow()` / `HideWindow()` to Platform

These methods back the radar window's show/hide state transitions. Thin wrappers over GLFW.

**Files:**
- Modify: `platform/platform.go` — add two methods to the `Platform` interface near the existing `IconifyWindow()` at line 101.
- Modify: `platform/glfw.go` — add the implementations near the existing `IconifyWindow` method.

- [ ] **Step 1: Find the existing window-state methods in glfw.go**

Run: `grep -n "IconifyWindow\|IsWindowMaximized" platform/glfw.go`
Expected: hits on the `IconifyWindow` / `IsWindowMaximized` method definitions. Use the locations as an anchor for the next step.

- [ ] **Step 2: Add `ShowWindow` and `HideWindow` to the `Platform` interface**

In `platform/platform.go`, immediately above the existing line:

```go
	// IconifyWindow minimizes the window to the taskbar / dock.
	IconifyWindow()
```

insert:

```go
	// ShowWindow makes the main application window visible. Used to
	// reveal the radar window after a scenario is selected.
	ShowWindow()

	// HideWindow hides the main application window. Used to return the
	// app to the home/connect dialog state without tearing down the
	// GLFW context. imgui secondary viewports continue to render.
	HideWindow()

```

- [ ] **Step 3: Add implementations to `glfw.go`**

In `platform/glfw.go`, immediately above the existing `IconifyWindow` method, insert:

```go
func (g *glfwPlatform) ShowWindow() {
	g.window.Show()
}

func (g *glfwPlatform) HideWindow() {
	g.window.Hide()
}

```

- [ ] **Step 4: Verify compilation**

Run: `go build -o /dev/null ./platform/`
Expected: exit 0, no output.

Run: `go build -o /dev/null ./panes/`
Expected: exit 0, no output. (The panes package depends on the `platform.Platform` interface — confirms the new methods don't break consumers.)

- [ ] **Step 5: Commit**

```bash
git add platform/platform.go platform/glfw.go
git commit -m "platform: add ShowWindow/HideWindow methods

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 3: Shared custom-title-bar helper

Introduce one function used by every new OS-level window (home dialog, Messages, Flight Strips, Settings, Launch Control, Scenario Info). Replaces the scattered `DrawPinButton` calls.

**Files:**
- Modify: `cmd/vice/ui.go` — add new helper function near `applyPinWindowClass` (around line 851 area).

The drag behavior uses imgui-space positioning (`imgui.SetWindowPos`) rather than OS-pixel `glfw.SetWindowPos`. imgui multi-viewport may reassert viewport position each frame, so imgui-space is the stable path.

- [ ] **Step 1: Add the helper**

In `cmd/vice/ui.go`, immediately below the existing `applyPinWindowClass` function (which ends with `imgui.SetNextWindowClass(wc)` + `}`), insert:

```go
// applyBorderlessViewportClass configures the next imgui window so that,
// when imgui multi-viewport promotes it to its own OS window, that OS
// window has no native chrome (NoDecoration) and never folds back into
// the main viewport (NoAutoMerge). Call before imgui.BeginV.
func applyBorderlessViewportClass(windowTitle string, config *Config, p platform.Platform) {
	_, unpinned := config.UnpinnedWindows[windowTitle]
	appFocused := p.IsAppFocused()

	wc := imgui.NewWindowClass()
	setFlags := imgui.ViewportFlagsNoAutoMerge | imgui.ViewportFlagsNoDecoration
	clearFlags := imgui.ViewportFlags(0)
	if !unpinned && appFocused {
		setFlags |= imgui.ViewportFlagsTopMost
	} else {
		clearFlags |= imgui.ViewportFlagsTopMost
	}
	wc.SetViewportFlagsOverrideSet(setFlags)
	wc.SetViewportFlagsOverrideClear(clearFlags)
	imgui.SetNextWindowClass(wc)
}

// drawWindowTitleBar draws a custom title bar at the top of the current
// imgui window. Intended for windows that set WindowFlagsNoTitleBar so
// imgui does not draw its own. Returns true if the user clicked the
// close (X) button this frame.
//
//   - title:       text label shown on the left
//   - windowTitle: key used for the pin-thumbtack's UnpinnedWindows set
//                  (usually the same as title, but may differ for the
//                  home dialog which has no pin behavior — pass "" to
//                  skip the pin button)
//   - config:      used by the pin button to read/write UnpinnedWindows
//   - p:           platform handle (pin button uses it for focus state)
//
// Dragging within the title bar's empty space moves the imgui window.
// Must be called immediately after imgui.BeginV.
func drawWindowTitleBar(title, windowTitle string, config *Config, p platform.Platform) (closed bool) {
	style := imgui.CurrentStyle()
	lineHeight := imgui.TextLineHeight() + 2*style.FramePadding().Y
	closeBtnWidth := imgui.CalcTextSize(renderer.FontAwesomeIconTimes).X + 2*style.FramePadding().X
	pinBtnWidth := float32(0)
	if windowTitle != "" {
		pinBtnWidth = imgui.CalcTextSize(renderer.FontAwesomeIconThumbtack).X + 2*style.FramePadding().X
	}

	// Title bar background. Use ImGui's title-bar color so it blends.
	drawList := imgui.WindowDrawList()
	winPos := imgui.WindowPos()
	winSize := imgui.WindowSize()
	barMin := winPos
	barMax := imgui.Vec2{X: winPos.X + winSize.X, Y: winPos.Y + lineHeight}
	barCol := imgui.ColorU32Col(imgui.ColTitleBg)
	if imgui.IsWindowFocused() {
		barCol = imgui.ColorU32Col(imgui.ColTitleBgActive)
	}
	drawList.AddRectFilled(barMin, barMax, barCol)

	// Title text on the left, vertically centered in the bar.
	imgui.SetCursorPos(imgui.Vec2{X: style.FramePadding().X, Y: style.FramePadding().Y})
	imgui.TextUnformatted(title)

	// Right-cluster: optional pin button, then close button.
	rightX := winSize.X - closeBtnWidth - style.FramePadding().X
	if windowTitle != "" {
		rightX -= pinBtnWidth + style.ItemSpacing().X
		imgui.SetCursorPos(imgui.Vec2{X: rightX, Y: style.FramePadding().Y})
		DrawPinButton(windowTitle, config.UnpinnedWindows, p)
		rightX += pinBtnWidth + style.ItemSpacing().X
	}
	imgui.SetCursorPos(imgui.Vec2{X: rightX, Y: style.FramePadding().Y})
	imgui.PushStyleColorVec4(imgui.ColButtonHovered, imgui.Vec4{0.85, 0.15, 0.15, 1})
	imgui.PushStyleColorVec4(imgui.ColButtonActive, imgui.Vec4{0.7, 0.1, 0.1, 1})
	if imgui.Button(renderer.FontAwesomeIconTimes) {
		closed = true
	}
	imgui.PopStyleColorV(2)

	// Drag-to-move: if the user clicks-and-drags in the title-bar strip
	// (and not on a button), move the imgui window.
	mouse := imgui.CurrentIO().MousePos()
	inBar := mouse.X >= barMin.X && mouse.X <= barMax.X &&
		mouse.Y >= barMin.Y && mouse.Y <= barMax.Y
	if inBar && !imgui.IsAnyItemActive() && imgui.IsMouseDraggingV(imgui.MouseButtonLeft, 0) {
		delta := imgui.MouseDragDelta()
		imgui.ResetMouseDragDelta()
		imgui.SetWindowPosVec2(imgui.Vec2{X: winPos.X + delta.X, Y: winPos.Y + delta.Y})
	}

	// Reserve vertical space below the title bar so the caller's content
	// starts below it, not underneath.
	imgui.SetCursorPos(imgui.Vec2{X: 0, Y: lineHeight})
	return closed
}

```

- [ ] **Step 2: Verify it compiles**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

Note: if `imgui.SetWindowPosVec2` is not the exact cimgui-go name, use the equivalent accepting a `Vec2`. Verify with:
`go doc -short github.com/AllenDang/cimgui-go/imgui.SetWindowPos`
The correct spelling in this cimgui-go version will be either `SetWindowPos(Vec2)` or `SetWindowPosVec2(Vec2)` — adjust the helper to match.

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: add applyBorderlessViewportClass + drawWindowTitleBar helpers

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 4: Convert home dialog from modal to inline imgui window

Still with the main GLFW window visible — this task only changes the dialog structure; Task 5 will hide the main window.

**Files:**
- Modify: `cmd/vice/dialogs.go` — add new `uiDrawHomeDialog` function. Keep the existing `ScenarioSelectionModalClient` type for now (it's still referenced by the "New simulation" button); it'll be wired differently in Task 6.
- Modify: `cmd/vice/ui.go` — in `uiDraw`, when disconnected, call `uiDrawHomeDialog` instead of expecting the modal system to render the connect dialog.
- Modify: `cmd/vice/main.go` — remove the single-shot `uiShowConnectOrBenchmarkDialog` call at line 635; the home dialog is now rendered every frame while disconnected.

- [ ] **Step 1: Add `uiDrawHomeDialog`**

In `cmd/vice/dialogs.go`, immediately above the existing `uiShowConnectDialog` function (line 60), insert:

```go
// homeDialog holds persistent state for the home / scenario-manager
// window. Kept as a package-level value because it's a singleton tied
// to the app's disconnected state.
var homeDialog struct {
	simConfig *NewSimConfiguration
	// quitRequested is set when the user clicks the window's close (X)
	// button. The main loop polls it and exits.
	quitRequested bool
}

// uiDrawHomeDialog renders the home / scenario manager as a standalone
// OS window (via imgui multi-viewport + NoAutoMerge + NoDecoration).
// Called every frame by uiDraw when the app is disconnected.
func uiDrawHomeDialog(mgr *client.ConnectionManager, config *Config, p platform.Platform, lg *log.Logger) {
	if homeDialog.simConfig == nil {
		homeDialog.simConfig = MakeNewSimConfiguration(mgr, &config.LastTRACON, lg)
	}

	// Viewport class — borderless, never folds into the main viewport.
	applyBorderlessViewportClass("vice", config, p)
	imgui.SetNextWindowSize(imgui.Vec2{X: 500, Y: 550})

	show := true
	flags := imgui.WindowFlagsNoResize | imgui.WindowFlagsNoCollapse |
		imgui.WindowFlagsNoTitleBar | imgui.WindowFlagsNoSavedSettings
	if !imgui.BeginV("vice", &show, flags) {
		imgui.End()
		return
	}
	if drawWindowTitleBar("vice", "", config, p) {
		// User clicked the home dialog's close (X) — request app quit.
		homeDialog.quitRequested = true
	}

	// Scenario selection UI (inline, not modal). This is the same body
	// used by ScenarioSelectionModalClient.Draw().
	_ = homeDialog.simConfig.DrawScenarioSelectionUI(p, config)

	imgui.Separator()

	// Connect button — behaves like the modal's Next/Create button.
	btnText := homeDialog.simConfig.UIButtonText()
	disabled := homeDialog.simConfig.ScenarioSelectionDisabled(config)
	if disabled {
		imgui.BeginDisabled()
	}
	if imgui.Button(btnText) {
		if homeDialog.simConfig.ShowConfigurationWindow() {
			// Create flow: push the configuration modal on top.
			cfgClient := &ConfigurationModalClient{
				lg:          lg,
				simConfig:   homeDialog.simConfig,
				allowCancel: true,
				platform:    p,
				config:      config,
				mgr:         mgr,
			}
			uiShowModalDialog(NewModalDialogBox(cfgClient, p), false)
		} else {
			// Join flow: start directly.
			homeDialog.simConfig.displayError = homeDialog.simConfig.Start(config)
		}
	}
	if disabled {
		imgui.EndDisabled()
	}

	imgui.End()
}

// homeDialogShouldQuit returns (and clears) the quit request set by the
// home dialog's close button. Main loop calls this each frame.
func homeDialogShouldQuit() bool {
	q := homeDialog.quitRequested
	homeDialog.quitRequested = false
	return q
}
```

- [ ] **Step 2: Remove the startup modal call**

In `cmd/vice/main.go`, find the block around line 634:

```go
	if !mgr.Connected() && !*starsRandoms {
		uiShowConnectOrBenchmarkDialog(mgr, false, config, plat, lg)
	}
```

Delete these three lines. The home dialog now draws every frame automatically from `uiDraw` (Step 3 adds that call), so there's no need for a one-shot trigger. The benchmark/error modal flows that used `uiShowConnectOrBenchmarkDialog` remain in place for the "New simulation" in-app path (line 216) and the server-disconnect path (line 598); those are deferred to Task 6.

- [ ] **Step 3: Wire `uiDrawHomeDialog` into `uiDraw`**

In `cmd/vice/ui.go`, find the block:

```go
	if controlClient != nil && !hasActiveModalDialogs() {
		uiDrawSettingsWindow(controlClient, config, activeRadarPane, p, lg)
```

Immediately above the `if controlClient != nil && !hasActiveModalDialogs() {` line, insert:

```go
	// Home dialog renders whenever the app is disconnected. It is its
	// own OS window (multi-viewport + NoDecoration), independent of the
	// main GLFW window's visibility.
	if (controlClient == nil || !controlClient.Connected()) && !hasActiveModalDialogs() {
		uiDrawHomeDialog(mgr, config, p, lg)
	}

```

- [ ] **Step 4: Wire the home dialog's quit request into the main loop**

In `cmd/vice/main.go`, find the existing shutdown check around line 728:

```go
		if plat.ShouldStop() && !hasActiveModalDialogs() {
```

Replace with:

```go
		if (plat.ShouldStop() || homeDialogShouldQuit()) && !hasActiveModalDialogs() {
```

- [ ] **Step 5: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

Note: the `DrawScenarioSelectionUI` method may have a different exact signature or return type in the current tree. If the compile fails, run `grep -n "DrawScenarioSelectionUI" cmd/vice/simconfig.go` to find the exact signature and adjust the call in Step 1. Likewise for `ScenarioSelectionDisabled` / `UIButtonText` / `ShowConfigurationWindow` / `Start` — confirm they still exist and match the signatures used here.

- [ ] **Step 6: Commit**

```bash
git add cmd/vice/dialogs.go cmd/vice/ui.go cmd/vice/main.go
git commit -m "ui: render home dialog as standalone imgui viewport

Replaces the modal-based connect dialog with an inline imgui window
that renders every frame while disconnected. Main window still
visible at this point; subsequent task hides it.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 5: Hide the main GLFW window at startup; verify multi-viewport

The risk moment: confirm that `glfw.Hide` on the main window doesn't break multi-viewport rendering. If it does, the fallback is `IconifyWindow` — but verify first.

**Files:**
- Modify: `cmd/vice/main.go` — call `plat.HideWindow()` once, after platform/renderer init, before the main loop.

- [ ] **Step 1: Add the hide call**

In `cmd/vice/main.go`, find the line immediately before:

```go
	///////////////////////////////////////////////////////////////////////////
	// Main event / rendering loop
	lg.Info("Starting main loop")
```

Insert above that banner comment:

```go
	// Start with the main (radar) window hidden. The home dialog is its
	// own imgui-multi-viewport OS window and renders independently.
	// ShowWindow is called when a scenario is connected (see where
	// controlClient is first assigned).
	plat.HideWindow()

```

- [ ] **Step 2: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 3: Verify multi-viewport still works with main window hidden (interactive)**

Build a development binary (the repo's normal `go build -o vice.exe ./cmd/vice/` may fail on the pre-existing MSYS2 linker issue — resolve per the repo's usual toolchain setup, then):

Run: the freshly built binary.
Expected:
- No large empty/black main window appears in the task bar.
- The home dialog appears as its own OS window, with a custom title bar ("vice" left, red X right).
- Clicking the X exits the app.

**If the home dialog does NOT appear** (i.e., imgui multi-viewport stops rendering secondary viewports when the primary is hidden), switch the `plat.HideWindow()` call in Step 1 to:

```go
	plat.IconifyWindow()
```

and rebuild. Document the substitution in the commit message (Step 4). This is the pre-declared fallback from the spec.

- [ ] **Step 4: Commit**

```bash
git add cmd/vice/main.go
git commit -m "main: hide radar window at startup; home dialog stands alone

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

(If Step 3 triggered the iconify fallback, replace the message body with: `"main: iconify radar window at startup; Hide breaks multi-viewport rendering"` and proceed.)

---

## Task 6: State transitions — show radar on connect, show home on radar-close

Three paths wire into this state machine:
1. **Connect** (scenario selected successfully) → hide home, show radar.
2. **Radar close (X)** from the custom title bar → hide radar, disconnect, re-open home (the home dialog is drawn automatically by `uiDraw` once `controlClient` is nil or disconnected).
3. **"New simulation"** button in the menu bar → same as radar-close.

**Files:**
- Modify: `cmd/vice/main.go` — inside `uiResetControlClient` / the `NewControlClient` callback (line 583 `uiResetControlClient(c, plat, lg)` area), add `plat.ShowWindow()`.
- Modify: `cmd/vice/ui.go` — in the custom title bar close button (around line 362, the `if imgui.Button(renderer.FontAwesomeIconTimes)` inside the main menu bar), change from `p.CloseWindow()` to the state-transition helper. In the "New simulation" button (around line 215), change to call the same helper. Add a new helper `uiReturnToHomeDialog` near those call sites.

- [ ] **Step 1: Add the state-transition helper**

In `cmd/vice/ui.go`, immediately below `applyBorderlessViewportClass` (from Task 3), insert:

```go
// uiReturnToHomeDialog takes the app from radar-mode back to home-mode.
// Called when the user closes the radar window (title-bar X) or clicks
// "New simulation". Disconnects the active controlClient (so uiDraw
// renders the home dialog on the next frame), hides the radar GLFW
// window, and resets the home-dialog state so the scenario picker
// reflects a fresh session.
func uiReturnToHomeDialog(mgr *client.ConnectionManager, p platform.Platform) {
	mgr.Disconnect()
	p.HideWindow()
	// Force the home dialog to rebuild its simConfig next frame so
	// newly-available servers / TRACONs are picked up.
	homeDialog.simConfig = nil
}
```

- [ ] **Step 2: Rewire the radar's close (X) button**

In `cmd/vice/ui.go`, find the block around line 360 (inside the main menu bar):

```go
		// Tint the close button red on hover to telegraph the destructive action.
		imgui.PushStyleColorVec4(imgui.ColButtonHovered, imgui.Vec4{0.85, 0.15, 0.15, 1})
		imgui.PushStyleColorVec4(imgui.ColButtonActive, imgui.Vec4{0.7, 0.1, 0.1, 1})
		if imgui.Button(renderer.FontAwesomeIconTimes) {
			p.CloseWindow()
		}
		imgui.PopStyleColorV(2)
```

Change the button body from `p.CloseWindow()` to `uiReturnToHomeDialog(mgr, p)`. The block becomes:

```go
		// Tint the close button red on hover to telegraph the destructive action.
		imgui.PushStyleColorVec4(imgui.ColButtonHovered, imgui.Vec4{0.85, 0.15, 0.15, 1})
		imgui.PushStyleColorVec4(imgui.ColButtonActive, imgui.Vec4{0.7, 0.1, 0.1, 1})
		if imgui.Button(renderer.FontAwesomeIconTimes) {
			uiReturnToHomeDialog(mgr, p)
		}
		imgui.PopStyleColorV(2)
```

Note: `mgr` is a parameter of `uiDraw` and is already in scope here.

- [ ] **Step 3: Rewire the "New simulation" button**

In `cmd/vice/ui.go`, find:

```go
		if imgui.Button(renderer.FontAwesomeIconRedo) {
			uiShowConnectOrBenchmarkDialog(mgr, true, config, p, lg)
		}
```

Replace with:

```go
		if imgui.Button(renderer.FontAwesomeIconRedo) {
			uiReturnToHomeDialog(mgr, p)
		}
```

- [ ] **Step 4: Show the radar when a scenario connects**

In `cmd/vice/main.go`, find the `NewControlClient` callback around line 577–585:

```go
		func(c *client.ControlClient) {
			if mgr.LocalServer != nil && mgr.LocalServer.RPCClient != nil && mgr.LocalServer.RPCClient.Client != nil {
				lg.SetCrashReportClient(mgr.LocalServer.RPCClient.Client)
			}
			uiResetControlClient(c, plat, lg)
			controlClient = c
		},
```

Insert `plat.ShowWindow()` after the `controlClient = c` line:

```go
		func(c *client.ControlClient) {
			if mgr.LocalServer != nil && mgr.LocalServer.RPCClient != nil && mgr.LocalServer.RPCClient.Client != nil {
				lg.SetCrashReportClient(mgr.LocalServer.RPCClient.Client)
			}
			uiResetControlClient(c, plat, lg)
			controlClient = c
			plat.ShowWindow()
		},
```

- [ ] **Step 5: Reopen home dialog on server disconnect (error path)**

In `cmd/vice/main.go`, find the `ErrServerDisconnected` arm around line 596:

```go
		case server.ErrServerDisconnected:
			ShowErrorDialog(plat, lg, "Lost connection to the vice server.")
			uiShowConnectOrBenchmarkDialog(mgr, false, config, plat, lg)
```

Replace the `uiShowConnectOrBenchmarkDialog(...)` call with a radar-hide (the home dialog re-appears automatically once `controlClient` is nil):

```go
		case server.ErrServerDisconnected:
			ShowErrorDialog(plat, lg, "Lost connection to the vice server.")
			plat.HideWindow()
			homeDialog.simConfig = nil
```

- [ ] **Step 6: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 7: Commit**

```bash
git add cmd/vice/ui.go cmd/vice/main.go
git commit -m "ui: wire home<->radar state transitions

Connect shows radar; radar close-X and new-sim button return to home.
Server disconnect also hides radar.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 7: Launch Previous Scenario

Persist enough state on a successful connect to re-run it later. Add the button at the top of the home dialog.

**Files:**
- Modify: `cmd/vice/config.go` — add `LastFacility`, `LastGroupName`, `LastScenarioName` fields to `ConfigNoSim`. (Note: today's `LastTRACON` is actually the TRACON name, identical to `Facility` in `NewSimConfiguration` — we keep it for back-compat but add the two additional fields so the scenario within the TRACON is identified.)
- Modify: `cmd/vice/simconfig.go` — at the end of `Start()` (on success), persist `Facility` / `GroupName` / `ScenarioName` into the new Config fields.
- Modify: `cmd/vice/dialogs.go` — in `uiDrawHomeDialog`, add a "Launch Previous" button above the scenario picker. Disabled if any of the three fields is empty OR the combination cannot be resolved by `SetFacility`/`SetScenario`.

- [ ] **Step 1: Add fields to `ConfigNoSim`**

In `cmd/vice/config.go`, inside the `ConfigNoSim` struct, immediately below the existing:

```go
	LastServer    string
	LastTRACON    string
```

insert:

```go
	// LastFacility / LastGroupName / LastScenarioName identify the most
	// recently launched scenario so the home dialog can offer a
	// "Launch Previous" shortcut. Empty strings mean "no previous run";
	// stale values (server/TRACON/scenario no longer resolvable) are
	// detected at dialog-draw time and disable the button.
	LastFacility     string
	LastGroupName    string
	LastScenarioName string
```

- [ ] **Step 2: Persist on successful launch**

In `cmd/vice/simconfig.go`, find the `Start()` method on `*NewSimConfiguration`. At the end of the success path (immediately before the existing `return nil` — or wherever the function returns a nil error), write the three fields:

Run: `grep -n "func (c \*NewSimConfiguration) Start" cmd/vice/simconfig.go`
to locate the function, then locate the final `return nil` in it.

Immediately before that `return nil`, insert:

```go
	// Persist enough state for the home dialog's "Launch Previous"
	// button to reconstitute this scenario on the next session.
	config.LastFacility = c.Facility
	config.LastGroupName = c.GroupName
	config.LastScenarioName = c.ScenarioName
```

(If `Start` takes a `*Config` parameter under a different name — e.g., `cfg` — adjust accordingly. The three lines use whatever the parameter name is.)

- [ ] **Step 3: Add the Launch Previous button to the home dialog**

In `cmd/vice/dialogs.go`, in the `uiDrawHomeDialog` function, find the line:

```go
	// Scenario selection UI (inline, not modal). This is the same body
	// used by ScenarioSelectionModalClient.Draw().
	_ = homeDialog.simConfig.DrawScenarioSelectionUI(p, config)
```

Insert immediately before it:

```go
	// Launch Previous Scenario — primary action, populated from Config.
	hasPrev := config.LastFacility != "" && config.LastGroupName != "" && config.LastScenarioName != ""
	canResolvePrev := hasPrev && homeDialog.simConfig.CanResolveScenario(
		config.LastFacility, config.LastGroupName, config.LastScenarioName)
	label := "Launch Previous Scenario"
	if hasPrev {
		label = fmt.Sprintf("Launch Previous: %s / %s / %s",
			config.LastFacility, config.LastGroupName, config.LastScenarioName)
	}
	if !canResolvePrev {
		imgui.BeginDisabled()
	}
	if imgui.Button(label) {
		homeDialog.simConfig.SetFacility(config.LastFacility)
		homeDialog.simConfig.SetScenario(config.LastGroupName, config.LastScenarioName)
		homeDialog.simConfig.displayError = homeDialog.simConfig.Start(config)
	}
	if !canResolvePrev {
		imgui.EndDisabled()
		if imgui.IsItemHovered() {
			imgui.SetTooltip("No previous scenario available")
		}
	}
	imgui.Separator()

```

- [ ] **Step 4: Add `CanResolveScenario` to `NewSimConfiguration`**

In `cmd/vice/simconfig.go`, add a new method near the existing `SetScenario` method:

```go
// CanResolveScenario reports whether the given facility/group/scenario
// triple can be located in the current catalog. Used by the home
// dialog's Launch Previous button to decide whether to enable it.
func (c *NewSimConfiguration) CanResolveScenario(facility, groupName, scenarioName string) bool {
	catalogs := c.mgr.GetScenarioCatalogs()
	if catalogs == nil {
		return false
	}
	facilityCatalogs, ok := catalogs[facility]
	if !ok {
		return false
	}
	groupCatalog, ok := facilityCatalogs[groupName]
	if !ok {
		return false
	}
	_, ok = groupCatalog.Scenarios[scenarioName]
	return ok
}
```

Note: the exact field names on `server.ScenarioCatalog` and the accessor for catalogs on `*client.ConnectionManager` may differ. If the compile fails, `grep -n "type ScenarioCatalog struct\|GetScenarioCatalogs\|selectedFacilityCatalogs" server/ cmd/vice/` to find the correct names and adjust the method body. Keep the method's *signature* (name, params, return) unchanged so Step 3 still compiles.

- [ ] **Step 5: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 6: Commit**

```bash
git add cmd/vice/config.go cmd/vice/simconfig.go cmd/vice/dialogs.go
git commit -m "ui: add Launch Previous Scenario button to home dialog

Persists facility/group/scenario on successful connect; button
disables with tooltip when the persisted triple can't be resolved.

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 8: Apply custom chrome to Messages and Flight Strips

**Files:**
- Modify: `cmd/vice/ui.go` — in the block that draws Messages / Flight Strips (the block restored in Task 1 Step 3), swap `applyPinWindowClass` for `applyBorderlessViewportClass`.
- Modify: `panes/messages.go` — change `DrawWindow` to use `WindowFlagsNoTitleBar` and call `drawWindowTitleBar` instead of the inline `DrawPinButton`. Close button in the shared title bar replaces no explicit imgui close.
- Modify: `panes/flightstrip.go` — same treatment.

- [ ] **Step 1: Update the draw block in `cmd/vice/ui.go`**

Find the block (restored in Task 1):

```go
		if ui.showMessages {
			applyPinWindowClass("Messages", config, p)
			config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config.UnpinnedWindows, lg)
		}
		if ui.showFlightStrips {
			applyPinWindowClass("Flight Strips", config, p)
			config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config.UnpinnedWindows, lg)
		}
```

Replace with:

```go
		if ui.showMessages {
			applyBorderlessViewportClass("Messages", config, p)
			config.MessagesPane.DrawWindow(&ui.showMessages, controlClient, p, config, lg)
		}
		if ui.showFlightStrips {
			applyBorderlessViewportClass("Flight Strips", config, p)
			config.FlightStripPane.DrawWindow(&ui.showFlightStrips, controlClient, p, config, lg)
		}
```

Note: we pass `config` (not `config.UnpinnedWindows`) so the pane can hand it to `drawWindowTitleBar`, which needs it for the pin button.

- [ ] **Step 2: Update `MessagesPane.DrawWindow` signature + body**

In `panes/messages.go`, find the function. `panes/messages.go` can't import `cmd/vice`; the helper `drawWindowTitleBar` lives in `cmd/vice/ui.go`. To avoid the cyclic-import issue, introduce a small function-pointer type in `panes` that the pane calls, with the actual implementation injected from `cmd/vice`.

Restructure is simpler than that, though: the pane doesn't need to know about `*Config`. We extract the essentials into two narrow parameters. Change the signature to:

```go
func (mp *MessagesPane) DrawWindow(show *bool, c *client.ControlClient, p platform.Platform,
	unpinnedWindows map[string]struct{}, lg *log.Logger) {
```

Wait — that's already the pre-feature signature (restored in Task 1). We *keep* it. What changes is the *body*: skip imgui's title bar via flags, and call the draw helpers.

But `drawWindowTitleBar` lives in `cmd/vice`. We can't call it from `panes`. **Resolution:** move the shared title bar helper into the `panes` package (it's reusable chrome — belongs at the same layer as `DrawPinButton`, which is already in `panes`).

Before editing `messages.go`, first move the helpers. See Step 3.

- [ ] **Step 3: Move `drawWindowTitleBar` into the `panes` package**

Cut the `drawWindowTitleBar` function from `cmd/vice/ui.go` (added in Task 3). Paste it into `panes/pinbutton.go` (which already contains `DrawPinButton`), renamed to `DrawTitleBar` (capital D for exported).

Change the function signature to remove the `config *Config` parameter — instead take the pin-unpinnedWindows map directly, matching `DrawPinButton`'s signature:

```go
// DrawTitleBar draws a custom title bar at the top of the current
// imgui window. Same contract as the old inline DrawPinButton call,
// plus a close (X) button and drag-to-move in the title-bar strip.
// Caller is expected to have set WindowFlagsNoTitleBar on Begin.
//
//   - title:           text label on the left
//   - pinWindowTitle:  key used for the UnpinnedWindows map; pass "" to
//                      suppress the pin button (e.g., home dialog)
//   - unpinnedWindows: same map passed to DrawPinButton today
//
// Returns true if the user clicked the close (X) button this frame.
func DrawTitleBar(title, pinWindowTitle string,
	unpinnedWindows map[string]struct{}, p platform.Platform) (closed bool) {
	style := imgui.CurrentStyle()
	lineHeight := imgui.TextLineHeight() + 2*style.FramePadding().Y
	closeBtnWidth := imgui.CalcTextSize(renderer.FontAwesomeIconTimes).X + 2*style.FramePadding().X
	pinBtnWidth := float32(0)
	if pinWindowTitle != "" {
		pinBtnWidth = imgui.CalcTextSize(renderer.FontAwesomeIconThumbtack).X + 2*style.FramePadding().X
	}

	drawList := imgui.WindowDrawList()
	winPos := imgui.WindowPos()
	winSize := imgui.WindowSize()
	barMin := winPos
	barMax := imgui.Vec2{X: winPos.X + winSize.X, Y: winPos.Y + lineHeight}
	barCol := imgui.ColorU32Col(imgui.ColTitleBg)
	if imgui.IsWindowFocused() {
		barCol = imgui.ColorU32Col(imgui.ColTitleBgActive)
	}
	drawList.AddRectFilled(barMin, barMax, barCol)

	imgui.SetCursorPos(imgui.Vec2{X: style.FramePadding().X, Y: style.FramePadding().Y})
	imgui.TextUnformatted(title)

	rightX := winSize.X - closeBtnWidth - style.FramePadding().X
	if pinWindowTitle != "" {
		rightX -= pinBtnWidth + style.ItemSpacing().X
		imgui.SetCursorPos(imgui.Vec2{X: rightX, Y: style.FramePadding().Y})
		DrawPinButton(pinWindowTitle, unpinnedWindows, p)
		rightX += pinBtnWidth + style.ItemSpacing().X
	}
	imgui.SetCursorPos(imgui.Vec2{X: rightX, Y: style.FramePadding().Y})
	imgui.PushStyleColorVec4(imgui.ColButtonHovered, imgui.Vec4{0.85, 0.15, 0.15, 1})
	imgui.PushStyleColorVec4(imgui.ColButtonActive, imgui.Vec4{0.7, 0.1, 0.1, 1})
	if imgui.Button(renderer.FontAwesomeIconTimes) {
		closed = true
	}
	imgui.PopStyleColorV(2)

	mouse := imgui.CurrentIO().MousePos()
	inBar := mouse.X >= barMin.X && mouse.X <= barMax.X &&
		mouse.Y >= barMin.Y && mouse.Y <= barMax.Y
	if inBar && !imgui.IsAnyItemActive() && imgui.IsMouseDraggingV(imgui.MouseButtonLeft, 0) {
		delta := imgui.MouseDragDelta()
		imgui.ResetMouseDragDelta()
		imgui.SetWindowPosVec2(imgui.Vec2{X: winPos.X + delta.X, Y: winPos.Y + delta.Y})
	}

	imgui.SetCursorPos(imgui.Vec2{X: 0, Y: lineHeight})
	return closed
}
```

Update any references in `cmd/vice/ui.go` / `cmd/vice/dialogs.go`: the home dialog's call changes from `drawWindowTitleBar("vice", "", config, p)` to:

```go
	if panes.DrawTitleBar("vice", "", config.UnpinnedWindows, p) {
		homeDialog.quitRequested = true
	}
```

- [ ] **Step 4: Update `MessagesPane.DrawWindow` body to use the title bar**

In `panes/messages.go`, replace:

```go
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{300, 100}, imgui.Vec2{4096, 4096})
	if mp.font != nil {
		mp.font.ImguiPush()
	}
	imgui.BeginV("Messages", show, 0)
	DrawPinButton("Messages", unpinnedWindows, p)
```

with:

```go
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{300, 100}, imgui.Vec2{4096, 4096})
	if mp.font != nil {
		mp.font.ImguiPush()
	}
	imgui.BeginV("Messages", show, imgui.WindowFlagsNoTitleBar)
	if DrawTitleBar("Messages", "Messages", unpinnedWindows, p) {
		*show = false
	}
```

- [ ] **Step 5: Update `FlightStripPane.DrawWindow` body similarly**

In `panes/flightstrip.go`, replace:

```go
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{X: 400, Y: 200}, imgui.Vec2{X: 4096, Y: 4096})
	imgui.BeginV("Flight Strips", show, 0)
	DrawPinButton("Flight Strips", unpinnedWindows, p)
```

with:

```go
	imgui.SetNextWindowSizeConstraints(imgui.Vec2{X: 400, Y: 200}, imgui.Vec2{X: 4096, Y: 4096})
	imgui.BeginV("Flight Strips", show, imgui.WindowFlagsNoTitleBar)
	if DrawTitleBar("Flight Strips", "Flight Strips", unpinnedWindows, p) {
		*show = false
	}
```

- [ ] **Step 6: Verify compilation**

Run: `go build -o /dev/null ./panes/`
Expected: exit 0.
Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 7: Commit**

```bash
git add panes/pinbutton.go panes/messages.go panes/flightstrip.go cmd/vice/ui.go cmd/vice/dialogs.go
git commit -m "ui: custom title bar on Messages and Flight Strips windows

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 9: Apply custom chrome to Settings, Launch Control, Scenario Info

**Files:**
- Modify: `cmd/vice/ui.go` — `uiDrawSettingsWindow` (line ~937): swap pin-window-class → borderless-viewport-class; add NoTitleBar flag; call `panes.DrawTitleBar` after `BeginV`.
- Modify: `cmd/vice/ui.go` — `drawScenarioInfoWindow` and `LaunchControlWindow.Draw` — same treatment. (Locate via `grep -n "drawScenarioInfoWindow\|func.*LaunchControlWindow.*Draw" cmd/vice/*.go`.)

- [ ] **Step 1: Update `uiDrawSettingsWindow`**

Find the `imgui.BeginV("Settings", ...)` call inside `uiDrawSettingsWindow` and the immediately-preceding `applyPinWindowClass("Settings", ...)` call.

Replace the `applyPinWindowClass("Settings", config, p)` call with:

```go
	applyBorderlessViewportClass("Settings", config, p)
```

Then, in the `imgui.BeginV("Settings", ...)` call, OR `imgui.WindowFlagsNoTitleBar` into the existing flags argument. Immediately after `BeginV`, insert:

```go
	if panes.DrawTitleBar("Settings", "Settings", config.UnpinnedWindows, p) {
		ui.showSettings = false
	}
```

(If the existing code already has a `DrawPinButton("Settings", ...)` call right after `BeginV`, replace it with the `DrawTitleBar` call.)

- [ ] **Step 2: Update `drawScenarioInfoWindow`**

Locate the function. Apply the same three changes:
1. `applyPinWindowClass("Scenario Info", config, p)` → `applyBorderlessViewportClass("Scenario Info", config, p)`
2. Add `imgui.WindowFlagsNoTitleBar` to the `BeginV` flags.
3. Immediately after `BeginV`, replace any existing `DrawPinButton("Scenario Info", ...)` call with:

```go
	if panes.DrawTitleBar("Scenario Info", "Scenario Info", config.UnpinnedWindows, p) {
		ui.showScenarioInfo = false
	}
```

- [ ] **Step 3: Update `LaunchControlWindow.Draw`**

Locate the function. Same three-step treatment, using the launch-control-specific `show` variable (likely `ui.showLaunchControl`). The pin title is "Launch Control".

```go
	applyBorderlessViewportClass("Launch Control", config, p)
	// ... existing Begin call, add WindowFlagsNoTitleBar to its flags ...
	if panes.DrawTitleBar("Launch Control", "Launch Control", config.UnpinnedWindows, p) {
		ui.showLaunchControl = false
	}
```

Note: `LaunchControlWindow.Draw`'s signature may not currently take `config`; pass it through if needed.

- [ ] **Step 4: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 5: Commit**

```bash
git add cmd/vice/ui.go cmd/vice/launchcontrol.go
git commit -m "ui: custom title bar on Settings, Scenario Info, Launch Control

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 10: Suppress secondary windows while radar is hidden

When the user closes the radar (returns to home), every secondary window should disappear too, without mutating the `ui.show*` toggles so they restore on the next connect.

**Files:**
- Modify: `cmd/vice/ui.go` — in `uiDraw`, wrap the block that draws Settings / Launch Control / Scenario Info / Messages / Flight Strips in a `controlClient != nil && controlClient.Connected()` check that's stricter than today's `controlClient != nil && !hasActiveModalDialogs()`.

- [ ] **Step 1: Tighten the connected-only block**

Find the existing:

```go
	if controlClient != nil && !hasActiveModalDialogs() {
		uiDrawSettingsWindow(controlClient, config, activeRadarPane, p, lg)

		if ui.showScenarioInfo { ... }
		if ui.showLaunchControl { ... }
		if ui.showMessages { ... }
		if ui.showFlightStrips { ... }
	}
```

Replace the condition with:

```go
	connected := controlClient != nil && controlClient.Connected()
	if connected && !hasActiveModalDialogs() {
		...
	}
```

This is already close to what the code does — the meaningful change is `controlClient.Connected()` (not just `controlClient != nil`). Between Task 6's `mgr.Disconnect()` call and the next frame, `controlClient` may still be non-nil but disconnected; the `Connected()` check ensures secondary windows hide immediately.

- [ ] **Step 2: Verify compilation**

Run: `go vet ./cmd/vice/ 2>&1 | grep -v "struct literal uses unkeyed fields" | grep -v "^#"`
Expected: empty output.

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: suppress secondary windows while radar is hidden

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

## Task 11: Manual verification

No automated UI tests in this repo. This is the feature's only coverage.

- [ ] **Step 1: Build**

Run: `go build -o vice.exe ./cmd/vice/`
If the pre-existing MSYS2/onnxruntime linker error blocks the full build, resolve it (DLLs in PATH, MSYS2 installed) before proceeding. This is not part of this feature.

- [ ] **Step 2: Launch → home dialog only**

Run the binary.
Expected:
- No large empty window. No black canvas.
- A single OS window appears: the home dialog, with a custom title bar ("vice" on the left, red X on the right).
- The Task 5 risk is validated: multi-viewport rendering works with the main GLFW window hidden.

- [ ] **Step 3: Pick a scenario via the full picker → radar appears**

Select a server, TRACON, and scenario; click Connect (or Create + Start for create flows).
Expected:
- Home dialog hides.
- Radar window appears with scope + top menu bar.

- [ ] **Step 4: Open Messages + Flight Strips → separate OS windows**

Click the Messages icon and Flight Strips icon in the radar's menu bar.
Expected:
- Each appears as its own OS-level window (separate entry in Alt-Tab / task bar).
- Each has a custom title bar with title on the left, pin thumbtack + red X on the right.
- Drag in the title-bar strip moves the window. Click X hides the window.
- Click the pin thumbtack: window toggles always-on-top when the app is focused (existing behavior).

- [ ] **Step 5: Open Settings, Launch Control, Scenario Info → separate OS windows**

Open each via its menu-bar button.
Expected: same treatment as Messages / Flight Strips — custom title bar, pin, close.

- [ ] **Step 6: Close radar (X) → back to home**

Click the red X in the radar's menu bar.
Expected:
- Radar window hides.
- All secondary windows vanish.
- Home dialog reappears (same position as before).

- [ ] **Step 7: Launch Previous Scenario**

In the home dialog, click "Launch Previous: ...".
Expected: connects immediately to the same scenario as the prior session, radar reappears, no need to touch the picker.

- [ ] **Step 8: Close home dialog (X) → app quits**

Click the red X on the home dialog.
Expected: process exits cleanly.

- [ ] **Step 9: Persistence across restart**

Launch. Confirm:
- "Launch Previous" button label reflects the last-used scenario (not empty).
- Home dialog position restored (not centered if you'd moved it).
- After connecting: radar size/position restored; Messages / Flight Strips / etc. restored to their last positions via imgui settings.

- [ ] **Step 10: Fullscreen still works**

On the radar, click the maximize button until fullscreen; exit. Relaunch.
Expected: radar restores to its pre-fullscreen windowed geometry, not monitor-size-at-(0,0). The prior fullscreen-geometry fix (commit `4400793a`) remains effective.

- [ ] **Step 11: "Start in full-screen" checkbox**

In the home dialog, tick "Start in full-screen". Pick a scenario.
Expected: radar opens fullscreen.

- [ ] **Step 12: Scope-square mode**

With a scenario connected, open Settings → enable "Force STARS scope square".
Expected: scope renders as a centered square inside the radar window; no letterbox-docked panes (confirms Task 1's revert took).

- [ ] **Step 13: Multi-monitor drag**

Drag the home dialog across monitors with different DPI. Expected: it follows the mouse smoothly; no off-screen disappearance; title bar stays grabbable. Repeat for the radar and Messages.

- [ ] **Step 14: Degenerate — server disconnect mid-sim**

If you can trigger it (e.g., multi-controller server goes away), confirm: an error dialog appears; dismissing it hides the radar and re-shows the home dialog. (This exercises Task 6 Step 5.)

- [ ] **Step 15: Final commit if any fixes were needed**

If any verification step surfaced a defect, fix it in a follow-up commit against the appropriate task. Otherwise no additional commit is required.

---

## Self-review checklist (for plan author)

- Spec "Goal" (home dialog + separate radar + separate secondary windows): Tasks 4 (home dialog), 5 (hide radar), 6 (transitions), 8/9 (secondary chrome). ✓
- Spec "State machine": Task 6 implements all four transitions (launch → home, connect → radar, radar-close → home, home-close → quit). ✓
- Spec "Home dialog" layout: Task 4 renders it; Task 7 adds the Launch Previous button at the top. ✓
- Spec "Radar window" no-change claim: Task 1 reverts the integrated-panes regression; Tasks 5/6 only hide/show, don't restructure. ✓
- Spec "Secondary windows" common treatment + custom title bar + pin retained + per-window resize policy: Tasks 3, 8, 9. Settings / Scenario Info keep their existing fixed-size / no-resize behavior (Task 9 doesn't add NoResize override). ✓
- Spec "Modals" stay as-is: no task changes modal plumbing. ✓
- Spec "Revert integrated-panes": Task 1. ✓
- Spec "Risks" hidden primary viewport (Risk 1): Task 5 Step 3 is the empirical check, with pre-declared iconify fallback. ✓
- Spec "Risks" drag on borderless viewport (Risk 2): Task 3's helper uses imgui-space `SetWindowPos` for drag, not OS-pixel deltas. ✓
- Spec "Risks" Launch Previous robustness (Risk 3): Task 7's `CanResolveScenario` gate + disabled-tooltip. ✓
- Spec "Testing": every checklist item in Task 11. ✓

No unimplemented spec requirements. No placeholders in the plan body (the `grep` / `go doc` verifications are verification steps, not placeholders). Signature names (`DrawTitleBar`, `applyBorderlessViewportClass`, `CanResolveScenario`, `uiReturnToHomeDialog`, `homeDialog`, `homeDialogShouldQuit`) are used identically across tasks.
