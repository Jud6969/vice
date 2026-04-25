# Voice Switch Pane Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `VoiceSwitchPane` to vice — a toggleable per-client window listing tuned frequencies with RX/TX checkboxes, that gates message reception and command transmission. Includes a built-in 121.500 guard row whose TX gates `GUARD` commands.

**Architecture:** Client-only. New file `panes/voiceswitch.go` mirrors `FlightStripPane` lifecycle (Activate/ResetSim/DrawWindow/DrawUI). Reconciles each frame against `c.State.GetPositionsForTCW(c.State.UserTCW)`. RX gate replaces the existing `UserControlsPosition` check in `panes/messages.go`. TX gate is a single chokepoint added at the top of `client.RunAircraftCommands` via a new `CanTransmit func(string) bool` field on `ControlClient`, wired by `cmd/vice` to call into the voice switch pane. No sim/server changes.

**Tech Stack:** Go, imgui via `cimgui-go`, existing vice packages (`av`, `sim`, `client`, `panes`, `cmd/vice`).

**Spec:** `docs/superpowers/specs/2026-04-25-voice-switch-design.md`.

---

## File Structure

| Path | Status | Responsibility |
|---|---|---|
| `panes/voiceswitch.go` | new | `VoiceSwitchPane` type + all behavior (lifecycle, reconcile, helpers, rendering). |
| `panes/voiceswitch_test.go` | new | Unit tests for reconcile/seed, IsRX, TX helpers, AllowsCommand, manual add/remove. |
| `panes/messages.go` | modify (~line 166) | Replace `UserControlsPosition` call with `voiceSwitch.IsRX(...)`; thread `*VoiceSwitchPane` through `DrawWindow`. |
| `client/control.go` | modify (~line 435) | Add `CanTransmit func(string) bool` field on `ControlClient`; gate at top of `RunAircraftCommands`. |
| `cmd/vice/config.go` | modify | `ShowVoiceSwitch bool` (default true); `VoiceSwitchPane *panes.VoiceSwitchPane` field; activate it. |
| `cmd/vice/ui.go` | modify | Show/hide handling; `DrawWindow` invocation; settings collapsing header; thread pane into `MessagesPane.DrawWindow`. |
| `cmd/vice/main.go` | modify | Wire `cc.CanTransmit`; per-frame `Reconcile`; `ResetSim` call; persist `ShowVoiceSwitch`. |

---

## Task 1: Skeleton — `VoiceSwitchPane` type, constructor, lifecycle stubs

**Files:**
- Create: `panes/voiceswitch.go`
- Test: `panes/voiceswitch_test.go`

- [ ] **Step 1: Write the failing test**

```go
// panes/voiceswitch_test.go
package panes

import (
	"testing"

	av "github.com/mmp/vice/aviation"
)

func TestNewVoiceSwitchPane_DefaultsAndGuardConstant(t *testing.T) {
	vs := NewVoiceSwitchPane()
	if vs == nil {
		t.Fatal("NewVoiceSwitchPane returned nil")
	}
	if vs.FontSize == 0 {
		t.Errorf("FontSize = 0, want non-zero default")
	}
	if vs.seeded {
		t.Errorf("seeded = true on fresh pane, want false")
	}
	if len(vs.rows) != 0 {
		t.Errorf("rows length = %d on fresh pane, want 0", len(vs.rows))
	}
	if GuardFrequency != av.NewFrequency(121.500) {
		t.Errorf("GuardFrequency = %v, want %v", GuardFrequency, av.NewFrequency(121.500))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./panes/ -run TestNewVoiceSwitchPane_DefaultsAndGuardConstant -v`
Expected: FAIL with "undefined: NewVoiceSwitchPane" / "undefined: GuardFrequency".

- [ ] **Step 3: Write minimal implementation**

```go
// panes/voiceswitch.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/platform"
	"github.com/mmp/vice/renderer"
	"github.com/mmp/vice/sim"
)

// GuardFrequency is the standard 121.500 MHz emergency/guard frequency.
var GuardFrequency = av.NewFrequency(121.500)

type VoiceSwitchPane struct {
	FontSize int
	font     *renderer.Font

	rows     []voiceSwitchRow
	seeded   bool
	addInput string
}

type voiceSwitchRow struct {
	Freq  av.Frequency
	RX    bool
	TX    bool
	Owned bool
	Guard bool
}

func NewVoiceSwitchPane() *VoiceSwitchPane {
	return &VoiceSwitchPane{FontSize: 12}
}

func (vs *VoiceSwitchPane) Activate(r renderer.Renderer, p platform.Platform, eventStream *sim.EventStream, lg *log.Logger) {
	if vs.FontSize == 0 {
		vs.FontSize = 12
	}
	if vs.font = renderer.GetFont(renderer.FontIdentifier{Name: renderer.FlightStripPrinter, Size: vs.FontSize}); vs.font == nil {
		vs.font = renderer.GetDefaultFont()
	}
}

func (vs *VoiceSwitchPane) ResetSim(c *client.ControlClient, p platform.Platform, lg *log.Logger) {
	vs.rows = nil
	vs.seeded = false
	vs.addInput = ""
}

func (vs *VoiceSwitchPane) DisplayName() string { return "Voice Switch" }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./panes/ -run TestNewVoiceSwitchPane_DefaultsAndGuardConstant -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panes/voiceswitch.go panes/voiceswitch_test.go
git commit -m "panes: add VoiceSwitchPane skeleton with GuardFrequency constant"
```

---

## Task 2: Reconcile — auto-seed and consolidation tracking

**Files:**
- Modify: `panes/voiceswitch.go`
- Test: `panes/voiceswitch_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `panes/voiceswitch_test.go`:

```go
import (
	"github.com/mmp/vice/sim"
	// (av already imported)
)

func makeStateWithControllers(tcw sim.TCW, owned map[sim.ControlPosition]av.Frequency, others map[sim.ControlPosition]av.Frequency) *sim.CommonState {
	controllers := map[sim.ControlPosition]*av.Controller{}
	for pos, freq := range owned {
		controllers[pos] = &av.Controller{Callsign: string(pos), Frequency: freq}
	}
	for pos, freq := range others {
		controllers[pos] = &av.Controller{Callsign: string(pos), Frequency: freq}
	}

	primary := sim.TCP("")
	var ownedPositions []sim.ControlPosition
	for pos := range owned {
		ownedPositions = append(ownedPositions, pos)
		if primary == "" {
			primary = sim.TCP(pos)
		}
	}

	return &sim.CommonState{
		Controllers: controllers,
		DynamicState: sim.DynamicState{
			UserTCW: tcw,
			CurrentConsolidation: map[sim.TCW]*sim.TCPConsolidation{
				tcw: {PrimaryTCP: primary, AdditionalTCPs: ownedPositionsToTCPs(ownedPositions[1:])},
			},
		},
	}
}

func ownedPositionsToTCPs(pos []sim.ControlPosition) []sim.TCP {
	out := make([]sim.TCP, len(pos))
	for i, p := range pos {
		out[i] = sim.TCP(p)
	}
	return out
}

func TestReconcile_AutoSeedAddsGuardAndOwnedRows(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{
			"JFK_TWR": av.NewFrequency(124.350),
			"JFK_GND": av.NewFrequency(121.900),
		}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	if !vs.seeded {
		t.Fatal("seeded=false after first reconcile")
	}
	if len(vs.rows) != 3 {
		t.Fatalf("rows=%d, want 3 (guard + 2 owned)", len(vs.rows))
	}

	// Find each expected row
	freqs := map[av.Frequency]voiceSwitchRow{}
	for _, r := range vs.rows {
		freqs[r.Freq] = r
	}
	guard, ok := freqs[GuardFrequency]
	if !ok || !guard.Guard || !guard.RX || !guard.TX {
		t.Errorf("guard row missing or wrong: %+v", guard)
	}
	twr, ok := freqs[av.NewFrequency(124.350)]
	if !ok || !twr.Owned || !twr.RX || !twr.TX {
		t.Errorf("JFK_TWR row missing or wrong: %+v", twr)
	}
	gnd, ok := freqs[av.NewFrequency(121.900)]
	if !ok || !gnd.Owned || !gnd.RX || !gnd.TX {
		t.Errorf("JFK_GND row missing or wrong: %+v", gnd)
	}
}

func TestReconcile_DefersSeedUntilTCWAssigned(t *testing.T) {
	state := &sim.CommonState{
		Controllers:  map[sim.ControlPosition]*av.Controller{},
		DynamicState: sim.DynamicState{UserTCW: ""},
	}
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if vs.seeded {
		t.Fatal("seeded=true with empty UserTCW")
	}
	if len(vs.rows) != 0 {
		t.Fatalf("rows=%d before seed, want 0", len(vs.rows))
	}
}

func TestReconcile_LosingPositionFlipsRowOff(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	// Drop the position
	state.CurrentConsolidation["TEST"] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state)

	var twr *voiceSwitchRow
	for i := range vs.rows {
		if vs.rows[i].Freq == av.NewFrequency(124.350) {
			twr = &vs.rows[i]
		}
	}
	if twr == nil {
		t.Fatal("JFK_TWR row removed; should remain")
	}
	if twr.Owned || twr.RX || twr.TX {
		t.Errorf("after losing position: %+v, want Owned/RX/TX all false", *twr)
	}
}

func TestReconcile_GainingPositionRestoresRow(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	// Lose
	state.CurrentConsolidation["TEST"] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state)
	// Regain
	state.CurrentConsolidation["TEST"] = &sim.TCPConsolidation{PrimaryTCP: "JFK_TWR"}
	vs.reconcile(state)

	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(124.350) {
			if !r.Owned || !r.RX || !r.TX {
				t.Errorf("after regain: %+v, want Owned/RX/TX all true", r)
			}
			return
		}
	}
	t.Fatal("JFK_TWR row not found after regain")
}

func TestReconcile_GuardSurvivesReconcile(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	// User toggles guard RX off
	for i := range vs.rows {
		if vs.rows[i].Guard {
			vs.rows[i].RX = false
		}
	}

	// Reconcile after some change
	state.CurrentConsolidation["TEST"] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state)

	for _, r := range vs.rows {
		if r.Guard {
			if r.RX {
				t.Errorf("guard RX flipped back to true after reconcile; user toggle should persist")
			}
			return
		}
	}
	t.Fatal("guard row missing after reconcile")
}

func TestReconcile_DedupesSharedFrequency(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{
			"JFK_TWR_1": av.NewFrequency(124.350),
			"JFK_TWR_2": av.NewFrequency(124.350),
		}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	count := 0
	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(124.350) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared-freq rows = %d, want 1 (deduped)", count)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./panes/ -run "TestReconcile" -v`
Expected: FAIL — `vs.reconcile undefined` and friends.

- [ ] **Step 3: Implement `reconcile`**

Add to `panes/voiceswitch.go`:

```go
// Reconcile syncs the row list with the current sim state. It MUST be called
// every frame from the main loop (cmd/vice/main.go), regardless of whether
// the voice switch window is visible — the RX state is consulted by the
// messages pane on every event.
func (vs *VoiceSwitchPane) Reconcile(c *client.ControlClient) {
	if c == nil || c.State == nil {
		return
	}
	vs.reconcile(c.State)
}

func (vs *VoiceSwitchPane) reconcile(ss *sim.CommonState) {
	// Step 1: defer seeding until a TCW is assigned.
	if !vs.seeded && ss.UserTCW == "" {
		return
	}

	// Step 2: ensure guard row exists (idempotent).
	hasGuard := false
	for _, r := range vs.rows {
		if r.Guard {
			hasGuard = true
			break
		}
	}
	if !hasGuard {
		vs.rows = append(vs.rows, voiceSwitchRow{
			Freq: GuardFrequency, RX: true, TX: true, Guard: true,
		})
	}

	// Step 3: build set of currently-owned freqs for this TCW.
	currentlyOwned := map[av.Frequency]bool{}
	for _, pos := range ss.GetPositionsForTCW(ss.UserTCW) {
		ctrl, ok := ss.Controllers[pos]
		if !ok || ctrl == nil || ctrl.Frequency == 0 {
			continue
		}
		currentlyOwned[ctrl.Frequency] = true
	}

	// Step 4: update existing rows.
	rowFreqs := map[av.Frequency]bool{}
	for i := range vs.rows {
		r := &vs.rows[i]
		rowFreqs[r.Freq] = true
		if r.Guard {
			continue // user-managed only; reconcile never touches
		}
		if r.Owned && !currentlyOwned[r.Freq] {
			r.Owned, r.RX, r.TX = false, false, false
		} else if !r.Owned && currentlyOwned[r.Freq] {
			r.Owned, r.RX, r.TX = true, true, true
		}
	}

	// Step 5: append rows for newly-owned freqs not yet present.
	for freq := range currentlyOwned {
		if !rowFreqs[freq] {
			vs.rows = append(vs.rows, voiceSwitchRow{
				Freq: freq, RX: true, TX: true, Owned: true,
			})
		}
	}

	vs.seeded = true
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./panes/ -run "TestReconcile" -v`
Expected: PASS for all reconcile tests. If `GetPositionsForTCW`'s actual signature differs from what's assumed in the test helper, fix the test helper to match (use real `TCPConsolidation` field names from `sim/state.go`).

- [ ] **Step 5: Commit**

```bash
git add panes/voiceswitch.go panes/voiceswitch_test.go
git commit -m "panes: VoiceSwitchPane reconcile (auto-seed, guard row, gain/lose)"
```

---

## Task 3: `IsRX` helper

**Files:**
- Modify: `panes/voiceswitch.go`
- Test: `panes/voiceswitch_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `panes/voiceswitch_test.go`:

```go
func TestIsRX_OwnedDefaultsTrue(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if !vs.IsRX("JFK_TWR", state) {
		t.Errorf("IsRX(JFK_TWR) = false on freshly-seeded pane, want true")
	}
}

func TestIsRX_RXOffSuppresses(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	for i := range vs.rows {
		if vs.rows[i].Freq == av.NewFrequency(124.350) {
			vs.rows[i].RX = false
		}
	}
	if vs.IsRX("JFK_TWR", state) {
		t.Errorf("IsRX = true after RX toggled off, want false")
	}
}

func TestIsRX_UnresolvableFallsBackToUserControlsPosition(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	// "_TOWER" sentinel is not in Controllers map.
	got := vs.IsRX("_TOWER", state)
	want := state.UserControlsPosition("_TOWER")
	if got != want {
		t.Errorf("IsRX(_TOWER) = %v, want fallback %v", got, want)
	}
}

func TestIsRX_ResolvableButNoRowFallsBack(t *testing.T) {
	// Add a controller that's not owned and not in the row list.
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)},
		map[sim.ControlPosition]av.Frequency{"BOS_TWR": av.NewFrequency(127.750)})

	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	got := vs.IsRX("BOS_TWR", state)
	want := state.UserControlsPosition("BOS_TWR")
	if got != want {
		t.Errorf("IsRX(BOS_TWR) = %v, want fallback %v", got, want)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./panes/ -run "TestIsRX" -v`
Expected: FAIL — `vs.IsRX undefined`.

- [ ] **Step 3: Implement `IsRX`**

Add to `panes/voiceswitch.go`:

```go
// IsRX reports whether transmissions addressed to pos should be received
// (shown in messages pane / trigger audio alert) by the user.
//
// Resolution order:
//  1. If pos cannot resolve to a numeric frequency (sentinel like "_TOWER",
//     virtual/external controllers without a Frequency field) →
//     fall back to ss.UserControlsPosition(pos).
//  2. If a row exists for that frequency → return row.RX.
//  3. No row for that frequency (pre-seed, or freq not tuned) →
//     fall back to ss.UserControlsPosition(pos).
func (vs *VoiceSwitchPane) IsRX(pos sim.ControlPosition, ss *sim.CommonState) bool {
	ctrl, ok := ss.Controllers[pos]
	if !ok || ctrl == nil || ctrl.Frequency == 0 {
		return ss.UserControlsPosition(pos)
	}
	for _, r := range vs.rows {
		if r.Freq == ctrl.Frequency {
			return r.RX
		}
	}
	return ss.UserControlsPosition(pos)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./panes/ -run "TestIsRX" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panes/voiceswitch.go panes/voiceswitch_test.go
git commit -m "panes: VoiceSwitchPane IsRX helper for messages-pane gate"
```

---

## Task 4: TX helpers — `CanTransmitOnPrimary`, `CanGuardTransmit`, `AllowsCommand`

**Files:**
- Modify: `panes/voiceswitch.go`
- Test: `panes/voiceswitch_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `panes/voiceswitch_test.go`:

```go
func TestCanTransmitOnPrimary_DefaultsTrue(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if !vs.CanTransmitOnPrimary(state) {
		t.Errorf("CanTransmitOnPrimary = false on freshly-seeded pane, want true")
	}
}

func TestCanTransmitOnPrimary_OffBlocks(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	for i := range vs.rows {
		if vs.rows[i].Freq == av.NewFrequency(124.350) {
			vs.rows[i].TX = false
		}
	}
	if vs.CanTransmitOnPrimary(state) {
		t.Errorf("CanTransmitOnPrimary = true after TX off, want false")
	}
}

func TestCanTransmitOnPrimary_PreSeedReturnsTrue(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	// no reconcile → no rows
	if !vs.CanTransmitOnPrimary(state) {
		t.Errorf("CanTransmitOnPrimary = false pre-seed, want true (don't break commands)")
	}
}

func TestCanGuardTransmit_DefaultsTrue(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if !vs.CanGuardTransmit() {
		t.Errorf("CanGuardTransmit = false on seeded pane, want true")
	}
}

func TestCanGuardTransmit_OffBlocks(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	for i := range vs.rows {
		if vs.rows[i].Guard {
			vs.rows[i].TX = false
		}
	}
	if vs.CanGuardTransmit() {
		t.Errorf("CanGuardTransmit = true after guard TX off, want false")
	}
}

func TestAllowsCommand_RoutesByGuardToken(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	// Both gates default true → both commands allowed.
	if !vs.AllowsCommand("FH 270", state) {
		t.Errorf("AllowsCommand(FH 270) = false, want true")
	}
	if !vs.AllowsCommand("GUARD FC 134050", state) {
		t.Errorf("AllowsCommand(GUARD ...) = false, want true")
	}

	// Turn off primary TX. GUARD should still pass; non-GUARD should fail.
	for i := range vs.rows {
		if vs.rows[i].Owned {
			vs.rows[i].TX = false
		}
	}
	if vs.AllowsCommand("FH 270", state) {
		t.Errorf("AllowsCommand(FH 270) = true after primary TX off, want false")
	}
	if !vs.AllowsCommand("GUARD FC 134050", state) {
		t.Errorf("AllowsCommand(GUARD ...) = false when only primary TX off, want true")
	}

	// Turn off guard TX. GUARD should now fail; primary irrelevant.
	for i := range vs.rows {
		if vs.rows[i].Guard {
			vs.rows[i].TX = false
		}
	}
	if vs.AllowsCommand("GUARD FC 134050", state) {
		t.Errorf("AllowsCommand(GUARD ...) = true after guard TX off, want false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./panes/ -run "TestCanTransmit|TestCanGuard|TestAllows" -v`
Expected: FAIL — undefined helpers.

- [ ] **Step 3: Implement the helpers**

Add to `panes/voiceswitch.go` (and add `"strings"` to the imports):

```go
// CanTransmitOnPrimary reports whether a non-GUARD command from this user
// should be transmitted. Pre-seed and unresolvable cases default to true so
// commands aren't silently broken when the model can't tell.
func (vs *VoiceSwitchPane) CanTransmitOnPrimary(ss *sim.CommonState) bool {
	primary := ss.PrimaryPositionForTCW(ss.UserTCW)
	ctrl, ok := ss.Controllers[primary]
	if !ok || ctrl == nil || ctrl.Frequency == 0 {
		return true
	}
	for _, r := range vs.rows {
		if r.Freq == ctrl.Frequency {
			return r.TX
		}
	}
	return true
}

// CanGuardTransmit reports whether a GUARD command from this user should be
// transmitted. Pre-seed (no guard row yet) defaults to true.
func (vs *VoiceSwitchPane) CanGuardTransmit() bool {
	for _, r := range vs.rows {
		if r.Guard {
			return r.TX
		}
	}
	return true
}

// AllowsCommand inspects cmd for a GUARD token. If present, returns
// CanGuardTransmit(). Otherwise returns CanTransmitOnPrimary(ss).
//
// Detection: case-insensitive, whitespace-bounded "GUARD" anywhere in the
// command body. The command callsign has already been split off by
// AircraftCommandRequest.Callsign, so cmd contains only the post-callsign
// instruction tokens.
func (vs *VoiceSwitchPane) AllowsCommand(cmd string, ss *sim.CommonState) bool {
	for _, tok := range strings.Fields(cmd) {
		if strings.EqualFold(tok, "GUARD") {
			return vs.CanGuardTransmit()
		}
	}
	return vs.CanTransmitOnPrimary(ss)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./panes/ -run "TestCanTransmit|TestCanGuard|TestAllows" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panes/voiceswitch.go panes/voiceswitch_test.go
git commit -m "panes: VoiceSwitchPane TX helpers (primary, guard, AllowsCommand)"
```

---

## Task 5: Manual add and remove

**Files:**
- Modify: `panes/voiceswitch.go`
- Test: `panes/voiceswitch_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `panes/voiceswitch_test.go`:

```go
func TestManualAdd_ValidFreqAppendsRow(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)},
		map[sim.ControlPosition]av.Frequency{"BOS_TWR": av.NewFrequency(127.750)})
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	if !vs.tryAddFreq(av.NewFrequency(127.750), state) {
		t.Fatal("tryAddFreq returned false for valid scenario freq")
	}
	found := false
	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(127.750) {
			if r.Owned || r.Guard || !r.RX || r.TX {
				t.Errorf("manual-added row state = %+v, want RX:true TX:false Owned:false Guard:false", r)
			}
			found = true
		}
	}
	if !found {
		t.Error("manual-added row not present")
	}
}

func TestManualAdd_InvalidFreqRejected(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	before := len(vs.rows)
	if vs.tryAddFreq(av.NewFrequency(135.000), state) {
		t.Error("tryAddFreq returned true for unknown freq, want false")
	}
	if len(vs.rows) != before {
		t.Errorf("rows changed (%d → %d) on rejected add", before, len(vs.rows))
	}
}

func TestManualAdd_DuplicateRejected(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)

	before := len(vs.rows)
	if vs.tryAddFreq(av.NewFrequency(124.350), state) {
		t.Error("tryAddFreq returned true for duplicate freq, want false")
	}
	if len(vs.rows) != before {
		t.Errorf("rows changed (%d → %d) on duplicate add", before, len(vs.rows))
	}
}

func TestManualRemove_NonOwnedRow(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)},
		map[sim.ControlPosition]av.Frequency{"BOS_TWR": av.NewFrequency(127.750)})
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	vs.tryAddFreq(av.NewFrequency(127.750), state)

	if !vs.removeFreq(av.NewFrequency(127.750)) {
		t.Error("removeFreq returned false for non-owned row")
	}
	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(127.750) {
			t.Error("non-owned row still present after remove")
		}
	}
}

func TestManualRemove_OwnedRowRejected(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if vs.removeFreq(av.NewFrequency(124.350)) {
		t.Error("removeFreq returned true for owned row, want false (button shouldn't be exposed)")
	}
}

func TestManualRemove_GuardRejected(t *testing.T) {
	state := makeStateWithControllers("TEST",
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)
	vs := NewVoiceSwitchPane()
	vs.reconcile(state)
	if vs.removeFreq(GuardFrequency) {
		t.Error("removeFreq returned true for guard row, want false")
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./panes/ -run "TestManualAdd|TestManualRemove" -v`
Expected: FAIL — undefined `tryAddFreq`/`removeFreq`.

- [ ] **Step 3: Implement add/remove**

Add to `panes/voiceswitch.go`:

```go
// tryAddFreq appends a manually-tuned row for freq if (a) freq matches at
// least one controller in the scenario and (b) freq isn't already a row.
// Returns true if the row was appended.
func (vs *VoiceSwitchPane) tryAddFreq(freq av.Frequency, ss *sim.CommonState) bool {
	// Reject duplicates.
	for _, r := range vs.rows {
		if r.Freq == freq {
			return false
		}
	}
	// Validate against scenario controllers.
	valid := false
	for _, ctrl := range ss.Controllers {
		if ctrl != nil && ctrl.Frequency == freq {
			valid = true
			break
		}
	}
	if !valid {
		return false
	}
	vs.rows = append(vs.rows, voiceSwitchRow{
		Freq: freq, RX: true, TX: false, Owned: false, Guard: false,
	})
	return true
}

// removeFreq drops the row for freq, but only if the row is neither Owned
// nor Guard. Returns true if a row was removed.
func (vs *VoiceSwitchPane) removeFreq(freq av.Frequency) bool {
	for i, r := range vs.rows {
		if r.Freq == freq {
			if r.Owned || r.Guard {
				return false
			}
			vs.rows = append(vs.rows[:i], vs.rows[i+1:]...)
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./panes/ -run "TestManualAdd|TestManualRemove" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add panes/voiceswitch.go panes/voiceswitch_test.go
git commit -m "panes: VoiceSwitchPane manual add/remove"
```

---

## Task 6: Wire RX gate into `panes/messages.go`

**Files:**
- Modify: `panes/messages.go`

- [ ] **Step 1: Read current `processEvents` signature and call sites**

Run: `grep -n "processEvents\|DrawWindow" panes/messages.go | head -20`
Expected: see the existing `MessagesPane.DrawWindow` and `processEvents` signatures.

- [ ] **Step 2: Add `*VoiceSwitchPane` parameter to `MessagesPane.DrawWindow` and thread it to `processEvents`**

Find `MessagesPane.DrawWindow(...)` in `panes/messages.go` and add `voiceSwitch *VoiceSwitchPane` to the parameter list (placed right after the `*client.ControlClient` argument). Pass it through to `processEvents`.

Change `processEvents` signature similarly: add `voiceSwitch *VoiceSwitchPane` as the last parameter.

- [ ] **Step 3: Replace the RX check at line 166**

In `panes/messages.go` replace:

```go
toUs := c.State.UserControlsPosition(event.ToController)
```

with:

```go
toUs := voiceSwitch.IsRX(event.ToController, &c.State)
```

Leave the surrounding `priv := c.State.TCWIsPrivileged(...)` and `if !toUs && !priv { break }` lines unchanged.

- [ ] **Step 4: Build verify**

Run: `go build ./...`
Expected: build error in `cmd/vice/ui.go` (caller of `MessagesPane.DrawWindow` doesn't pass `voiceSwitch` yet). That's OK; the next task wires it. Confirm only that error appears, not unrelated breakage.

- [ ] **Step 5: Commit**

```bash
git add panes/messages.go
git commit -m "panes: route messages RX gate through voice switch IsRX"
```

---

## Task 7: Add `CanTransmit` chokepoint in `client/control.go`

**Files:**
- Modify: `client/control.go`

- [ ] **Step 1: Add the field on `ControlClient`**

In `client/control.go` (or `client/client.go` where `ControlClient` is declared — find via `grep -n "type ControlClient struct" client/`), add a new field:

```go
// CanTransmit, if non-nil, is consulted at the top of RunAircraftCommands.
// If it returns false, the RPC is silently dropped (no state change, no error
// to the caller). Wired by cmd/vice to call into the voice switch pane.
CanTransmit func(cmd string) bool
```

- [ ] **Step 2: Gate at the top of `RunAircraftCommands`**

In `client/control.go` at the top of `func (c *ControlClient) RunAircraftCommands(req AircraftCommandRequest, handleResult func(message string, remainingInput string))`:

```go
if c.CanTransmit != nil && !c.CanTransmit(req.Commands) {
    return
}
```

Insert as the very first statement of the function body, before TTS hold or any other side effects.

- [ ] **Step 3: Build verify**

Run: `go build ./...`
Expected: build succeeds (this change is additive; no callers required).

- [ ] **Step 4: Commit**

```bash
git add client/control.go
git commit -m "client: gate RunAircraftCommands on optional CanTransmit callback"
```

---

## Task 8: `DrawWindow` rendering

**Files:**
- Modify: `panes/voiceswitch.go`

- [ ] **Step 1: Implement `DrawWindow` and `DrawUI`**

Append to `panes/voiceswitch.go`:

```go
import (
	"fmt"
	"strconv"

	"github.com/AllenDang/cimgui-go/imgui"
)

// DrawUI renders the per-pane settings (font size selector). Called from
// the Settings dialog's collapsing header.
func (vs *VoiceSwitchPane) DrawUI(p platform.Platform, config *platform.Config) {
	id := renderer.FontIdentifier{Name: vs.font.Id.Name, Size: vs.FontSize}
	if newFont, changed := renderer.DrawFontSizeSelector(&id); changed {
		vs.FontSize = newFont.Size
		vs.font = newFont
	}
}

// DrawWindow renders the voice switch window. The pane's row list must be
// kept current by Reconcile being called from the main loop each frame
// (regardless of whether this window is shown).
func (vs *VoiceSwitchPane) DrawWindow(show *bool, c *client.ControlClient,
	p platform.Platform, unpinnedWindows map[string]struct{}, lg *log.Logger) {

	if show != nil && !*show {
		return
	}

	imgui.SetNextWindowSizeConstraints(imgui.Vec2{X: 240, Y: 160}, imgui.Vec2{X: 4096, Y: 4096})
	imgui.BeginV("Voice Switch", show, 0)
	DrawPinButton("Voice Switch", unpinnedWindows, p)
	if vs.font != nil {
		vs.font.ImguiPush()
	}

	// Sort rows: guard first, then by frequency ascending.
	displayOrder := make([]int, len(vs.rows))
	for i := range displayOrder {
		displayOrder[i] = i
	}
	// Simple two-pass: guard rows up front, others in original order.
	guardFirst := displayOrder[:0]
	for _, i := range displayOrder {
		if vs.rows[i].Guard {
			guardFirst = append(guardFirst, i)
		}
	}
	for _, i := range displayOrder {
		if !vs.rows[i].Guard {
			guardFirst = append(guardFirst, i)
		}
	}

	var toRemove av.Frequency
	for _, i := range guardFirst {
		row := &vs.rows[i]
		imgui.PushIDInt(int32(i))

		imgui.Checkbox("RX", &row.RX)
		imgui.SameLine()
		imgui.Checkbox("TX", &row.TX)
		imgui.SameLine()
		imgui.Text(row.Freq.String())
		if row.Guard {
			imgui.SameLine()
			imgui.Text("GUARD")
		}

		// Tooltip: list controller(s) on this freq.
		if imgui.IsItemHovered() {
			vs.drawRowTooltip(row, c)
		}

		if !row.Owned && !row.Guard {
			imgui.SameLine()
			if imgui.SmallButton("x") {
				toRemove = row.Freq
			}
		}

		imgui.PopID()
	}
	if toRemove != 0 {
		vs.removeFreq(toRemove)
	}

	imgui.Separator()
	imgui.Text("Tune freq:")
	imgui.SameLine()
	imgui.SetNextItemWidth(80)
	if imgui.InputTextWithHint("##addfreq", "121.500", &vs.addInput, imgui.InputTextFlagsEnterReturnsTrue, nil, nil) {
		vs.commitAddInput(c)
	}

	if vs.font != nil {
		imgui.PopFont()
	}
	imgui.End()
}

func (vs *VoiceSwitchPane) drawRowTooltip(row *voiceSwitchRow, c *client.ControlClient) {
	imgui.BeginTooltip()
	if row.Guard {
		imgui.Text("Emergency / guard frequency (121.500 MHz)")
	} else if c != nil && c.State != nil {
		any := false
		for pos, ctrl := range c.State.Controllers {
			if ctrl != nil && ctrl.Frequency == row.Freq {
				imgui.Text(fmt.Sprintf("%s — %s", pos, ctrl.RadioName))
				any = true
			}
		}
		if !any {
			imgui.Text(row.Freq.String())
		}
	}
	imgui.EndTooltip()
}

func (vs *VoiceSwitchPane) commitAddInput(c *client.ControlClient) {
	defer func() { vs.addInput = "" }()
	if c == nil || c.State == nil {
		return
	}
	parsed, err := strconv.ParseFloat(vs.addInput, 32)
	if err != nil {
		return
	}
	freq := av.NewFrequency(float32(parsed))
	vs.tryAddFreq(freq, c.State)
}

var _ UIDrawer = (*VoiceSwitchPane)(nil)
```

- [ ] **Step 2: Build verify**

Run: `go build ./panes/`
Expected: build succeeds. If `DrawPinButton` or `UIDrawer` isn't exported in the same way as for `FlightStripPane`, mirror exactly what `panes/flightstrip.go` does at the top of its `DrawWindow`.

- [ ] **Step 3: Commit**

```bash
git add panes/voiceswitch.go
git commit -m "panes: VoiceSwitchPane DrawWindow + DrawUI rendering"
```

---

## Task 9: Wire into `cmd/vice/config.go`

**Files:**
- Modify: `cmd/vice/config.go`

- [ ] **Step 1: Add the config fields**

In `cmd/vice/config.go`, find the `Config` struct (around line 50). Next to `FlightStripPane *panes.FlightStripPane`, add:

```go
VoiceSwitchPane *panes.VoiceSwitchPane
```

Next to `ShowFlightStrips bool`, add:

```go
ShowVoiceSwitch bool
```

- [ ] **Step 2: Default-initialize**

Find the default-config block (around line 200, where `FlightStripPane: panes.NewFlightStripPane()` and `ShowFlightStrips: true` appear). Add:

```go
VoiceSwitchPane: panes.NewVoiceSwitchPane(),
ShowVoiceSwitch: true,
```

- [ ] **Step 3: Migration safety for existing configs**

Find the `if config.FlightStripPane == nil { config.FlightStripPane = panes.NewFlightStripPane() }` block (around line 254). Add the parallel:

```go
if config.VoiceSwitchPane == nil {
    config.VoiceSwitchPane = panes.NewVoiceSwitchPane()
}
```

- [ ] **Step 4: Activate on connect**

Find `c.FlightStripPane.Activate(r, p, eventStream, lg)` (around line 294). Add:

```go
c.VoiceSwitchPane.Activate(r, p, eventStream, lg)
```

- [ ] **Step 5: Build verify**

Run: `go build ./cmd/vice/`
Expected: still has unresolved references in main.go and ui.go from upcoming tasks. If the only errors come from those two files, you're on track.

- [ ] **Step 6: Commit**

```bash
git add cmd/vice/config.go
git commit -m "cmd/vice: register VoiceSwitchPane in Config"
```

---

## Task 10: Wire into `cmd/vice/ui.go`

**Files:**
- Modify: `cmd/vice/ui.go`

- [ ] **Step 1: Add the show flag and DrawWindow call**

Find `ui.showFlightStrips = config.ShowFlightStrips` (around line 145). Add:

```go
ui.showVoiceSwitch = config.ShowVoiceSwitch
```

Find the type holding `showFlightStrips bool` (the `ui` struct nearby) and add a parallel `showVoiceSwitch bool` field.

Find the `config.FlightStripPane.DrawWindow(...)` call (around line 340). Add immediately after:

```go
config.VoiceSwitchPane.DrawWindow(&ui.showVoiceSwitch, controlClient, p, config.UnpinnedWindows, lg)
```

- [ ] **Step 2: Settings collapsing header**

Find the `if imgui.CollapsingHeaderBoolPtr(config.FlightStripPane.DisplayName(), nil) {` block (around line 1140). Add immediately after its closing brace:

```go
if imgui.CollapsingHeaderBoolPtr(config.VoiceSwitchPane.DisplayName(), nil) {
    config.VoiceSwitchPane.DrawUI(p, &config.Config)
}
```

- [ ] **Step 3: Thread voice switch into messages pane**

Find the call to `config.MessagesPane.DrawWindow(...)`. Add `config.VoiceSwitchPane` as the new parameter (matching the new signature from Task 6).

- [ ] **Step 4: Build verify**

Run: `go build ./cmd/vice/`
Expected: still missing wiring in main.go (CanTransmit + per-frame Reconcile + ResetSim). Final piece in next task.

- [ ] **Step 5: Commit**

```bash
git add cmd/vice/ui.go
git commit -m "cmd/vice: render voice switch window and settings header; thread into messages pane"
```

---

## Task 11: Wire into `cmd/vice/main.go` (CanTransmit, Reconcile, ResetSim, persistence)

**Files:**
- Modify: `cmd/vice/main.go`

- [ ] **Step 1: Wire `CanTransmit` once at client setup**

Find the place where `controlClient` (the `*client.ControlClient`) is constructed/assigned. Immediately after it's available, add:

```go
controlClient.CanTransmit = func(cmd string) bool {
    return config.VoiceSwitchPane.AllowsCommand(cmd, &controlClient.State)
}
```

(If `controlClient` is wrapped/abstracted, place the assignment at the same point where other one-shot ControlClient setup happens.)

- [ ] **Step 2: Per-frame Reconcile**

Find the main render loop. Before any pane draws or message processing for the frame, add:

```go
if controlClient != nil {
    config.VoiceSwitchPane.Reconcile(controlClient)
}
```

(Place near the top of the per-frame block, alongside any other per-frame state updates that happen unconditionally.)

- [ ] **Step 3: ResetSim**

Find `config.FlightStripPane.ResetSim(c, plat, lg)` (around line 568). Add:

```go
config.VoiceSwitchPane.ResetSim(c, plat, lg)
```

- [ ] **Step 4: Persist `ShowVoiceSwitch`**

Find `config.ShowFlightStrips = ui.showFlightStrips` (around line 685). Add:

```go
config.ShowVoiceSwitch = ui.showVoiceSwitch
```

- [ ] **Step 5: Build verify**

Run: `go build ./...`
Expected: full build succeeds.

- [ ] **Step 6: Run all tests**

Run: `go test ./panes/ ./client/ ./sim/ ./stt/ ./aviation/ -count=1`
Expected: all pass. The pre-existing STT failure recorded in memory is unrelated; ignore it if it persists.

- [ ] **Step 7: Commit**

```bash
git add cmd/vice/main.go
git commit -m "cmd/vice: wire voice switch CanTransmit, Reconcile, ResetSim, persistence"
```

---

## Task 12: Manual verification

**Files:** none — visual / runtime smoke test only.

- [ ] **Step 1: Build and launch**

Run: `go build -o vice ./cmd/vice/ && ./vice`
Expected: window comes up; "Voice Switch" appears in default layout alongside "Flight Strips".

- [ ] **Step 2: Verify auto-seed**

Connect to any scenario. Expected: voice switch lists the guard row (`121.500 GUARD`) plus one row per frequency of the position(s) you control. All RX+TX checkboxes default on.

- [ ] **Step 3: Verify RX gate**

Toggle RX off on your owned freq → next pilot transmission to your position is absent from the messages pane and no audio alert fires. Toggle back on → next transmission appears normally.

- [ ] **Step 4: Verify TX gate (primary)**

Toggle TX off on your owned freq → typed STARS commands silently no-op (no readback, no error, no state change on the aircraft). Toggle back on → commands work normally.

- [ ] **Step 5: Verify TX gate (guard)**

With primary TX on, guard TX on → `<callsign> GUARD` works (aircraft frequency switches). Toggle guard TX off → same command silently no-ops. Toggle primary TX off, guard TX on → `<callsign> GUARD` still works (independent gate).

- [ ] **Step 6: Verify manual add and remove**

Type a non-owned frequency that exists in the scenario → row appears with RX on, TX off, and an `x` button. Click `x` → row disappears. Type an invalid freq → input clears, no row added.

- [ ] **Step 7: Verify consolidation reaction**

Hand a position to another controller (or have one handed to you). Expected: gainer's voice switch row appears with RX+TX on; loser's row stays but flips RX+TX off.

- [ ] **Step 8: Verify reset on reconnect**

Add a manual freq, then disconnect and reconnect. Expected: manual freq is gone; auto-seed runs fresh with current owned positions; guard row is back at default RX+TX on.

- [ ] **Step 9: Final commit (if needed)**

If any minor fixes were needed during manual verification, commit them. Otherwise, no commit for this task.

---

## Self-Review Notes

- **Spec coverage check:** every "In scope" bullet from the spec has a task. Guard row (Tasks 1, 2), auto-seed/reconcile (Task 2), manual add/remove (Task 5), per-row RX/TX semantics (Tasks 3, 4), `x` button rendering rule (Task 8), tooltip (Task 8), RX gate (Task 6), TX gate via single chokepoint (Task 7 + Task 11 wiring), guard-vs-primary command routing (Task 4 + Task 7), persistence of `ShowVoiceSwitch` only (Tasks 9, 11).
- **No placeholders:** every step has the actual code or command needed.
- **Type consistency:** `voiceSwitchRow` fields used in tests match the type definition; `IsRX` / `CanTransmitOnPrimary` / `CanGuardTransmit` / `AllowsCommand` / `Reconcile` / `tryAddFreq` / `removeFreq` referenced consistently across tasks.
- **Known unknowns to confirm at execution time:**
  - Exact field names on `sim.TCPConsolidation` (the test helper assumes `PrimaryTCP` and an "additional" slice; if the real field name differs, fix the test helper to match what `GetPositionsForTCW` reads from).
  - Exact placement of `ControlClient` construction in `cmd/vice/main.go` for Task 11 Step 1; the spec says "at client setup" but the actual line varies.
  - Whether `panes` already exports `DrawPinButton` and `UIDrawer` for Task 8 — the spec assumes parity with `FlightStripPane`.
