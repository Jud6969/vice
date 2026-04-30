// cmd/vice/replaydialog.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmp/vice/client/replay"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/panes"
	"github.com/mmp/vice/platform"

	"github.com/AllenDang/cimgui-go/imgui"
)

// ReplayPickerModalClient renders the file-picker for "Replay session…".
type ReplayPickerModalClient struct {
	platform platform.Platform
	lg       *log.Logger
	entries  []replay.FileEntry
	chosen   int // -1 if none
	err      error
}

func (c *ReplayPickerModalClient) Title() string { return "Replay session" }

func (c *ReplayPickerModalClient) Opening() {
	c.chosen = -1
	c.entries, c.err = replay.ListMostRecent(replayDir())
}

func (c *ReplayPickerModalClient) Buttons() []ModalDialogButton {
	return []ModalDialogButton{
		{text: "Cancel"},
		{
			text:     "Open",
			disabled: c.chosen < 0 || c.chosen >= len(c.entries),
			action: func() bool {
				rp, err := replay.Load(c.entries[c.chosen].Path)
				if err != nil {
					c.err = err
					return false
				}
				ui.replayPlayer = panes.NewReplayPlayer(rp)
				ui.showMap = true
				return true
			},
		},
	}
}

func (c *ReplayPickerModalClient) Draw() int {
	if c.err != nil {
		imgui.TextColored(imgui.Vec4{X: 1, Y: 0.4, Z: 0.4, W: 1}, c.err.Error())
		imgui.Separator()
	}
	if len(c.entries) == 0 {
		imgui.TextDisabled("No replay files in ~/.vice/replays/")
		return -1
	}
	for i, e := range c.entries {
		label := fmt.Sprintf("%s   (%s, %.1f MB)",
			filepath.Base(e.Path),
			e.MTime.Local().Format(time.RFC822),
			float64(e.Size)/1e6)
		if imgui.SelectableBoolV(label, c.chosen == i, 0, imgui.Vec2{}) {
			c.chosen = i
		}
	}
	return -1
}
