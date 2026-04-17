# Separate-Windows Architecture

**Date:** 2026-04-17
**Status:** Approved design, ready for implementation plan.

## Goal

Replace the current "one big window that's empty while disconnected" UX with a multi-window architecture:

- Launching the app shows only a **home dialog** (scenario manager), as its own OS window. No large empty canvas behind it.
- Picking a scenario hides the home dialog and reveals a dedicated **radar window** containing the scope + menu bar.
- Auxiliary panels (**Messages, Flight Strips, Settings, Launch Control, Scenario Info**) always render as their own OS-level popup windows with custom title bars.
- Closing the radar window returns to the home dialog. Closing the home dialog quits the app.

## Non-goals

- Multiple simultaneous radar windows / multi-sim. Still one active `controlClient` at a time.
- Replacing cimgui-go / imgui multi-viewport with a custom window system.
- Moving scope rendering to an offscreen texture. The radar keeps drawing directly to the main GLFW framebuffer (Approach 3 from brainstorming, rejected).
- Creating a second real GLFW window for the home dialog (Approach 2 from brainstorming, rejected). imgui's existing multi-viewport system handles it.

## Architecture

**One GLFW window.** The existing main GLFW window is *the radar window*. It is shown only while a scenario is connected; otherwise it is hidden.

**Everything else is an imgui window promoted to its own OS viewport.** Using the flags `ViewportFlagsNoAutoMerge | ViewportFlagsNoDecoration`, each imgui window becomes a real OS window with no native chrome. We draw a custom title bar inside each window.

**State machine:**

```
[Launch]
  └─► radar window HIDDEN
      home dialog VISIBLE (own OS window)
                │
  Pick scenario │
                ▼
      home dialog HIDDEN
      radar window SHOWN
      Messages / Flight Strips / etc. SHOWN per persisted toggles
                │
 Close radar    │
 (title-bar X)  ▼
      radar window HIDDEN
      all secondary windows HIDDEN
      home dialog VISIBLE
                │
 Close home     │
 dialog (X)     ▼
      App quits
```

The **"New simulation"** button in the radar menu bar follows the same path as closing the radar: hide radar, reopen home dialog.

**Risk — hidden primary viewport.** imgui multi-viewport depends on the primary GLFW window for event processing. `glfw.HideWindow` keeps the event loop running on Windows, so secondary viewports should continue rendering and receiving input. If this fails in practice, the fallback is `glfw.IconifyWindow` or parking the window at 1×1 off-screen. Verify empirically during implementation Task 1; do not commit to the fallback until measured.

## Home dialog (scenario manager)

**Layout, top to bottom:**

1. **Custom title bar:** "vice" label on the left, close (X) button on the right. Dragging empty space in the title bar moves the window. No minimize / maximize.
2. **Primary action strip:** a prominent **"Launch Previous Scenario"** button, showing the last-used scenario name inline (e.g., *"Launch Previous: N90 / JFK Final"*). Disabled with a tooltip ("No previous scenario") when no previous scenario is persisted or the persisted reference can't be resolved (server offline, TRACON removed, etc.).
3. Thin separator.
4. **Full scenario manager:** the existing connect UI (server dropdown, TRACON picker, scenario picker, Connect button). Same controls as today, just rendered inline instead of inside a modal frame.
5. Footer: the existing "Start in full-screen" checkbox.

**Imgui flags:** `NoAutoMerge | NoDecoration | NoResize | NoCollapse | NoSavedSettings` on the viewport/window class. Size approximately 500×550 px (subject to tuning). Position persists via a new `HomeDialogPosition [2]int` config field; first launch centers on the primary monitor.

**"Launch Previous" semantics:** runs the same code path as the full picker's Connect button but with the persisted prior selection applied. This requires persisting enough state to uniquely identify the last scenario. Today's config has `LastServer` and `LastTRACON`; the implementation plan must add whatever else is needed (likely the scenario name within the TRACON, and possibly the controller-position / role selection). The exact fields will be scoped during planning by reading `ScenarioSelectionModalClient`.

**Close (X):** quits the app. (The dialog is only visible while disconnected, so close always means quit.)

## Radar window

**Identity:** the existing main GLFW window. Custom title bar and menu bar as today. No structural change to scope rendering — `panes.DrawPanes` continues to draw against the main framebuffer.

**Menu-bar contents (unchanged from current code):**

- Pause / Fast-forward (when connected, sim-specific)
- "New simulation" (redo icon) — now hides the radar and reopens the home dialog
- Settings, Scenario Info, Keyboard ref, Launch Control, Messages toggle, Flight Strips toggle
- About, Discord
- Right cluster: microphone indicator, minimize, maximize/fullscreen-restore, close

**Close (X):** hides radar, hides all secondary windows, shows home dialog. Does **not** quit the app.

**Fullscreen / maximize / minimize:** existing logic applies unchanged. The previous fullscreen-geometry fix (commit `4400793a`) continues to work.

**Size / position persistence:** continues via the same `InitialWindowSize` / `InitialWindowPosition` fields, with the "only persist from a real windowed state" guard already in place.

**What goes away (revert from previous integrated-panes work):**

- `PopOutMessages`, `PopOutFlightStrips`, `MessagesOnRight` config fields
- `dockPane` closure in `cmd/vice/ui.go`
- `applyDockedWindowClass` helper
- `dockedFlags` parameter on `MessagesPane.DrawWindow` and `FlightStripPane.DrawWindow`
- Settings-UI controls for pop-out / side layout

Scope-square mode stays as-is; the radar still renders the scope as a centered square when that mode is active, with a natural black letterbox on the sides.

## Secondary windows

**Applies to:** Messages, Flight Strips, Settings, Launch Control, Scenario Info. (And the home dialog itself reuses the same chrome primitives.)

**Common flags:**

- Window class: `ViewportFlagsNoAutoMerge | ViewportFlagsNoDecoration` (existing `applyPinWindowClass` already sets `NoAutoMerge`; extend or replace to also set `NoDecoration`).
- `imgui.BeginV` flag: `WindowFlagsNoTitleBar` (so imgui does not draw its own title bar — we draw ours inside).

**Shared custom-title-bar helper** — new function, roughly:

```go
drawWindowTitleBar(title string, show *bool, config *Config, p platform.Platform)
```

Called immediately after `imgui.BeginV`. Responsibilities:

- Render the title text (left).
- Render the **pin thumbtack** (replaces the inline `DrawPinButton` calls today). Toggles membership in `config.UnpinnedWindows`.
- Render the **close (X) button** (right). Sets `*show = false`.
- Provide **drag-to-move** behavior on empty space in the title bar. Implementation: prefer imgui's built-in window-move path (`imgui.IsWindowHovered` + manual `imgui.SetWindowPos` based on mouse delta) over `glfw.SetWindowPos`, since imgui may reassert viewport position each frame and compete with GLFW-level moves.

No minimize / maximize on secondary windows — they're utility windows.

**Per-window resize policy:**

- **Messages, Flight Strips, Launch Control:** resizable via OS window edges (imgui's default when `NoResize` is not set).
- **Settings, Scenario Info:** fixed size (existing behavior preserved — they set `NoResize` / use `FixedSizeDialogClient` today).

**Visibility rule:** each secondary window is rendered when `ui.show<Name>` is true AND the radar is currently visible. When the radar hides (return to home), all secondary windows are suppressed without mutating their `show*` toggles, so they restore on the next scenario launch.

**Sizes / positions:** imgui's existing `ImGuiSettings` persistence in `Config.ImGuiSettings` continues to handle per-window geometry. No new config fields needed for secondary windows.

**Pin-thumbtack behavior:** unchanged semantically — toggles whether the window stays always-on-top when the app is focused. Only the rendering location moves (from the imgui-drawn title bar into our custom title bar).

## Modals

Error dialogs, benchmark progress, new-release notifications, discord opt-in, target-gen notification — the existing `uiShowModalDialog` machinery stays. Modals continue to use `imgui.OpenPopupStr` and center on the current main viewport (home viewport when disconnected, radar viewport when connected). No custom title bar on modals — their transient / OS-modal-ish feel is fine.

## Implementation scope

**Files expected to change:**

| File | Change |
|------|--------|
| `platform/glfw.go` (+ `platform.Platform` interface) | Add `ShowWindow()` / `HideWindow()` methods. |
| `cmd/vice/main.go` | Hide main GLFW window at startup; show/hide on scenario connect/disconnect. |
| `cmd/vice/dialogs.go` | Restructure `ScenarioSelectionModalClient` into a non-modal home-dialog render path; add "Launch Previous" button. |
| `cmd/vice/config.go` | Add fields to persist enough state for "Launch Previous"; add `HomeDialogPosition`. Remove `PopOutMessages`, `PopOutFlightStrips`, `MessagesOnRight`. |
| `cmd/vice/ui.go` | Introduce `drawWindowTitleBar` helper. Remove letterbox `dockPane` block. Apply the shared window-class + custom title bar to Messages, Flight Strips, Settings, Launch Control, Scenario Info. Radar close-X now hides radar + shows home. "New simulation" button follows same path. Remove integrated-pane settings controls. |
| `panes/messages.go` | Revert `dockedFlags` param; call `drawWindowTitleBar`. Remove inline `DrawPinButton`. |
| `panes/flightstrip.go` | Same as Messages. |

**Revert strategy:** the affected files are edited directly in this plan rather than `git revert`ing the integrated-panes commits. This avoids merge noise and keeps each new commit focused.

## Risks

1. **Hidden primary viewport** — see Architecture section. Verify early; fall back to iconify if needed.
2. **Custom drag of borderless viewports** — if `glfw.SetWindowPos` on a secondary viewport is overridden each frame by imgui's viewport position manager, route drag through `imgui.SetWindowPos` (imgui-space) instead. Measure during the title-bar implementation task; pick the path that sticks.
3. **"Launch Previous" robustness** — if the persisted prior selection can't be resolved (server unreachable, TRACON removed from data), the button is disabled with a tooltip. No silent fallback to the full picker; the user should see why the shortcut is unavailable.
4. **Settings window position on re-open** — the shared title bar's drag path must not cause the Settings window to jump between OS monitors on DPI changes. Use imgui-space dragging (which is DPI-aware) rather than raw OS pixel deltas.

## Testing

Repo has no automated UI tests. All verification is manual:

1. **Launch** → only the home dialog appears, custom title bar visible, no big black window behind it.
2. **Pick a scenario via the full picker** → home hides; radar window appears with scope + menu bar.
3. **Open Messages + Flight Strips** → each shows as its own OS-level window with a custom title bar, draggable, pin thumbtack functional, close (X) hides the window.
4. **Open Settings, Launch Control, Scenario Info** → each is its own OS window with custom title bar; pin thumbtack works; close (X) hides.
5. **Close radar (X)** → radar + all secondary windows vanish; home dialog reappears.
6. **Launch Previous Scenario** → connects immediately to the prior sim, skipping the picker.
7. **Close home dialog (X)** → app quits.
8. **Quit + relaunch** → "Launch Previous" populated with last-used scenario; home dialog position restored; radar size / position restored on scenario start; secondary-window positions restored via `ImGuiSettings`.
9. **Fullscreen the radar, exit, relaunch** → radar restores to windowed geometry (previous fullscreen fix still holds).
10. **"Start in full-screen" checkbox on home dialog** → next launch, after picking a scenario, the radar opens fullscreen.
11. **Scope-square mode still works** → after picking a scenario, toggle the mode in Settings; scope renders as a centered square; no letterbox-docked panes.
12. **Monitor move** → drag the home dialog across monitors; DPI changes don't make it disappear off-screen. Repeat for radar and Messages.

## Rejected alternatives

- **Approach 2 (two real GLFW windows):** home and radar as independently-created GLFW windows. Gives simultaneous coexistence we don't need; requires restructuring the renderer and platform layer; imgui multi-viewport fights a second independently-created primary.
- **Approach 3 (imgui viewports all the way, radar is render-to-texture):** cleanest conceptually but requires rewriting `panes.DrawPanes` to target a texture, plus mapping all input / mouse-coord / DPI math from texture space. Too much for a UX change.
- **Variant of Approach 1 where the radar and home coexist** (state-machine option C from brainstorming): user rejected in favor of strict sequential visibility.
