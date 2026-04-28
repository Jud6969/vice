// sim/voice_test.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"
)

func newSimWithVoice(t *testing.T) *Sim {
	t.Helper()
	s := &Sim{
		eventStream: NewEventStream(nil),
	}
	return s
}

func TestStartPTT_GrantsWhenIdle(t *testing.T) {
	s := newSimWithVoice(t)
	if !s.StartPTT("TCW-1", "tok-A") {
		t.Fatal("StartPTT should grant when idle")
	}
}

func TestStartPTT_DeniesWhenSomeoneElseTalking(t *testing.T) {
	s := newSimWithVoice(t)
	s.StartPTT("TCW-1", "tok-A")
	if s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("StartPTT for tok-B should be denied while tok-A holds the slot")
	}
}

func TestStartPTT_AllowsSameTokenReentrant(t *testing.T) {
	s := newSimWithVoice(t)
	s.StartPTT("TCW-1", "tok-A")
	if !s.StartPTT("TCW-1", "tok-A") {
		t.Fatal("StartPTT for the active talker token should remain granted")
	}
}

func TestStartPTT_DifferentTCWsAreIndependent(t *testing.T) {
	s := newSimWithVoice(t)
	if !s.StartPTT("TCW-1", "tok-A") {
		t.Fatal("StartPTT TCW-1 tok-A should grant")
	}
	if !s.StartPTT("TCW-2", "tok-B") {
		t.Fatal("StartPTT TCW-2 tok-B should grant (different TCW)")
	}
}

func TestStopPTT_ClearsSlotForActiveTalker(t *testing.T) {
	s := newSimWithVoice(t)
	s.StartPTT("TCW-1", "tok-A")
	s.StopPTT("TCW-1", "tok-A")
	if !s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("after StopPTT, TCW-1 should be free for tok-B")
	}
}

func TestStopPTT_NoOpForNonTalker(t *testing.T) {
	s := newSimWithVoice(t)
	s.StartPTT("TCW-1", "tok-A")
	s.StopPTT("TCW-1", "tok-B") // tok-B isn't the talker
	if s.StartPTT("TCW-1", "tok-C") {
		t.Fatal("tok-A should still hold TCW-1 (tok-B's StopPTT was a no-op)")
	}
}

func TestClearTalkerForToken_ClearsAllTCWsHeldByToken(t *testing.T) {
	s := newSimWithVoice(t)
	s.StartPTT("TCW-1", "tok-A")
	s.ClearTalkerForToken("tok-A")
	if !s.StartPTT("TCW-1", "tok-B") {
		t.Fatal("ClearTalkerForToken should free the slot")
	}
}

func TestRecordPTTChunk_PostsEventForActiveTalker(t *testing.T) {
	s := newSimWithVoice(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.StartPTT("TCW-1", "tok-A")
	s.RecordPTTChunk("TCW-1", "tok-A", []int16{10, 20, 30})

	got := sub.Get()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	e := got[0]
	if e.Type != PeerVoiceEvent {
		t.Fatalf("got Type=%v, want PeerVoiceEvent", e.Type)
	}
	if string(e.SourceTCW) != "TCW-1" || e.SenderToken != "tok-A" || len(e.VoiceChunk) != 3 || e.VoiceEnd {
		t.Errorf("event = %+v", e)
	}
}

func TestRecordPTTChunk_DropsIfNotActiveTalker(t *testing.T) {
	s := newSimWithVoice(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.StartPTT("TCW-1", "tok-A")
	s.RecordPTTChunk("TCW-1", "tok-IMPOSTER", []int16{1, 2, 3})

	if got := sub.Get(); len(got) != 0 {
		t.Fatalf("expected no events for non-talker chunk, got %d", len(got))
	}
}

func TestStopPTT_PostsEndEvent(t *testing.T) {
	s := newSimWithVoice(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.StartPTT("TCW-1", "tok-A")
	s.StopPTT("TCW-1", "tok-A")

	got := sub.Get()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1 (end event)", len(got))
	}
	if got[0].Type != PeerVoiceEvent || !got[0].VoiceEnd {
		t.Errorf("event = %+v, want PeerVoiceEvent with VoiceEnd=true", got[0])
	}
}
