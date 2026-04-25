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
