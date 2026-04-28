// client/voice_test.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package client

import (
	"testing"

	"github.com/mmp/vice/sim"
)

type fakeSink struct {
	chunks [][]int16
}

func (f *fakeSink) AppendSpeechPCM(pcm []int16) {
	cp := make([]int16, len(pcm))
	copy(cp, pcm)
	f.chunks = append(f.chunks, cp)
}

func TestPeerVoicePlayback_FeedsChunksToSink(t *testing.T) {
	es := sim.NewEventStream(nil)
	defer es.Destroy()

	pv := NewPeerVoicePlayback(nil)
	pv.SetEventStream(es)

	es.Post(sim.Event{Type: sim.PeerVoiceEvent, VoiceChunk: []int16{1, 2}})
	es.Post(sim.Event{Type: sim.PeerVoiceEvent, VoiceChunk: []int16{3, 4, 5}})
	es.Post(sim.Event{Type: sim.PeerVoiceEvent, VoiceEnd: true}) // end → drop
	es.Post(sim.Event{Type: sim.StatusMessageEvent, WrittenText: "hi"}) // non-voice → ignore

	sink := &fakeSink{}
	pv.Update(sink)

	if len(sink.chunks) != 2 {
		t.Fatalf("got %d chunks, want 2", len(sink.chunks))
	}
	if len(sink.chunks[0]) != 2 || len(sink.chunks[1]) != 3 {
		t.Errorf("chunks = %+v", sink.chunks)
	}
}

func TestPeerVoicePlayback_SetEventStream_ReplacesSubscription(t *testing.T) {
	pv := NewPeerVoicePlayback(nil)

	es1 := sim.NewEventStream(nil)
	defer es1.Destroy()
	es2 := sim.NewEventStream(nil)
	defer es2.Destroy()

	pv.SetEventStream(es1)
	pv.SetEventStream(es2) // replaces; should not panic / leak

	es2.Post(sim.Event{Type: sim.PeerVoiceEvent, VoiceChunk: []int16{42}})
	sink := &fakeSink{}
	pv.Update(sink)
	if len(sink.chunks) != 1 || sink.chunks[0][0] != 42 {
		t.Errorf("after re-setting stream, got chunks = %+v", sink.chunks)
	}
}
