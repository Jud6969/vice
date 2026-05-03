// sim/voice_integration_test.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
)

// Two controllers (tokens) signed in to the same TCW. A presses PTT and
// streams; B's filtered event slice for that TCW must contain A's
// chunks, but A's own slice must not.
func TestVoiceRelay_TwoClientsSameTCW(t *testing.T) {
	s := newSimWithVoice(t)
	subA := s.eventStream.Subscribe()
	defer subA.Unsubscribe()
	subB := s.eventStream.Subscribe()
	defer subB.Unsubscribe()

	// A acquires the slot; B is denied.
	if !s.StartPTT("TCW-1", "tok-A") {
		t.Fatal("A should be granted")
	}
	if s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("B should be denied while A is talking")
	}

	// A streams three chunks.
	for _, chunk := range [][]int16{{1, 2}, {3, 4}, {5, 6}} {
		s.RecordPTTChunk("TCW-1", "tok-A", chunk)
	}

	// A's filtered slice has no PeerVoiceEvents (self-echo dropped).
	aEvents := s.PrepareRadioTransmissionsForTCWAndToken("TCW-1", "tok-A", subA.Get())
	for _, e := range aEvents {
		if e.Type == PeerVoiceEvent {
			t.Errorf("A should not see own voice events; got %+v", e)
		}
	}

	// B's filtered slice has all three chunks in order.
	bEvents := s.PrepareRadioTransmissionsForTCWAndToken("TCW-1", "tok-B", subB.Get())
	var voiceCount int
	for i, e := range bEvents {
		if e.Type != PeerVoiceEvent {
			continue
		}
		voiceCount++
		if e.SourceTCW != "TCW-1" || e.SenderToken != "tok-A" {
			t.Errorf("B chunk %d wrong source: %+v", i, e)
		}
	}
	if voiceCount != 3 {
		t.Fatalf("B should see 3 voice chunks, got %d", voiceCount)
	}

	// A stops; B can now start.
	s.StopPTT("TCW-1", "tok-A")
	if !s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("after A stops, B should be granted")
	}
}

// Different TCW: events from TCW-1 must not leak to a listener on TCW-2,
// even when both are subscribed to the same EventStream.
func TestVoiceRelay_DifferentTCWsDoNotLeak(t *testing.T) {
	s := newSimWithVoice(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.StartPTT("TCW-1", "tok-A")
	s.RecordPTTChunk("TCW-1", "tok-A", []int16{1, 2, 3})

	// Listener is on TCW-2 with a different token.
	out := s.PrepareRadioTransmissionsForTCWAndToken("TCW-2", "tok-B", sub.Get())
	for _, e := range out {
		if e.Type == PeerVoiceEvent {
			t.Errorf("TCW-2 listener should not see TCW-1 voice; got %+v", e)
		}
	}
}

// ClearTalkerForToken (the disconnect path) frees the slot and posts an
// end event so the listener can finalize.
func TestVoiceRelay_DisconnectMidPTT(t *testing.T) {
	s := newSimWithVoice(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.StartPTT("TCW-1", "tok-A")
	s.RecordPTTChunk("TCW-1", "tok-A", []int16{1, 2, 3})

	// A disconnects.
	s.ClearTalkerForToken("tok-A")

	out := s.PrepareRadioTransmissionsForTCWAndToken("TCW-1", "tok-B", sub.Get())
	var sawChunk, sawEnd bool
	for _, e := range out {
		if e.Type != PeerVoiceEvent {
			continue
		}
		if e.VoiceEnd {
			sawEnd = true
		} else if len(e.VoiceChunk) > 0 {
			sawChunk = true
		}
	}
	if !sawChunk {
		t.Error("listener should have seen the chunk that arrived before disconnect")
	}
	if !sawEnd {
		t.Error("listener should have seen the synthetic end event from disconnect cleanup")
	}

	// And the slot is free.
	if !s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("after disconnect cleanup, slot should be free")
	}
}

// Two clients on the same TCW: a pilot transmission's PlayAt is
// observed identical on both subscriptions.
func TestRadioBus_TwoClientsSeeSamePlayAt(t *testing.T) {
	s := newSimWithRadio(t)
	subA := s.eventStream.Subscribe()
	defer subA.Unsubscribe()
	subB := s.eventStream.Subscribe()
	defer subB.Unsubscribe()

	s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "American 123, climb and maintain 8000",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	aEvents := subA.Get()
	bEvents := subB.Get()
	if len(aEvents) != 1 || len(bEvents) != 1 {
		t.Fatalf("both subscribers should see 1 event; got A=%d B=%d", len(aEvents), len(bEvents))
	}
	if !aEvents[0].PlayAt.Equal(bEvents[0].PlayAt) {
		t.Errorf("PlayAt diverged: A=%v B=%v", aEvents[0].PlayAt, bEvents[0].PlayAt)
	}
}

// Controller A starting PTT on TCW-1 advances TCW-1's RadioHoldUntil
// in a way that B's next state-snapshot will reflect.
func TestRadioBus_PTTAdvancesSharedHold(t *testing.T) {
	s := newSimWithRadio(t)

	d := s.EnsureTCWDisplay("TCW-1")
	before := d.RadioHoldUntil

	s.StartPTT("TCW-1", "tok-A")

	if !d.RadioHoldUntil.After(before) {
		t.Errorf("RadioHoldUntil did not advance after StartPTT; before=%v after=%v", before, d.RadioHoldUntil)
	}
}

// Pilot transmission on TCW-1 must NOT affect TCW-2's RadioHoldUntil.
func TestRadioBus_DifferentTCWsAreIndependent(t *testing.T) {
	s := newSimWithRadio(t)

	s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "tcw-1 traffic",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	d2 := s.EnsureTCWDisplay("TCW-2")
	if !d2.RadioHoldUntil.IsZero() {
		t.Errorf("TCW-2 should not be affected; got RadioHoldUntil=%v", d2.RadioHoldUntil)
	}
}
