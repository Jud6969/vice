# Integrated STARS Panes Design

**Date:** 2026-04-17
**Branch:** `windowed-border`

## Goal

When the application is running in `WindowScaleMode` (STARS or ERAM), the
scope pane is centered as a square with black letterbox bars on either side.
Use those bars to host the Messages and Flight Strips panes inline instead
of as floating OS windows, and draw a 1 px white border around the scope
panel. Keep today's floating behavior available via per-pane "pop out"
settings.

## Scope

In scope:
- 1 px white border around the square STARS scope pane (square mode only).
- Dock Messages / Flight Strips into the left or right letterbox bar when
  scope-square mode is active and the pane is not popped out.
- Settings UI: per-pane pop-out checkboxes and a single mutually-exclusive
  side-layout radio (Messages-left/Strips-right vs. Messages-right/Strips-left).
- Persist settings on `Config`.

Out of scope:
- Native renderer path for docked panes (keep ImGui).
- Any layout that puts both panes on the same side (explicitly rejected —
  mutual exclusion in the UI prevents this by construction).
- Behavior changes when scope-square mode is off.

## Architecture

### Component 1 — STARS panel border
File: `panes/display.go`

Immediately after the block that shrinks `paneDisplayExtent` to a centered
square (currently around line 83–96), queue a 1 px white rectangle on the
outer edge of the square. Uses the existing `commandBuffer` and the renderer's
line-drawing primitives. Only runs inside the `if p.SquareScopePane()` branch,
so the border appears exclusively in scope-square mode.

### Component 2 — Docked pane layout
File: `cmd/vice/ui.go` (the call site that draws the Messages / Flight Strips
windows) plus the `applyPinWindowClass` helper.

When `config.MainWindowSquare` is true **and** the pane is not popped out:

- Compute `bar_width = (displayW - square_side) / 2` using the same math as
  `panes/display.go`.
- Resolve the pane's target side via `config.MessagesOnLeft` (flight strips
  go on the opposite side by construction).
- Before `imgui.BeginV`, call:
  - `imgui.SetNextWindowPos(pos)` with
    `pos = (0, menuBarHeight)` for left,
    `pos = (displayW - bar_width, menuBarHeight)` for right.
  - `imgui.SetNextWindowSize((bar_width, displayH - menuBarHeight))`.
- Pass window flags: `NoMove | NoResize | NoCollapse | NoTitleBar`.
- In `applyPinWindowClass`, when docked, clear `ViewportFlagsNoAutoMerge`
  so the window stays inside the main viewport instead of spawning an OS
  window.

When popped out (or when `MainWindowSquare` is false): skip all of the
above — fall through to today's code path (floating OS window,
`ViewportFlagsNoAutoMerge` set).

### Component 3 — Config fields and settings UI
File: `cmd/vice/config.go` for field definitions, `cmd/vice/ui.go` for the
settings window.

New fields on the existing config struct that holds `WindowScaleMode`:

```go
PopOutMessages     bool // default false
PopOutFlightStrips bool // default false
MessagesOnLeft     bool // default true
```

Zero-values produce the desired defaults (integrated, Messages-left/
Strips-right). No migration is required for existing configs.

Settings UI, rendered adjacent to the existing `WindowScaleMode` controls:

```
[ ] Pop out Messages
[ ] Pop out Flight Strips
Side layout:
  ( ) Messages left  / Flight Strips right
  ( ) Messages right / Flight Strips left
```

The side-layout radio writes to `MessagesOnLeft`. Flight Strips' side is
derived as `!MessagesOnLeft` — there is no independent flight-strip side
field. This makes same-side overlap impossible by construction.

The settings section may be shown unconditionally, but rows that have no
effect while scope-square mode is off can be dimmed (optional polish; not
required for correctness).

## Behavior Matrix

| Scope-square mode | Pop-out setting | Result                                       |
|-------------------|-----------------|----------------------------------------------|
| on                | off             | Docked in its chosen letterbox bar           |
| on                | on              | Floats as OS window (today's behavior)       |
| off               | either          | Floats as OS window (pop-out setting unused) |

## Error handling

- If `bar_width <= 0` (e.g., the window is exactly square and there is no
  letterbox), skip docking and fall back to floating. Prevents a
  zero-width ImGui window.
- All existing pane callbacks (`DrawWindow`, event processing, fonts,
  scrolling) are unchanged — only the window placement and class differ.

## Testing

Manual verification checklist:
1. Enable STARS scaling mode. Verify Messages docks left, Flight Strips
   docks right, and scope has a 1 px white border on all four sides.
2. Toggle "Pop out Messages" → Messages becomes a floating OS window again;
   left bar is empty.
3. Toggle side layout → Messages and Flight Strips swap bars.
4. Disable scaling mode → both panes float regardless of pop-out settings;
   scope border is gone (no square).
5. Resize main window while docked → panes resize with the bars.
6. Restart app → settings persist.

Automated tests are not added; the change is entirely layout/rendering
configuration and is best verified visually.

## Rejected alternatives

- **Native-renderer docked panes.** Duplicates every bit of message and
  flight-strip rendering logic (font, scrolling, interaction) for no user
  benefit.
- **Full pane-slot refactor.** Adding Messages/Strips as first-class panes
  in the pane-layout system is a sweeping refactor of `panes/display.go`
  that dwarfs this feature.
- **Independent per-pane side radios.** Would permit both panes on the
  same bar and require runtime overlap handling. Mutual-exclusion in
  the UI eliminates the problem by construction.
