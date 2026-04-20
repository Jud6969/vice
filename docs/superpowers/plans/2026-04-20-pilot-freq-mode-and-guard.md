# Frequency Management Mode & GUARD Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a cycleable "Realistic / Conventional" frequency-management toggle in Settings and a new `GUARD` command prefix that silently forces an aircraft onto a specified frequency, with typed and spoken variants.

**Architecture:** Two orthogonal features on `pilot-freq-handoff-realism`. A config bool (`RealisticFrequencyManagement`, default `false`) is propagated into `Sim.State` and read at three branch points: FC handler, `ContactIntent` render, and STT pattern registration. `GUARD` is a prefix detected in `runOneControlCommand` that pre-switches `ac.ControllerFrequency` and then recurses into the trailing command with `fromGuard=true` suppressing all pilot transmissions. STT adds a keyword-triggered pattern pass for guard phrasings.

**Tech Stack:** Go, cimgui-go (imgui-go v1), cgo bindings for sherpa-onnx STT. Tests via `go test`. Note: cgo-dependent tests may not build on this Windows machine due to environmental `collect2.exe` issue; use `go vet` + `go build` as fallback verification.

**Source spec:** `docs/superpowers/specs/2026-04-20-pilot-freq-mode-and-guard-design.md`

**Existing errors to reuse (don't redefine):** `sim/errors.go` already has `ErrInvalidFrequency` ("Frequency does not resolve to any controller") — use it where the spec says "ErrUnknownFrequency". `ErrNoMatchingFlight` serves as "unknown aircraft" (used at `sim/control.go:2339`).

---

## File Structure Overview

| File | Responsibility | Change type |
|---|---|---|
| `cmd/vice/config.go` | User config persistence | Add `RealisticFrequencyManagement bool` field |
| `sim/state.go` | Sim state propagation | Add `RealisticFrequencyManagement bool` field; plumbed from client on scenario start |
| `sim/errors.go` | Error constants | Add `ErrAlreadyOnFrequency` (reuse `ErrInvalidFrequency` and `ErrNoMatchingFlight`) |
| `sim/control.go` | Command dispatch, FC/TO handlers, guard mechanics | Add `fromGuard` param; add GUARD detection; add `Sim.Guard`; branch FC on mode |
| `aviation/intent.go` | Readback rendering | Mode-aware `SameFacility` forcing in `ContactIntent.Render` path (stamped at dispatch) |
| `stt/handlers.go` | STT pattern table | Add guard-pattern pre-pass; add conventional-mode pattern registration |
| `stt/typeparsers.go` | Shared token parsers | Add `acknowledgeWithIdentTail` parser |
| `cmd/vice/ui.go` | Settings UI | Add cycleable mode button in Speech to Text section |
| `sim/control_test.go` | Sim-side tests | New tests for mode branches + GUARD |
| `stt/parse_test.go` | STT tests | New tests for guard grammar |

---

## Task Ordering Rationale

Tasks 1–5 implement Feature A (mode toggle) bottom-up: config field → propagation → behavior branches → UI. Tasks 6–10 implement Feature B (GUARD) bottom-up: errors → `fromGuard` plumbing → `Sim.Guard` → dispatcher detection → STT grammar. Task 11 is end-to-end manual verification.

Each task ends with a commit. Commits are small and reversible.

---

## Task 1: Add `RealisticFrequencyManagement` config field

**Files:**
- Modify: `cmd/vice/config.go` (add field near `SelectedMicrophone`)

**Context:** `Config` is the per-user JSON config. Adding a bool field with zero value = Conventional (default per spec). Existing configs without the field unmarshal as `false` automatically.

- [ ] **Step 1: Add the config field**

Open `cmd/vice/config.go`. Find the line declaring `SelectedMicrophone string` (around line 84). Insert directly below:

```go
	RealisticFrequencyManagement bool
```

No default initializer needed — zero value is `false` = Conventional per spec.

- [ ] **Step 2: Verify the build**

Run: `go build ./cmd/vice/...`
Expected: Success (no references yet beyond the struct).

- [ ] **Step 3: Commit**

```bash
git add cmd/vice/config.go
git commit -m "config: add RealisticFrequencyManagement flag (default Conventional)"
```

---

## Task 2: Propagate flag into `Sim.State`

**Files:**
- Modify: `sim/state.go` (add `RealisticFrequencyManagement bool` field on `State`)
- Modify: `sim/manager.go` or equivalent scenario-setup path (find where `SimConfiguration` → `State` occurs)

**Context:** The sim layer needs its own read of the flag so FC/readback branches can consult it without reaching back into the client. The client already propagates settings like this — find the `NewSim` or equivalent and include the new field.

- [ ] **Step 1: Locate the propagation point**

Run: `grep -rn "FullScreenMonitor\|SelectedMicrophone" --include="*.go" sim/ server/ client/ cmd/vice/ | head -20`

Record which file writes `SimConfiguration` → `State`. The mode flag needs to hitch on the same mechanism. If none of those three fields cross the client→sim boundary (they may stay client-only), then the sim layer needs to read `s.cfg.RealisticFrequencyManagement` from the client config handle stored on `Sim` — check for a field like `s.clientConfig` or a config-passing constructor.

If both paths fail: add the bool directly to the scenario config struct (`SimConfiguration` or `LaunchConfig`) and copy it into `State.RealisticFrequencyManagement` at sim-start.

- [ ] **Step 2: Add field to `State` struct**

Open `sim/state.go`. Add below the existing fields:

```go
	// RealisticFrequencyManagement controls strictness of the FC handler,
	// readback style, and STT grammar. false (default) = Conventional mode.
	RealisticFrequencyManagement bool
```

- [ ] **Step 3: Write propagation code**

In the identified setup path, copy `config.RealisticFrequencyManagement` → `state.RealisticFrequencyManagement` at sim-start time. Example (actual placement depends on Step 1):

```go
state.RealisticFrequencyManagement = cfg.RealisticFrequencyManagement
```

- [ ] **Step 4: Verify build**

Run: `go build ./...`
Expected: Success.

- [ ] **Step 5: Commit**

```bash
git add sim/state.go sim/<setup-file>.go
git commit -m "sim: plumb RealisticFrequencyManagement into Sim.State"
```

---

## Task 3: Mode-aware readback via `ContactIntent.SameFacility`

**Files:**
- Modify: `sim/control.go:2349` (FrequencyChange — where `sameFacility` is computed)
- Modify: `sim/control.go:2300` area (ContactTower — same pattern)
- Test: `sim/control_test.go`

**Context:** Per spec §Feature A row "Readback style", Conventional forces `SameFacility = false` so the readback is always position+freq. In Realistic, the existing logic stands (same-facility → freq-only). The stamping happens at intent construction time in the sim. The client never sees the raw same-facility truth post-dispatch — perfect hook point.

Current code at `sim/control.go:2349`:

```go
sameFacility := !fromTypedCommand && fromCtrl != nil && fromCtrl.Facility == target.Facility
```

- [ ] **Step 1: Write failing test**

Add to `sim/control_test.go`:

```go
func TestFrequencyChange_ConventionalMode_ForcesPositionReadback(t *testing.T) {
	s := newTestSim(t) // helper that sets up a sim with two controllers same facility
	s.State.RealisticFrequencyManagement = false
	// aircraft on ControllerA's freq, target = ControllerB (same facility)
	intent, err := s.FrequencyChange(tcwA, callsignFromTest, freqB, "", false)
	require.NoError(t, err)
	ci, ok := intent.(av.ContactIntent)
	require.True(t, ok)
	require.False(t, ci.SameFacility, "Conventional must force SameFacility=false")
}

func TestFrequencyChange_RealisticMode_AllowsSameFacility(t *testing.T) {
	s := newTestSim(t)
	s.State.RealisticFrequencyManagement = true
	intent, err := s.FrequencyChange(tcwA, callsignFromTest, freqB, "", false)
	require.NoError(t, err)
	ci, ok := intent.(av.ContactIntent)
	require.True(t, ok)
	require.True(t, ci.SameFacility, "Realistic should set SameFacility=true for same-facility handoff")
}
```

If `newTestSim` doesn't exist, inspect `sim/control_test.go` for the existing sim-setup helper (likely `makeSim`, `newSim`, or inline setup) and use that pattern.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sim/ -run TestFrequencyChange_ -v`
Expected: FAIL (Conventional test: `SameFacility` is `true` because current code respects `fromCtrl.Facility == target.Facility`).

- [ ] **Step 3: Implement the mode branch**

In `sim/control.go` around line 2349, change:

```go
sameFacility := !fromTypedCommand && fromCtrl != nil && fromCtrl.Facility == target.Facility
```

to:

```go
sameFacility := s.State.RealisticFrequencyManagement &&
	!fromTypedCommand && fromCtrl != nil && fromCtrl.Facility == target.Facility
```

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./sim/ -run TestFrequencyChange_ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: force Conventional mode to position+freq readback"
```

---

## Task 4: Mode-aware FC handler (bare FC + unknown freq)

**Files:**
- Modify: `sim/control.go:4325–4357` (`case 'F':` branch)
- Modify: `sim/control.go:2342–2346` (FrequencyChange unknown-freq branch)
- Test: `sim/control_test.go`

**Context:** Per spec §Feature A:
- Conventional + bare `FC` (non-cleared) → `ContactTrackingController` (today's realistic behavior rejects with `ErrInvalidCommandSyntax` unless cleared).
- Conventional + unknown freq → silent route to tracking controller (today's realistic behavior emits `UnknownFrequencyIntent`).

Today's `case 'F':` at line 4326–4334:

```go
if command == "FC" {
    if ac, ok := s.Aircraft[callsign]; ok && ac.Nav.Approach.Cleared {
        return s.ContactTower(tcw, callsign, 0, "", true)
    }
    return s.ContactTrackingController(tcw, ACID(callsign))  // already permissive
}
```

Actually — reading more carefully, the current bare-`FC` path **does** fall back to `ContactTrackingController`. So the Realistic-mode "must have freq" behavior isn't wired yet! Verify by reading the current code at that line before writing the test. If the permissive fallback is in place today, then Task 4 for bare FC is a **new** Realistic-mode rejection path.

- [ ] **Step 1: Re-read current FC handler**

Open `sim/control.go` and read lines 4326–4356. Confirm:
- Bare `FC` (no digits): behavior under approach-cleared vs not.
- Unknown freq under `FC <digits>`: currently emits `UnknownFrequencyIntent` via `FrequencyChange`.

Match this observation to the spec §Feature A table. The Conventional behaviors described in the table should match the current code exactly (since Conventional = pre-branch behavior). Realistic is the new stricter path.

- [ ] **Step 2: Write failing tests**

Add to `sim/control_test.go`:

```go
func TestFC_Bare_Conventional_FallsBackToTrackingController(t *testing.T) {
	s := newTestSim(t)
	s.State.RealisticFrequencyManagement = false
	intent, err := s.runOneControlCommand(tcwA, callsignFromTest, "FC", 0, false)
	require.NoError(t, err)
	require.NotNil(t, intent, "Conventional bare FC should produce a ContactIntent")
}

func TestFC_Bare_Realistic_Rejects(t *testing.T) {
	s := newTestSim(t)
	s.State.RealisticFrequencyManagement = true
	// Aircraft not cleared for approach:
	_, err := s.runOneControlCommand(tcwA, callsignFromTest, "FC", 0, false)
	require.ErrorIs(t, err, ErrInvalidCommandSyntax, "Realistic bare FC on non-cleared aircraft must reject")
}

func TestFC_UnknownFreq_Conventional_RoutesToTrackingController(t *testing.T) {
	s := newTestSim(t)
	s.State.RealisticFrequencyManagement = false
	// freq 135000 is not in scenario
	intent, err := s.runOneControlCommand(tcwA, callsignFromTest, "FC135000", 0, false)
	require.NoError(t, err)
	_, isContact := intent.(av.ContactIntent)
	require.True(t, isContact, "Conventional unknown freq should produce ContactIntent, not UnknownFrequencyIntent")
}

func TestFC_UnknownFreq_Realistic_UnknownFrequencyIntent(t *testing.T) {
	s := newTestSim(t)
	s.State.RealisticFrequencyManagement = true
	intent, err := s.runOneControlCommand(tcwA, callsignFromTest, "FC135000", 0, false)
	require.NoError(t, err)
	_, isUnknown := intent.(av.UnknownFrequencyIntent)
	require.True(t, isUnknown, "Realistic unknown freq must produce UnknownFrequencyIntent")
}
```

Note: `runOneControlCommand` is unexported — the test file must be in `package sim`. Confirm with: `head -1 sim/control_test.go`.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./sim/ -run TestFC_ -v`
Expected: Bare+Realistic test FAILS (no rejection path yet); UnknownFreq+Conventional test FAILS (currently emits `UnknownFrequencyIntent` always).

- [ ] **Step 4: Add bare-FC Realistic rejection**

In `sim/control.go`, modify the `if command == "FC"` block:

```go
if command == "FC" {
    if ac, ok := s.Aircraft[callsign]; ok && ac.Nav.Approach.Cleared {
        return s.ContactTower(tcw, callsign, 0, "", true)
    }
    if s.State.RealisticFrequencyManagement {
        return nil, ErrInvalidCommandSyntax
    }
    return s.ContactTrackingController(tcw, ACID(callsign))
}
```

- [ ] **Step 5: Add unknown-freq Conventional fallback in `FrequencyChange`**

In `sim/control.go:2342`, modify:

```go
target, err := s.resolveControllerByFrequency(ac, freq, positionHint)
if err != nil {
    if !s.State.RealisticFrequencyManagement {
        // Conventional: silently route to tracking controller.
        return s.ContactTrackingController(tcw, ACID(callsign))
    }
    s.enqueueUnknownFrequencyCallback(callsign, TCP(ac.ControllerFrequency), freq)
    return av.UnknownFrequencyIntent{Frequency: freq}, nil
}
```

- [ ] **Step 6: Run tests to verify pass**

Run: `go test ./sim/ -run TestFC_ -v`
Expected: PASS (all four).

- [ ] **Step 7: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: gate strict FC behavior behind Realistic mode"
```

---

## Task 5: Mode-aware STT pattern registration

**Files:**
- Modify: `stt/handlers.go` (pattern table around line 1260)
- Test: `stt/parse_test.go`

**Context:** Per spec §Feature A, Conventional mode needs the legacy patterns back. Today the grammar only has the strict `contact {position} {frequency}` form. In Conventional the grammar must also accept freq-less forms like `contact approach` and route them to bare `FC` (which Conventional itself permits).

The STT layer has access to the client config (it runs in the client process) — it can read `config.RealisticFrequencyManagement` directly. If the STT pattern table is static and doesn't currently branch on any config, add a config parameter to the pattern-registration function and invoke it per-session.

- [ ] **Step 1: Locate the STT pattern table**

Open `stt/handlers.go`. Find the function that registers patterns (likely `init`, `registerPatterns`, or a `var patterns = ...` declaration). Confirm how it's consumed — does the STT layer have a `*Config` handle?

Run: `grep -n "contact approach\|contact departure\|priority" stt/handlers.go | head -20`

- [ ] **Step 2: Write failing test**

Add to `stt/parse_test.go`:

```go
func TestContactApproach_Conventional_ParsesToBareFC(t *testing.T) {
	cmd := parseTranscription(t, "contact approach", conventionalCfg())
	require.Equal(t, "FC", cmd)
}

func TestContactApproach_Realistic_NoMatch(t *testing.T) {
	cmd := parseTranscription(t, "contact approach", realisticCfg())
	require.Empty(t, cmd, "Realistic should not match bare 'contact approach' without frequency")
}
```

Helpers `parseTranscription`, `conventionalCfg`, `realisticCfg` need to exist; add them to the test file if not present. Inspect `stt/parse_test.go` for an existing transcription-test harness and follow its pattern.

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./stt/ -run TestContactApproach_ -v`
Expected: FAIL (no mode-aware dispatch yet).

- [ ] **Step 4: Add Conventional-mode patterns**

In `stt/handlers.go`, register these patterns only when `cfg.RealisticFrequencyManagement == false`:

```go
if !cfg.RealisticFrequencyManagement {
    // Conventional: permit legacy freq-less forms.
    patterns = append(patterns, Pattern{
        Priority: 3,
        Tokens:   []Token{literal("contact"), positionText()},
        Emit:     func(m Match) string { return "FC" },
    })
    patterns = append(patterns, Pattern{
        Priority: 3,
        Tokens:   []Token{literal("over"), literal("to"), positionText()},
        Emit:     func(m Match) string { return "FC" },
    })
}
```

(The exact type structure depends on `stt/handlers.go` — inspect current patterns and copy their shape. The principle: freq-less `contact {position}` patterns exist in Conventional, don't exist in Realistic.)

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./stt/ -run TestContactApproach_ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stt/handlers.go stt/parse_test.go
git commit -m "stt: restore freq-less contact patterns in Conventional mode"
```

---

## Task 6: Settings UI cycleable mode button

**Files:**
- Modify: `cmd/vice/ui.go:895–1080` (Speech to Text collapsing header)

**Context:** Per spec §Settings UI, a single `imgui.Button` that flips `config.RealisticFrequencyManagement`. Insert between the microphone selector (`ends at line ~952`) and the Test PTT button (`starts ~line 955`).

- [ ] **Step 1: Locate the insertion point**

Open `cmd/vice/ui.go`. Confirm lines 940–952 render the microphone combo, ending with `imgui.EndCombo()` followed by a blank line.

- [ ] **Step 2: Add the mode button**

After `imgui.EndCombo()` at line ~952 (before the Test PTT section), insert:

```go
		// Frequency Management mode — cycleable button.
		imgui.Text("Mode:")
		imgui.SameLine()
		modeLabel := "Conventional###freqMode"
		if config.RealisticFrequencyManagement {
			modeLabel = "Realistic###freqMode"
		}
		if imgui.Button(modeLabel) {
			config.RealisticFrequencyManagement = !config.RealisticFrequencyManagement
		}
		if imgui.IsItemHovered() {
			imgui.SetTooltip("Click to toggle.\n" +
				"Conventional: bare FC works; unknown frequencies still route; readback always full position+freq.\n" +
				"Realistic: frequencies required and strict; readback varies by facility.")
		}
```

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/vice/...`
Expected: Success.

- [ ] **Step 4: Manual verification**

Launch vice, open Settings → Speech to Text. Verify:
- Button labeled "Conventional" appears between microphone and Test PTT.
- Clicking flips label to "Realistic" and back.
- Tooltip displays on hover.
- Row doesn't reflow when the label changes.

Note: If cgo build fails on this machine (known collect2 issue), stop at Step 3 and verify with `go vet ./cmd/vice/...` instead.

- [ ] **Step 5: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "ui: cycleable Realistic/Conventional frequency mode button"
```

---

## Task 7: Add `ErrAlreadyOnFrequency` error

**Files:**
- Modify: `sim/errors.go`

- [ ] **Step 1: Add the error constant**

In `sim/errors.go`, add to the `var (...)` block (alphabetically between `ErrAircraftAlreadyReleased` and `ErrAmbiguousTower`):

```go
	ErrAlreadyOnFrequency              = errors.New("Aircraft is already on that frequency")
```

- [ ] **Step 2: Verify build**

Run: `go build ./sim/...`
Expected: Success.

- [ ] **Step 3: Commit**

```bash
git add sim/errors.go
git commit -m "sim: add ErrAlreadyOnFrequency for GUARD precondition"
```

---

## Task 8: Plumb `fromGuard` through `runOneControlCommand`

**Files:**
- Modify: `sim/control.go:4082` (`runOneControlCommand` signature)
- Modify: every call site of `runOneControlCommand`
- Modify: `sim/control.go` intent-emitting helpers that call `enqueuePilotTransmission` (filter by fromGuard)

**Context:** Per spec §Feature B, `fromGuard` suppresses **all** pilot transmissions while still applying state changes. The flag rides alongside `delayReduction` on the dispatch context.

- [ ] **Step 1: Change `runOneControlCommand` signature**

At `sim/control.go:4082`, change:

```go
func (s *Sim) runOneControlCommand(tcw TCW, callsign av.ADSBCallsign, command string, delayReduction time.Duration) (av.CommandIntent, error) {
```

to:

```go
func (s *Sim) runOneControlCommand(tcw TCW, callsign av.ADSBCallsign, command string, delayReduction time.Duration, fromGuard bool) (av.CommandIntent, error) {
```

- [ ] **Step 2: Update all call sites**

Run: `grep -n "runOneControlCommand(" sim/*.go`

For each call, add `false` as the new final argument. Typical caller patterns:
- Internal batch dispatchers in `sim/control.go` — pass `false` (they're not guard-driven).
- The future `Sim.Guard` will pass `true` — that's Task 10.

Example edit:

```go
// before
result, err := s.runOneControlCommand(tcw, callsign, cmd, delayReduction)
// after
result, err := s.runOneControlCommand(tcw, callsign, cmd, delayReduction, false)
```

- [ ] **Step 3: Verify build**

Run: `go build ./sim/...`
Expected: Success. If a caller is missed, the compiler will flag it.

- [ ] **Step 4: Suppress `enqueuePilotTransmission` when fromGuard**

Problem: `enqueuePilotTransmission` is called from many paths (`sim/control.go:2565`, `2626`, `2846`, etc. and `sim/sim.go:1261`, `1471`, etc.). We can't plumb `fromGuard` into all of them.

Cleanest approach: wrap the call in a helper on `Sim` that inspects a transient flag set during guard dispatch. Add:

```go
// sim/control.go (near other Sim methods)
func (s *Sim) maybeEnqueuePilotTransmission(callsign av.ADSBCallsign, tcp TCP, tx PendingTransmission) {
    if s.suppressPilotTx {
        return
    }
    s.enqueuePilotTransmission(callsign, tcp, tx)
}
```

Add `suppressPilotTx bool` field on `Sim` (next to `mu`). Don't replace all existing `enqueuePilotTransmission` calls — only the ones reachable from commands that guard might wrap. In practice, every intent-emitting command path goes through `dispatchControlledAircraftCommand` or similar helpers — the `enqueuePilotTransmission` calls downstream of those need the wrapper.

Simpler alternative (preferred): set `s.suppressPilotTx = true` / `false` around the recursive `runOneControlCommand` call in `Sim.Guard` (Task 10), and patch `enqueuePilotTransmission` itself to return early when the flag is set:

```go
// At the top of enqueuePilotTransmission (find current signature):
func (s *Sim) enqueuePilotTransmission(callsign av.ADSBCallsign, tcp TCP, tx PendingTransmission) {
    if s.suppressPilotTx {
        return
    }
    // ... existing body
}
```

Add the field:

```go
// near the top of Sim struct definition
suppressPilotTx bool
```

This centralizes suppression in one file with one branch.

- [ ] **Step 5: Verify build**

Run: `go build ./sim/...`
Expected: Success.

- [ ] **Step 6: Commit**

```bash
git add sim/control.go sim/sim.go
git commit -m "sim: plumb fromGuard flag and add pilot-tx suppression"
```

---

## Task 9: Suppress readback rendering when fromGuard

**Files:**
- Modify: `sim/control.go` (the intent-returning paths triggered by GUARD)

**Context:** `enqueuePilotTransmission` suppression (Task 8) handles verbal readbacks generated out-of-band. But command handlers also **return** intents that the caller (dispatcher or STARS UI) may render into the `MessagesPane`. When `fromGuard=true`, the guard path must return `nil` as the intent so the top-level dispatcher doesn't push it into the transmission feed.

Simplest implementation: `Sim.Guard` filters the intent returned by recursive `runOneControlCommand` — keep nil, discard non-nil. This way `fromGuard` doesn't need to thread deeper than the Guard method itself.

- [ ] **Step 1: Verify the filter strategy**

Read `sim/control.go` dispatchers that call `runOneControlCommand`. Find where returned intents get turned into pilot transmissions (likely via a `RadioTransmission` batch). If the intent return is consumed only at the top level, filtering in Guard is sufficient — no deeper plumbing needed.

- [ ] **Step 2: Note the design decision**

Add a comment in `Sim.Guard` (to be written in Task 10) that explicitly drops the returned intent and relies on `suppressPilotTx` for side-channel transmissions.

- [ ] **Step 3: No code change in this task**

This task is a design checkpoint. The actual filtering is implemented in Task 10. Skip to Task 10.

---

## Task 10: Implement `Sim.Guard`

**Files:**
- Modify: `sim/control.go` (add `Sim.Guard` and `resolveGuardTarget`)
- Test: `sim/control_test.go`

- [ ] **Step 1: Write failing tests**

Add to `sim/control_test.go`:

```go
func TestGuard_Bare_SwitchesToUserFrequency(t *testing.T) {
	s := newTestSim(t)
	ac := s.Aircraft[callsignFromTest]
	origFreq := ac.ControllerFrequency
	require.NotEqual(t, origFreq, ControlPosition(s.State.Controllers[tcwA].Callsign),
		"precondition: aircraft must start off user's freq")

	intent, err := s.Guard(tcwA, callsignFromTest, "")
	require.NoError(t, err)
	require.Nil(t, intent, "bare GUARD should produce no intent")
	require.Equal(t, ControlPosition(s.State.Controllers[tcwA].Callsign), s.Aircraft[callsignFromTest].ControllerFrequency)
}

func TestGuard_AlreadyOnFrequency_Rejects(t *testing.T) {
	s := newTestSim(t)
	s.Aircraft[callsignFromTest].ControllerFrequency = ControlPosition(s.State.Controllers[tcwA].Callsign)
	_, err := s.Guard(tcwA, callsignFromTest, "")
	require.ErrorIs(t, err, ErrAlreadyOnFrequency)
}

func TestGuard_UnknownAircraft_Rejects(t *testing.T) {
	s := newTestSim(t)
	_, err := s.Guard(tcwA, av.ADSBCallsign("ZZZ999"), "")
	require.ErrorIs(t, err, ErrNoMatchingFlight)
}

func TestGuard_Redirect_SwitchesToTargetController(t *testing.T) {
	s := newTestSim(t)
	// ControllerB exists at freqB in same facility
	intent, err := s.Guard(tcwA, callsignFromTest, "FC"+freqBDigits)
	require.NoError(t, err)
	require.Nil(t, intent, "GUARD FC <freq> should emit no pilot transmission")
	require.Equal(t, ControlPosition("CONTROLLER_B"), s.Aircraft[callsignFromTest].ControllerFrequency)
}

func TestGuard_SuppressesPilotTransmissions(t *testing.T) {
	s := newTestSim(t)
	before := len(s.pendingPilotTransmissions) // inspect internal queue
	_, err := s.Guard(tcwA, callsignFromTest, "ID")
	require.NoError(t, err)
	require.Equal(t, before, len(s.pendingPilotTransmissions), "GUARD ID must not enqueue verbal readback")
}
```

Adjust field names to match actual `Sim` struct — check current `Sim` definition for the transmission queue's field name.

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sim/ -run TestGuard_ -v`
Expected: FAIL (`Sim.Guard` doesn't exist yet).

- [ ] **Step 3: Implement `resolveGuardTarget`**

Add to `sim/control.go` (near `resolveControllerByFrequency`):

```go
// resolveGuardTarget returns the controller an aircraft should be switched to
// by a GUARD dispatch. Examines the trailing command tokens:
//   - Leading "FC<digits>" → parse freq, resolve via resolveControllerByFrequency.
//     * On match, return that controller.
//     * On no match, Realistic → ErrInvalidFrequency.
//     * On no match, Conventional → return the aircraft's current tracking controller.
//   - Otherwise → return the user's own controller (via tcw).
func (s *Sim) resolveGuardTarget(tcw TCW, ac *Aircraft, trailing string) (*av.Controller, error) {
    trimmed := strings.TrimSpace(trailing)
    if strings.HasPrefix(trimmed, "FC") && len(trimmed) > 2 {
        // Take just the FC<digits> token; split on first whitespace.
        fields := strings.Fields(trimmed)
        first := fields[0]
        rest := first[2:]
        if idx := strings.Index(rest, ":"); idx >= 0 {
            rest = rest[:idx]
        }
        freq, err := parseFrequencyDigits(rest)
        if err == nil {
            target, rerr := s.resolveControllerByFrequency(ac, freq, "")
            if rerr == nil {
                return target, nil
            }
            if s.State.RealisticFrequencyManagement {
                return nil, ErrInvalidFrequency
            }
            // Conventional: fall through to tracking controller.
            if tc, ok := s.State.Controllers[TCP(ac.ControllerFrequency)]; ok {
                return tc, nil
            }
            return nil, ErrInvalidFrequency
        }
    }
    // Bare GUARD or non-FC trailing → user's own frequency.
    if myCtrl, ok := s.State.Controllers[TCP(tcw)]; ok {
        return myCtrl, nil
    }
    return nil, ErrInvalidFrequency
}
```

- [ ] **Step 4: Implement `Sim.Guard`**

Add to `sim/control.go` (near `FrequencyChange`):

```go
// Guard dispatches a command via guard-broadcast semantics: the aircraft is
// forced onto a specified frequency regardless of its current listening state,
// then the trailing command (if any) is executed. No pilot transmissions are
// emitted for the duration of the guard dispatch; the aircraft switches and
// acts silently. Returns nil intent unconditionally — side effects only.
func (s *Sim) Guard(tcw TCW, callsign av.ADSBCallsign, trailing string) (av.CommandIntent, error) {
    s.mu.Lock(s.lg)
    defer s.mu.Unlock(s.lg)

    ac, ok := s.Aircraft[callsign]
    if !ok {
        return nil, ErrNoMatchingFlight
    }

    target, err := s.resolveGuardTarget(tcw, ac, trailing)
    if err != nil {
        return nil, err
    }
    targetPos := ControlPosition(target.Callsign)
    if ac.ControllerFrequency == targetPos {
        return nil, ErrAlreadyOnFrequency
    }
    ac.ControllerFrequency = targetPos

    if strings.TrimSpace(trailing) == "" {
        return nil, nil
    }

    // Suppress all pilot transmissions for the bundled command.
    s.suppressPilotTx = true
    defer func() { s.suppressPilotTx = false }()

    // Drop the returned intent — guard doesn't produce a verbal readback.
    _, rerr := s.runOneControlCommand(tcw, callsign, trailing, 0, true)
    return nil, rerr
}
```

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./sim/ -run TestGuard_ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: add Sim.Guard with silent freq switch and trailing dispatch"
```

---

## Task 11: Detect `GUARD` prefix in `runOneControlCommand`

**Files:**
- Modify: `sim/control.go:4082–4090` (entry of `runOneControlCommand`)
- Test: `sim/control_test.go`

**Context:** Typed commands arrive at `runOneControlCommand` as a single string. The STARS TG mode input `SWA123 GUARD FC 128375` arrives here as `callsign="SWA123", command="GUARD FC128375"` (after `mergeFrequencyArgs` joins `FC` and the freq digits). We need to detect the leading `GUARD` token, strip it, and route to `Sim.Guard` with the remainder.

- [ ] **Step 1: Write failing test**

Add to `sim/control_test.go`:

```go
func TestRunOneControlCommand_GuardPrefix_RoutesToGuard(t *testing.T) {
	s := newTestSim(t)
	origFreq := s.Aircraft[callsignFromTest].ControllerFrequency
	_, err := s.runOneControlCommand(tcwA, callsignFromTest, "GUARD", 0, false)
	require.NoError(t, err)
	require.NotEqual(t, origFreq, s.Aircraft[callsignFromTest].ControllerFrequency,
		"GUARD should change frequency")
}

func TestRunOneControlCommand_GuardWithTrailing_DispatchesTrailing(t *testing.T) {
	s := newTestSim(t)
	// ID is the "ident" trailing command
	_, err := s.runOneControlCommand(tcwA, callsignFromTest, "GUARD ID", 0, false)
	require.NoError(t, err)
	require.Equal(t, av.TransponderModeIdent, s.Aircraft[callsignFromTest].Transponder.Mode)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./sim/ -run TestRunOneControlCommand_Guard -v`
Expected: FAIL — `GUARD` is not recognized and routes to `case 'G':` which doesn't handle it.

- [ ] **Step 3: Add GUARD detection at top of `runOneControlCommand`**

In `sim/control.go:4083` (just after the length check), insert:

```go
    // GUARD prefix: deliver the trailing command (or bare freq switch) via
    // guard-broadcast semantics. Must be detected before the single-char
    // dispatch switch so it doesn't collide with 'G' (GA/GR*).
    const guardTok = "GUARD"
    if command == guardTok {
        return s.Guard(tcw, callsign, "")
    }
    if strings.HasPrefix(command, guardTok+" ") {
        return s.Guard(tcw, callsign, command[len(guardTok)+1:])
    }
```

Note: `fromGuard` parameter is already the 5th arg from Task 8. It defaults to `false` at this call-site; `Sim.Guard` flips the flag for the recursive call internally.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./sim/ -run TestRunOneControlCommand_Guard -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add sim/control.go sim/control_test.go
git commit -m "sim: detect GUARD prefix in command dispatcher"
```

---

## Task 12: STT guard keyword detection and pattern pass

**Files:**
- Modify: `stt/handlers.go` (add guard-keyword pre-pass and four patterns)
- Test: `stt/parse_test.go`

**Context:** Per spec §STT grammar, if the raw transcription contains the keyword `guard` (case-insensitive, word-boundary), run a dedicated guard-pattern pass with priorities 38–40 (beats all normal patterns). Four patterns emit `<callsign> GUARD FC <digits>` or `<callsign> GUARD FC <digits>:<hint>`.

- [ ] **Step 1: Locate the STT entry point**

Open `stt/handlers.go`. Find the function that processes a transcription into commands (likely `Parse`, `ProcessTranscription`, or similar). Confirm its signature and how it loops over patterns.

Run: `grep -n "^func.*Pars\|^func.*Process\|^func.*Match" stt/handlers.go | head -10`

- [ ] **Step 2: Write failing tests**

Add to `stt/parse_test.go`:

```go
func TestGuard_ContactMeImmediately(t *testing.T) {
	cmd := parseTranscription(t,
		"attention all aircraft this is orlando approach on guard southwest one two three contact me immediately on one two eight point three seven five",
		realisticCfg())
	require.Equal(t, "SWA123 GUARD FC128375", cmd)
}

func TestGuard_SwitchToMyFrequency(t *testing.T) {
	cmd := parseTranscription(t,
		"this is orlando approach on guard southwest one two three switch to my frequency one two eight point three seven five",
		realisticCfg())
	require.Equal(t, "SWA123 GUARD FC128375", cmd)
}

func TestGuard_Redirect(t *testing.T) {
	cmd := parseTranscription(t,
		"this is orlando approach on guard southwest one two three contact orlando approach on one three four point zero five",
		realisticCfg())
	require.Equal(t, "SWA123 GUARD FC134050:orlando approach", cmd)
}

func TestGuard_WithoutKeyword_NotGuard(t *testing.T) {
	cmd := parseTranscription(t,
		"southwest one two three switch to my frequency one two eight point three seven five",
		realisticCfg())
	// Without "guard" keyword, this should NOT be a GUARD — falls to normal FC.
	require.NotContains(t, cmd, "GUARD")
}
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./stt/ -run TestGuard_ -v`
Expected: FAIL (no guard patterns yet).

- [ ] **Step 4: Implement keyword detection and guard patterns**

Near the top of the parse function, add:

```go
// Guard keyword detection — case-insensitive word-boundary match.
hasGuardKeyword := regexp.MustCompile(`(?i)\bguard\b`).MatchString(transcription)
```

In the pattern registration, add the four guard patterns conditioned on `hasGuardKeyword`:

```go
if hasGuardKeyword {
    guardPatterns := []Pattern{
        {
            Priority: 40,
            Tokens:   []Token{callsign(), literal("contact"), literal("me"), literal("immediately"), literal("on"), frequency()},
            Emit:     func(m Match) string { return m.Callsign + " GUARD FC" + m.FrequencyDigits },
        },
        {
            Priority: 40,
            Tokens:   []Token{callsign(), literal("switch"), literal("to"), literal("my"), literal("frequency"), frequency()},
            Emit:     func(m Match) string { return m.Callsign + " GUARD FC" + m.FrequencyDigits },
        },
        {
            Priority: 39,
            Tokens:   []Token{callsign(), literal("contact"), positionText(), literal("on"), frequency()},
            Emit:     func(m Match) string { return m.Callsign + " GUARD FC" + m.FrequencyDigits + ":" + m.PositionText },
        },
        {
            Priority: 38,
            Tokens:   []Token{callsign(), literal("contact"), positionText(), frequency()},
            Emit:     func(m Match) string { return m.Callsign + " GUARD FC" + m.FrequencyDigits + ":" + m.PositionText },
        },
    }
    patterns = append(patterns, guardPatterns...)
}
```

Adjust `Token` / `Pattern` / `Match` type names to match actual `stt/handlers.go` code. If the existing pattern table uses a different structure (regex-based, state-machine-based, etc.), adapt shape while preserving priority ordering.

- [ ] **Step 5: Run tests to verify pass**

Run: `go test ./stt/ -run TestGuard_ -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add stt/handlers.go stt/parse_test.go
git commit -m "stt: guard keyword detection with four guard patterns"
```

---

## Task 13: STT "acknowledge with IDENT" tail parser

**Files:**
- Modify: `stt/handlers.go` or `stt/typeparsers.go` (shared helper)
- Test: `stt/parse_test.go`

**Context:** Per spec, any guard pattern can have an optional trailing `acknowledge with ident` clause. When present, append ` ID` to the emitted command.

- [ ] **Step 1: Write failing test**

Add to `stt/parse_test.go`:

```go
func TestGuard_AcknowledgeWithIdentTail(t *testing.T) {
	cmd := parseTranscription(t,
		"this is orlando approach on guard southwest one two three switch to my frequency one two eight point three seven five acknowledge with ident",
		realisticCfg())
	require.Equal(t, "SWA123 GUARD FC128375 ID", cmd)
}

func TestGuard_NoIdentTail_NoID(t *testing.T) {
	cmd := parseTranscription(t,
		"this is orlando approach on guard southwest one two three switch to my frequency one two eight point three seven five",
		realisticCfg())
	require.Equal(t, "SWA123 GUARD FC128375", cmd)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./stt/ -run TestGuard_AcknowledgeWith -v`
Expected: FAIL (no tail parser yet).

- [ ] **Step 3: Implement the tail parser**

In `stt/handlers.go`, add a post-match step for guard patterns:

```go
// Inside the parse function, after a guard pattern matches:
hasIdentTail := regexp.MustCompile(`(?i)\backnowledge with ident\b`).MatchString(transcription)
if hasIdentTail {
    emittedCmd += " ID"
}
```

Apply this only in the guard branch — normal (non-guard) patterns don't accept the tail.

- [ ] **Step 4: Run tests to verify pass**

Run: `go test ./stt/ -run TestGuard_AcknowledgeWith -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add stt/handlers.go stt/parse_test.go
git commit -m "stt: append ID to guard commands when 'acknowledge with ident' present"
```

---

## Task 14: Manual end-to-end verification

**Files:**
- No code changes; verification only.

**Context:** Unit tests cover sim-side logic and STT grammar in isolation. This task exercises the integrated path through the UI, spoken transmission, and aircraft state in a running sim.

- [ ] **Step 1: Build and launch**

Run: `go build -o vice.exe ./cmd/vice/`
Then launch: `./vice.exe`

If cgo build fails (known collect2 issue on this machine), note the failure in the task summary and stop at Step 0. The user can verify on a working build.

- [ ] **Step 2: Verify Settings toggle**

- Open Settings → Speech to Text
- Confirm a button labeled "Conventional" appears between microphone selector and Test PTT
- Click it — label flips to "Realistic"
- Hover — tooltip describes both modes
- Row does not reflow when the label changes

- [ ] **Step 3: Verify Conventional mode forgiveness**

- Set mode to "Conventional"
- Connect to any scenario
- Type `FC` (bare) against a non-cleared aircraft
- Verify aircraft hands off to tracking controller; readback is "contact {position} on {freq}, good day"

- [ ] **Step 4: Verify Realistic mode strictness**

- Set mode to "Realistic"
- Type `FC` (bare) against the same non-cleared aircraft
- Verify command is rejected with `ErrInvalidCommandSyntax`
- Type `FC 135000` (freq not in scenario)
- Verify aircraft says "say again the frequency?"

- [ ] **Step 5: Verify GUARD typed forms**

- Pick an aircraft NOT on your frequency (e.g., just handed off)
- Type `TG <callsign> GUARD` — aircraft silently switches to your frequency, no readback
- Type `TG <callsign> GUARD ID` against an aircraft not on your freq — aircraft silently switches AND IDENTs (datablock shows IDENT), no verbal readback
- Type `TG <callsign> GUARD` against an aircraft already on your freq — see "Aircraft is already on that frequency" error
- Type `TG <callsign> GUARD FC <other-controller-freq>` — aircraft silently transfers to that controller

- [ ] **Step 6: Verify GUARD spoken forms**

- With STT enabled, hold PTT and say:
  - "Attention all aircraft this is orlando approach on guard, Southwest one two three contact me immediately on one two eight point three seven five"
  - Verify SWA123 silently switches to 128.375
- Say: "...switch to my frequency one two eight point three seven five acknowledge with ident"
  - Verify SWA123 silently switches AND IDENTs
- Say (without "guard"): "Southwest one two three switch to my frequency one two eight point three seven five"
  - Verify normal FC path runs (pilot reads back)

- [ ] **Step 7: Record any regressions**

Log any issues in a new commit message body (no code change). If everything works, commit an empty marker or skip this step.

---

## Spec Coverage Checklist

Each requirement in the spec maps to a task:

| Spec section | Task(s) |
|---|---|
| §Motivation | Context for all tasks |
| §Features (A mode, B GUARD) | Tasks 1–6 (A), Tasks 7–13 (B) |
| §Architecture data-flow | Tasks 10–13 |
| §Feature A table (bare FC, unknown freq, readback) | Task 4 (FC, unknown), Task 3 (readback) |
| §Feature A mode-change UX | Task 6 (button) + Task 4 (re-read per dispatch) |
| §Feature B typed forms table | Tasks 10–11 |
| §Feature B preconditions | Task 10 |
| §Feature B dispatch code sample | Task 10 |
| §`fromGuard` flag | Task 8 (plumb + suppression) |
| §STT detection trigger | Task 12 |
| §STT guard pattern table (40/40/39/38) | Task 12 |
| §STT callsign extraction / frequency parser | Reuses existing (no task needed) |
| §STT keyword/callsign mismatch | Task 12 (tests cover no-callsign case) |
| §Settings UI | Task 6 |
| §Data model and touch points | All tasks |
| §Error surface summary | Tasks 7, 10 (errors + reject paths) |
| §Testing (unit) | Embedded in each task |
| §Testing (integration) | Task 14 |
| §Out of scope | No tasks needed |

---

## Notes on Test Harness

- `sim/control_test.go` tests run in `package sim` (internal tests) so they can reach unexported functions like `runOneControlCommand`.
- `sim/export_test.go` is the established pattern for exporting sim internals to tests — check it before writing a `newTestSim` helper; there may already be one.
- `stt/parse_test.go` structure: examine the existing tests for the harness shape and reuse.
- If cgo build fails (platform-specific): all `go test ./sim/...` runs may still work because sim does not import the STT cgo layer; `go test ./stt/...` likely needs cgo. Fall back to `go vet ./...` for STT changes and rely on the sim-side integration tests.
