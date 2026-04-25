// panes/voiceswitch.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"strings"

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

// Reconcile syncs the row list with the current sim state. It MUST be called
// every frame from the main loop (cmd/vice/main.go), regardless of whether
// the voice switch window is visible — the RX state is consulted by the
// messages pane on every event.
func (vs *VoiceSwitchPane) Reconcile(c *client.ControlClient) {
	if c == nil {
		return
	}
	vs.reconcile(&c.State.UserState.CommonState, c.State.UserTCW)
}

func (vs *VoiceSwitchPane) reconcile(ss *sim.CommonState, userTCW sim.TCW) {
	// Step 1: defer seeding until a TCW is assigned.
	if !vs.seeded && userTCW == "" {
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
	for _, pos := range ss.GetPositionsForTCW(userTCW) {
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

// IsRX reports whether transmissions addressed to pos should be received
// (shown in messages pane / trigger audio alert) by the user.
//
// Resolution order:
//  1. If pos cannot resolve to a numeric frequency (sentinel like "_TOWER",
//     virtual/external controllers without a Frequency field) →
//     fall back to ss.TCWControlsPosition(userTCW, pos).
//  2. If a row exists for that frequency → return row.RX.
//  3. No row for that frequency (pre-seed, or freq not tuned) →
//     fall back to ss.TCWControlsPosition(userTCW, pos).
func (vs *VoiceSwitchPane) IsRX(pos sim.ControlPosition, ss *sim.CommonState, userTCW sim.TCW) bool {
	ctrl, ok := ss.Controllers[pos]
	if !ok || ctrl == nil || ctrl.Frequency == 0 {
		return ss.TCWControlsPosition(userTCW, pos)
	}
	for _, r := range vs.rows {
		if r.Freq == ctrl.Frequency {
			return r.RX
		}
	}
	return ss.TCWControlsPosition(userTCW, pos)
}

// CanTransmitOnPrimary reports whether a non-GUARD command from this user
// should be transmitted. Pre-seed and unresolvable cases default to true so
// commands aren't silently broken when the model can't tell.
func (vs *VoiceSwitchPane) CanTransmitOnPrimary(ss *sim.CommonState, userTCW sim.TCW) bool {
	primary := ss.PrimaryPositionForTCW(userTCW)
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
// CanGuardTransmit(). Otherwise returns CanTransmitOnPrimary(ss, userTCW).
//
// Detection: case-insensitive, whitespace-bounded "GUARD" anywhere in the
// command body. The command callsign has already been split off by
// AircraftCommandRequest.Callsign, so cmd contains only the post-callsign
// instruction tokens.
func (vs *VoiceSwitchPane) AllowsCommand(cmd string, ss *sim.CommonState, userTCW sim.TCW) bool {
	for _, tok := range strings.Fields(cmd) {
		if strings.EqualFold(tok, "GUARD") {
			return vs.CanGuardTransmit()
		}
	}
	return vs.CanTransmitOnPrimary(ss, userTCW)
}

// tryAddFreq appends a manually-tuned row for freq if (a) freq matches at
// least one controller in the scenario and (b) freq isn't already a row.
// Returns true if the row was appended.
func (vs *VoiceSwitchPane) tryAddFreq(freq av.Frequency, ss *sim.CommonState) bool {
	for _, r := range vs.rows {
		if r.Freq == freq {
			return false
		}
	}
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
