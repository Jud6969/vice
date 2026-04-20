# Window-Aspect Scope-Square Design

**Date:** 2026-04-20
**Branch:** `windowed-border`

## Goal

Change the "Force STARS scope square" / "Force ERAM scope square" settings
so they lock the radar window itself to a 1:1 aspect ratio instead of
merely squaring the scope pane inside a free-aspect window with letterbox
bars. When either mode is active, the radar window is always square, the
scope pane fills the entire window, and fullscreen is disabled (since no
non-square monitor can satisfy a 1:1 aspect fullscreen).

## Scope

In scope:
- GLFW aspect-ratio lock (1:1) applied to the radar window while
  `WindowScaleMode != ""`.
- On toggle-on, snap the window to `min(WindowScaleTargets[mode],
  shorter_monitor_dim)`, keeping the `SquareScopePaneMinWindow = 1000`
  floor. Clamp window position so the resized window stays on screen.
- Disable fullscreen while `WindowScaleMode != ""`: grey out the
  "Start in full-screen" checkbox in Settings, force-clear
  `config.StartInFullScreen` when the user enables either square toggle,
  and short-circuit any runtime path that enters fullscreen.
- Startup path: if `WindowScaleMode != ""` on load, apply the same
  aspect lock and snap the initial window size to the target. Existing
  saved configs with a non-square `InitialWindowSize` migrate cleanly.

Out of scope:
- Removing the existing draw-time pane-squaring logic in `panes/display.go`
  and the centered-sub-region handling in `stars/dcb.go`. They become
  no-ops when the window is already square; leaving them in keeps the
  PR tight and serves as a safety net for any edge case we haven't
  anticipated.
- Any change to behavior when both square toggles are off.
- Any change to the STARS vs ERAM distinction beyond honoring the
  separate target sizes from `WindowScaleTargets` during the snap.

## Architecture

### Component 1 — Aspect lock and initial snap
File: `platform/glfw.go`

Extend `SetMainWindowSquare(square bool)`:
- When `square` is true:
  - Continue installing the `SquareScopePaneMinWindow` size floor.
  - Call `g.window.SetAspectRatio(1, 1)`.
  - Compute `target = min(WindowScaleTargets[g.config.WindowScaleMode],
    shorter_monitor_dim_for_current_window)`; clamp below by
    `SquareScopePaneMinWindow`.
  - If the current window size is not already square at `target`,
    `g.window.SetSize(target, target)`. Adjust window position if the
    new size would push the window off-screen (use
    `MainWindowMonitorWorkArea`).
- When `square` is false:
  - Clear the aspect lock (`g.window.SetAspectRatio(glfw.DontCare,
    glfw.DontCare)`).
  - Clear the size floor as today.
  - Leave window size as-is.

In `New(...)`, after the existing "If scope-square mode is active,
ensure the initial window is at least the minimum" block:
- If `config.WindowScaleMode != ""`:
  - Recompute the initial window size using the snap rule above,
    clamped to the monitor the window will open on.
  - Set aspect ratio 1:1 after window creation (same call as above).

### Component 2 — Fullscreen blocking
Files: `platform/fullscreen_linux.go`, `platform/fullscreen_windows.go`,
`platform/fullscreen_darwin.go`.

At the top of the `EnableFullScreen` entrypoint(s), early-return if
`g.config.WindowScaleMode != ""`. The Settings-UI checkbox is the
primary guard; this is belt-and-braces for any other caller (keybinding,
menu shortcut).

### Component 3 — Settings UI
File: `cmd/vice/ui.go` (the `uiDrawSettingsWindow` function, around the
existing "Force STARS scope square" / "Force ERAM scope square"
checkboxes at lines ~1017–1034).

Inside each checkbox handler, when enabling the square toggle
(`starsOn`/`eramOn` transitions to true):
- Clear `config.StartInFullScreen`.
- Call `p.EnableFullScreen(false)` if the platform is currently
  fullscreen (so enabling the toggle immediately drops out of
  fullscreen rather than waiting for a restart).

For the `Start in full-screen` checkbox (around line 1036): wrap it in
`imgui.BeginDisabled(config.WindowScaleMode != "")` /
`imgui.EndDisabled()` so it greys out while square mode is active.

## Data Flow

On enabling STARS square from Settings:
1. `starsOn` becomes true → `config.WindowScaleMode = "stars"`.
2. `config.StartInFullScreen` cleared; fullscreen dropped if active.
3. `p.SetMainWindowSquare(true)` → aspect locked to 1:1 and window
   snapped to `min(2075, shorter_monitor_dim)`.

On disabling:
1. `starsOn` becomes false → `config.WindowScaleMode = ""`.
2. `p.SetMainWindowSquare(false)` → aspect lock cleared, size floor
   cleared, window keeps its current (square) dimensions until the
   user resizes.

On startup with `WindowScaleMode = "stars"` already persisted:
1. `New` detects the mode, snaps `InitialWindowSize` to target, creates
   the window.
2. After creation, applies aspect lock 1:1 and size floor.
3. Fullscreen path is short-circuited if `StartInFullScreen` was
   somehow still true (migration safety).

## Error Handling

- If `WindowScaleTargets[mode]` is missing (unknown mode string in a
  corrupted config), fall back to `SquareScopePaneMinWindow` and log
  a warning. Don't panic.
- If the computed target is smaller than `SquareScopePaneMinWindow`
  (e.g., tiny monitor), use the floor and accept that the window may
  exceed the monitor; this matches the existing floor's behavior.
- GLFW's `SetAspectRatio` is best-effort; on platforms where it's
  not honored, the pre-existing draw-time pane-squaring logic still
  produces a correct-looking scope (the reason we keep it in place).

## Testing

Manual verification:
1. 1080p monitor, windowed: enable STARS square → window snaps to
   1080×1080 (clamped). Drag corner → stays square.
2. 1440p monitor, windowed: STARS snaps to 1440×1440; ERAM to
   1440×1440 (both clamp to monitor).
3. 4K monitor, windowed: STARS snaps to 2075×2075; ERAM to 2160×2160.
4. Enable square mode while in fullscreen → drops out of fullscreen;
   "Start in full-screen" checkbox is greyed.
5. Try to enter fullscreen via any keybinding while square mode is on →
   ignored.
6. Disable square mode → window stays at current size; aspect lock
   cleared; resize becomes free again; fullscreen checkbox re-enabled.
7. Save-and-restart with `WindowScaleMode = "stars"` and a previously
   non-square `InitialWindowSize` → restart opens a square window at
   target size.
8. Interaction with the custom Windows title bar
   (`platform/titlebar_windows.go`): resizing via the bottom-right
   corner respects the 1:1 lock.

## Open items (verify during implementation)

- `platform/titlebar_windows.go` does custom hit-testing for resize;
  confirm it doesn't bypass GLFW's aspect-ratio enforcement. If it
  does, clamp in the resize callback.
