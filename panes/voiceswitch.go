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
