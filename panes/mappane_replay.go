// pkg/panes/mappane_replay.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	"fmt"
	"time"

	"github.com/AllenDang/cimgui-go/imgui"
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client/replay"
	"github.com/mmp/vice/sim"
)

// ReplayPlayer wraps a loaded *replay.Replay and exposes it as a TrackSource.
// Time-progress is driven by Tick(now); the player advances cur as wall-clock
// elapsed time × speed corresponds to recorded frames.
type ReplayPlayer struct {
	rp      *replay.Replay
	cur     int
	playing bool
	speed   float32 // 1.0 = real-time

	// Reference points: when wallRef == cur frame's SimTime in real time.
	wallRef     time.Time
	frameRefIdx int
}

func NewReplayPlayer(rp *replay.Replay) *ReplayPlayer {
	return &ReplayPlayer{rp: rp, speed: 1.0}
}

// SetPlaying toggles the play state.
func (p *ReplayPlayer) SetPlaying(b bool) {
	p.playing = b
	p.SetWallReference(time.Now(), p.cur)
}

// SetSpeed sets the playback rate.
func (p *ReplayPlayer) SetSpeed(s float32) {
	if s <= 0 {
		s = 1
	}
	p.speed = s
	p.SetWallReference(time.Now(), p.cur)
}

// SetWallReference records that wall time `wall` corresponds to frame `idx`.
// Used internally on play/pause/scrub/speed change.
func (p *ReplayPlayer) SetWallReference(wall time.Time, idx int) {
	p.wallRef = wall
	p.frameRefIdx = idx
}

// SeekTo positions the player at the given frame index.
func (p *ReplayPlayer) SeekTo(idx int) {
	if idx < 0 {
		idx = 0
	}
	if p.rp != nil && idx >= len(p.rp.Frames) {
		idx = len(p.rp.Frames) - 1
	}
	if idx < 0 {
		idx = 0
	}
	p.cur = idx
	p.SetWallReference(time.Now(), idx)
}

// Step advances cur by delta frames (negative ok), clamped.
func (p *ReplayPlayer) Step(delta int) { p.SeekTo(p.cur + delta) }

// CurFrame returns the current frame index.
func (p *ReplayPlayer) CurFrame() int { return p.cur }

// FrameCount returns the total number of frames.
func (p *ReplayPlayer) FrameCount() int {
	if p.rp == nil {
		return 0
	}
	return len(p.rp.Frames)
}

// Speed returns the current speed.
func (p *ReplayPlayer) Speed() float32 { return p.speed }

// Playing returns whether playback is active.
func (p *ReplayPlayer) Playing() bool { return p.playing }

// Tick advances cur based on wall-clock elapsed since wallRef × speed. No-op
// when paused.
func (p *ReplayPlayer) Tick(now time.Time) {
	if !p.playing || p.rp == nil || len(p.rp.Frames) == 0 {
		return
	}
	if p.wallRef.IsZero() {
		p.SetWallReference(now, p.cur)
		return
	}
	elapsedNs := float64(now.Sub(p.wallRef).Nanoseconds()) * float64(p.speed)
	if elapsedNs <= 0 {
		return
	}
	target := p.rp.Frames[p.frameRefIdx].SimTimeUnix + int64(elapsedNs)
	idx := p.cur
	for idx+1 < len(p.rp.Frames) && p.rp.Frames[idx+1].SimTimeUnix <= target {
		idx++
	}
	p.cur = idx
	if p.cur >= len(p.rp.Frames)-1 {
		p.playing = false // auto-pause at end
	}
}

// Duration returns the elapsed real time between first and last frame.
func (p *ReplayPlayer) Duration() time.Duration {
	if p.rp == nil || len(p.rp.Frames) < 2 {
		return 0
	}
	return time.Duration(p.rp.Frames[len(p.rp.Frames)-1].SimTimeUnix - p.rp.Frames[0].SimTimeUnix)
}

// ElapsedAtCur returns the elapsed real time between first and current frame.
func (p *ReplayPlayer) ElapsedAtCur() time.Duration {
	if p.rp == nil || len(p.rp.Frames) == 0 {
		return 0
	}
	return time.Duration(p.rp.Frames[p.cur].SimTimeUnix - p.rp.Frames[0].SimTimeUnix)
}

// --- TrackSource impl ---

func (p *ReplayPlayer) Connected() bool { return p.rp != nil && len(p.rp.Frames) > 0 }
func (p *ReplayPlayer) Tracks() map[av.ADSBCallsign]*sim.Track {
	if !p.Connected() {
		return nil
	}
	return p.rp.Frames[p.cur].Tracks
}
func (p *ReplayPlayer) UserTCW() sim.TCW       { return "" }
func (p *ReplayPlayer) NmPerLongitude() float32 { return 45.5 }
func (p *ReplayPlayer) Facility() string {
	if p.rp == nil {
		return ""
	}
	return p.rp.Header.Facility
}
func (p *ReplayPlayer) Airports() map[string]*av.Airport               { return nil }
func (p *ReplayPlayer) Controllers() map[sim.ControlPosition]*av.Controller { return nil }

// DrawTimelineBar renders the play/pause/scrub/speed/step UI inside the
// existing imgui Map window. Caller must already be inside the window.
func (p *ReplayPlayer) DrawTimelineBar() {
	if p.rp == nil || len(p.rp.Frames) == 0 {
		return
	}
	icon := "Play"
	if p.playing {
		icon = "Pause"
	}
	if imgui.Button(icon) {
		p.SetPlaying(!p.playing)
	}
	imgui.SameLine()
	if imgui.Button("|<<") {
		p.Step(-1)
	}
	imgui.SameLine()
	if imgui.Button(">>|") {
		p.Step(+1)
	}
	imgui.SameLine()

	idx := int32(p.cur)
	imgui.SetNextItemWidth(-180)
	if imgui.SliderInt("##scrub", &idx, 0, int32(len(p.rp.Frames)-1)) {
		p.SeekTo(int(idx))
	}
	imgui.SameLine()

	speeds := []float32{0.25, 0.5, 1, 2, 4, 8}
	speedLabels := []string{"0.25x", "0.5x", "1x", "2x", "4x", "8x"}
	currentLabel := "1x"
	for i, s := range speeds {
		if s == p.speed {
			currentLabel = speedLabels[i]
		}
	}
	imgui.SetNextItemWidth(70)
	if imgui.BeginCombo("##speed", currentLabel) {
		for i, s := range speeds {
			if imgui.SelectableBoolV(speedLabels[i], s == p.speed, 0, imgui.Vec2{}) {
				p.SetSpeed(s)
			}
		}
		imgui.EndCombo()
	}
	imgui.SameLine()

	imgui.TextUnformatted(fmt.Sprintf("%s / %s",
		formatDur(p.ElapsedAtCur()), formatDur(p.Duration())))
}

func formatDur(d time.Duration) string {
	totalSec := int(d.Seconds())
	return fmt.Sprintf("%d:%02d", totalSec/60, totalSec%60)
}
