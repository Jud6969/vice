# TCW Radio Bus Sync Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make same-TCW relief controllers hear AI pilot audio in lockstep and respect each other's transmission silence windows. Pilot audio plays at a server-stamped `PlayAt` instant; all hold timers (post-readback, post-contact, post-PTT) live on `TCWDisplayState` so peers stay aligned.

**Architecture:** Server-authoritative state on `sim.TCWDisplayState.RadioHoldUntil`; new `PlayAt` field on `RadioTransmissionEvent`. Three server-side write paths feed `RadioHoldUntil` (pilot TX, PTT start, PTT release). Client `TransmissionManager` becomes a passive consumer. New client component `PilotVoicePlayback` synthesizes pilot TTS for observers (today's code only synthesizes on the requesting client). Phase 0 is a separate diagnostic effort that adds temporary logs to find the existing controller-voice-relay bug; the fix itself is a follow-up plan once the bug is identified.

**Tech Stack:** Go (server + client), existing `sim.EventStream`, existing `tts.Synthesize{Readback,Contact}TTS`, existing `TCWDisplayState` snapshot transport.

---

## File Map

**Modified files:**
- `sim/eventstream.go` — add `PlayAt sim.Time` field on `Event`.
- `sim/tcw_display.go` — add `RadioHoldUntil sim.Time` field on `TCWDisplayState`; remove `ScopeSyncEnabled bool`.
- `sim/radio.go` — add `pilotTransmissionDurationEstimate`, `postEventPadFor`, and `postRadioTransmission` helper that stamps `PlayAt` and bumps `RadioHoldUntil`. Refactor `postReadbackTransmission` and the `radio.go:505` posting site to use it. Return `PlayAt` from helpers that feed RPC results.
- `sim/voice.go` — `StartPTT` extends `RadioHoldUntil` to `+60s`; `StopPTT` and `ClearTalkerForToken` shrink to `+2s`.
- `sim/sim.go` — `PilotMixUp` and the readback paths (`RunAircraftControlCommands` callers) return `PlayAt` alongside the spoken text.
- `server/dispatcher.go` — add `ReadbackPlayAt sim.Time` to `AircraftCommandsResult`; add `ContactPlayAt sim.Time` to `RequestContactResult`. Plumb from sim helpers.
- `client/voice.go` — add `PilotVoicePlayback` (mirror of `PeerVoicePlayback`) that subscribes to the local event stream, synthesizes for `RadioTransmissionEvent`s where `e.ToController.TCP != myTCP`, and enqueues PCM via the `TransmissionManager`. Wire it into `ControlClient.GetUpdates` like `PeerVoicePlayback` is wired today.
- `client/client.go` — pass `PlayAt` through `synthesizeAndEnqueueReadback`/`synthesizeAndEnqueueContact`. Wire `PilotVoicePlayback` initialization. Remove `c.transmissions.HoldAfterTransmission()` call site.
- `client/control.go` — pass `result.ReadbackPlayAt` / `result.ContactPlayAt` to synthesize calls. Remove `c.transmissions.HoldAfterSilentContact(...)` call site.
- `client/stt.go` — `queuedTransmission` gets a `PlayAt sim.Time` field. `EnqueueReadbackPCM` / `EnqueueTransmissionPCM` accept `playAt`. `Update` accepts `simTime sim.Time` and `radioHoldUntil sim.Time`; gates on shared timestamps; defers entries with future `PlayAt`. Remove `holdUntil time.Time`, `HoldAfterTransmission`, `HoldAfterSilentContact`, the local 3s/8s/2s constants.

**Test files:**
- `sim/voice_test.go` (extend) — tests for `RadioHoldUntil` writes from `StartPTT`/`StopPTT`/`ClearTalkerForToken`.
- `sim/radio_test.go` (new) — tests for `postRadioTransmission` helper: `PlayAt` stamping, `RadioHoldUntil` advancement, back-to-back queuing.
- `sim/voice_integration_test.go` (extend) — two-client `PlayAt` parity test; PTT-extends-hold test.
- `sim/tcw_display_test.go` (new or extend if exists) — round-trip of `RadioHoldUntil` through `EnsureTCWDisplay`; verify `ScopeSyncEnabled` is gone (compile-time).
- `client/voice_test.go` (extend) — tests for `PilotVoicePlayback`: synthesizes for events not from self; skips events from self; respects `PlayAt`.
- `client/stt_test.go` (new) — tests for `TransmissionManager` consuming shared `RadioHoldUntil` and per-entry `PlayAt`.

---

## Phase 0 — Diagnostics for the existing controller voice relay

**Why first:** End-to-end manual testing of Phase 1 (Task 12 below) needs the controller voice relay working. The integration tests pass but real two-machine testing produces silence. Phase 0 instruments the relay so the user can identify the bug from a live test session. The fix itself is out of scope for this plan (it depends on what the diagnostic reveals); a follow-up plan will be written once the cause is known.

### Task 0.1: Add diagnostic logs to the voice relay

**Files:**
- Modify: `sim/voice.go`
- Modify: `sim/sim.go:637-650` (`PrepareRadioTransmissionsForTCWAndToken`)
- Modify: `client/voice.go` (`PeerVoicePlayback.Update`)

- [ ] **Step 1: Add `DBG_VOICE` log in `Sim.StartPTT`**

In `sim/voice.go`, after the function decides grant/deny but before returning, log the outcome.

```go
func (s *Sim) StartPTT(tcw TCW, token string) bool {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if s.activeTalker == nil {
		s.activeTalker = make(map[TCW]string)
	}
	if existing, ok := s.activeTalker[tcw]; ok && existing != token {
		s.lg.Warnf("DBG_VOICE: StartPTT denied tcw=%q token=%s existing=%s", tcw, token[:8], existing[:8])
		return false
	}
	s.activeTalker[tcw] = token
	s.lg.Warnf("DBG_VOICE: StartPTT granted tcw=%q token=%s", tcw, token[:8])
	return true
}
```

- [ ] **Step 2: Add sampled `DBG_VOICE` log in `Sim.RecordPTTChunk`**

In `sim/voice.go`, log every Nth chunk so we don't flood. Use a private counter on the Sim or a local static — easiest is a per-call modulo on a hash of token.

```go
func (s *Sim) RecordPTTChunk(tcw TCW, token string, samples []int16) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if s.activeTalker[tcw] != token {
		s.lg.Warnf("DBG_VOICE: RecordPTTChunk dropped (not active talker) tcw=%q token=%s active=%q", tcw, token[:8], s.activeTalker[tcw])
		return
	}
	s.dbgVoiceChunkCount++
	if s.dbgVoiceChunkCount%25 == 1 {
		s.lg.Warnf("DBG_VOICE: RecordPTTChunk fanout tcw=%q token=%s samples=%d count=%d", tcw, token[:8], len(samples), s.dbgVoiceChunkCount)
	}
	s.eventStream.Post(Event{
		Type:        PeerVoiceEvent,
		SourceTCW:   tcw,
		SenderToken: token,
		VoiceChunk:  samples,
	})
}
```

Also add the counter field to `Sim` in `sim/sim.go` (find the `type Sim struct` block, add `dbgVoiceChunkCount int` near `activeTalker`).

- [ ] **Step 3: Add `DBG_VOICE` log in `PrepareRadioTransmissionsForTCWAndToken` filter**

In `sim/sim.go`, when a `PeerVoiceEvent` is dropped, log why.

```go
out := events[:0]
for _, e := range events {
	if e.Type == PeerVoiceEvent {
		if e.SourceTCW != tcw {
			s.lg.Warnf("DBG_VOICE: filter drop wrong-tcw listener_tcw=%q event_tcw=%q sender=%s", tcw, e.SourceTCW, e.SenderToken[:8])
			continue
		}
		if e.SenderToken == token {
			s.lg.Warnf("DBG_VOICE: filter drop self-echo tcw=%q token=%s", tcw, token[:8])
			continue
		}
	}
	out = append(out, e)
}
return out
```

- [ ] **Step 4: Add `DBG_VOICE` log in `client/voice.go` `PeerVoicePlayback.Update`**

After the drain loop, log how many chunks were appended this frame.

```go
func (p *PeerVoicePlayback) Update(plat PlaybackSink) {
	p.mu.Lock()
	sub := p.events
	p.mu.Unlock()
	if sub == nil {
		return
	}
	var chunkCount, sampleCount int
	for _, e := range sub.Get() {
		if e.Type != sim.PeerVoiceEvent {
			continue
		}
		if e.VoiceEnd || len(e.VoiceChunk) == 0 {
			continue
		}
		plat.AppendSpeechPCM(e.VoiceChunk)
		chunkCount++
		sampleCount += len(e.VoiceChunk)
	}
	if chunkCount > 0 {
		p.lg.Warnf("DBG_VOICE: PilotVoicePlayback drained chunks=%d samples=%d", chunkCount, sampleCount)
	}
}
```

(Note: the existing struct already has `lg *log.Logger` field — `NewPeerVoicePlayback(lg *log.Logger)`. Confirm before logging that `p.lg != nil` if necessary.)

- [ ] **Step 5: Build and verify it compiles**

Run: `go build -tags vulkan ./...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add sim/voice.go sim/sim.go client/voice.go
git commit -m "$(cat <<'EOF'
WIP: voice relay diagnostic logging

Temporary DBG_VOICE logs at four points to identify why the same-TCW
controller voice relay produces silence in real two-machine testing
even though integration tests pass. Logs are removed once the bug is
diagnosed and fixed.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 7: Manual user task — collect diagnosis**

The user runs the server and connects two real clients on Tailscale, both signing onto the same TCW. One presses PTT and speaks. The user collects the relevant `DBG_VOICE` lines from the server log and reports them. This is **not** something the agent does — it's a hand-off. The fix follows in a separate plan.

---

## Phase 1 — Radio bus sync

### Task 1: Add `RadioHoldUntil` field on `TCWDisplayState`; remove `ScopeSyncEnabled`

**Files:**
- Modify: `sim/tcw_display.go`
- Test: `sim/tcw_display_test.go` (create if absent)

- [ ] **Step 1: Write failing test for the new field**

In `sim/tcw_display_test.go`:

```go
package sim

import (
	"testing"
)

func TestEnsureTCWDisplay_HasZeroRadioHoldUntil(t *testing.T) {
	s := &Sim{}
	d := s.EnsureTCWDisplay("TCW-1")
	if !d.RadioHoldUntil.IsZero() {
		t.Errorf("RadioHoldUntil should be zero on a fresh TCWDisplayState; got %v", d.RadioHoldUntil)
	}
}

func TestEnsureTCWDisplay_RadioHoldUntilPersists(t *testing.T) {
	s := &Sim{}
	d := s.EnsureTCWDisplay("TCW-1")
	target := NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	d.RadioHoldUntil = target

	d2 := s.EnsureTCWDisplay("TCW-1")
	if !d2.RadioHoldUntil.Equal(target) {
		t.Errorf("RadioHoldUntil not preserved across EnsureTCWDisplay; want %v got %v", target, d2.RadioHoldUntil)
	}
}
```

Add `"time"` import to the file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./sim -run TestEnsureTCWDisplay_HasZeroRadioHoldUntil -v`
Expected: FAIL with `d.RadioHoldUntil undefined (type *TCWDisplayState has no field or method RadioHoldUntil)`.

- [ ] **Step 3: Add the field; remove `ScopeSyncEnabled`**

In `sim/tcw_display.go`, change the `TCWDisplayState` struct:

```go
type TCWDisplayState struct {
	Annotations map[av.ADSBCallsign]TrackAnnotations

	ScopePrefsBlob []byte
	ScopePrefsRev  uint64

	// RadioHoldUntil is the sim-time before which the TCW radio is busy
	// or in post-event quiet. All TransmissionManagers at this TCW pause
	// playback while SimTime < RadioHoldUntil. Source-agnostic: pilot
	// transmissions, controller PTTs, and post-event holds all write here.
	RadioHoldUntil Time

	Rev uint64

	Fused bool
}
```

Note `Time` (not `sim.Time`) because we're inside package `sim`. Remove the `ScopeSyncEnabled bool` field entirely.

- [ ] **Step 4: Run all sim tests**

Run: `go test ./sim/...`
Expected: PASS. If anything fails referencing `ScopeSyncEnabled`, that means there's a leftover consumer — grep for `ScopeSyncEnabled` and remove the call site (it should already be unreferenced per the spec).

```bash
grep -rn "ScopeSyncEnabled" --include="*.go"
```
Expected: no matches (after removal). If matches exist, delete those lines and re-run tests.

- [ ] **Step 5: Build the whole tree**

Run: `go build -tags vulkan ./...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add sim/tcw_display.go sim/tcw_display_test.go
git commit -m "$(cat <<'EOF'
sim: add RadioHoldUntil to TCWDisplayState; remove ScopeSyncEnabled

RadioHoldUntil is the shared cutoff sim-time before which all
TransmissionManagers at this TCW must pause playback. Server-side
writers extend it; clients read it via the existing TCWDisplay
snapshot mechanism. ScopeSyncEnabled was already deprecated and
unused; deleted in the same commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: Add `PlayAt` field on `Event`

**Files:**
- Modify: `sim/eventstream.go`
- Test: `sim/eventstream_test.go` (extend)

- [ ] **Step 1: Write failing test**

In `sim/eventstream_test.go`, add:

```go
func TestEvent_PlayAtRoundTrip(t *testing.T) {
	target := NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	e := Event{
		Type:           RadioTransmissionEvent,
		DestinationTCW: "TCW-1",
		PlayAt:         target,
	}
	if !e.PlayAt.Equal(target) {
		t.Errorf("PlayAt not preserved; want %v got %v", target, e.PlayAt)
	}
}
```

Add `"time"` import if not present.

- [ ] **Step 2: Verify it fails**

Run: `go test ./sim -run TestEvent_PlayAtRoundTrip -v`
Expected: FAIL with `unknown field PlayAt in struct literal of type Event`.

- [ ] **Step 3: Add the field**

In `sim/eventstream.go`, in the `Event` struct definition (currently at line 275), add a new field in the radio-transmission section:

```go
type Event struct {
	Type                  EventType
	ADSBCallsign          av.ADSBCallsign
	ACID                  ACID
	FromController        ControlPosition
	ToController          ControlPosition
	DestinationTCW        TCW
	WrittenText           string
	SpokenText            string
	RadioTransmissionType av.RadioTransmissionType
	// PlayAt is the sim-time when listening clients should start audio
	// playback for a RadioTransmissionEvent. Server stamps this when the
	// event is queued. Late-arriving clients (SimTime > PlayAt) play
	// immediately without drop. Zero on non-radio events.
	PlayAt              Time
	LeaderLineDirection *math.CardinalOrdinalDirection
	WaypointInfo        []math.Point2LL
	STTTranscript       string
	STTCommand          string
	STTTimings          string
	Route               av.WaypointArray

	SourceTCW   TCW
	SenderToken string
	VoiceChunk  []int16
	VoiceEnd    bool
}
```

- [ ] **Step 4: Run test**

Run: `go test ./sim -run TestEvent_PlayAtRoundTrip -v`
Expected: PASS.

- [ ] **Step 5: Build**

Run: `go build -tags vulkan ./...`
Expected: exit 0.

- [ ] **Step 6: Commit**

```bash
git add sim/eventstream.go sim/eventstream_test.go
git commit -m "$(cat <<'EOF'
sim: add PlayAt field on Event for radio transmission scheduling

PlayAt carries the server-stamped sim-time at which listening clients
should start audio playback. Non-radio events leave it zero.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Server-side helper that stamps `PlayAt` and bumps `RadioHoldUntil` for pilot transmissions

**Files:**
- Modify: `sim/radio.go`
- Test: `sim/radio_test.go` (new)

- [ ] **Step 1: Write failing test**

Create `sim/radio_test.go`:

```go
// sim/radio_test.go
package sim

import (
	"testing"
	"time"

	av "github.com/mmp/vice/aviation"
)

func newSimWithRadio(t *testing.T) *Sim {
	t.Helper()
	s := &Sim{
		eventStream: NewEventStream(nil),
		State:       &CommonState{},
	}
	s.State.SimTime = NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	return s
}

func TestPostRadioTransmission_StampsPlayAtAtSimTimePlusBuffer(t *testing.T) {
	s := newSimWithRadio(t)
	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		ADSBCallsign:          av.ADSBCallsign("AAL123"),
		DestinationTCW:        "TCW-1",
		WrittenText:           "American 123, climb and maintain 8000",
		SpokenText:            "American 123, climb and maintain 8000",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	events := sub.Get()
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	want := s.State.SimTime.Add(playAtForwardBuffer)
	if !events[0].PlayAt.Equal(want) {
		t.Errorf("PlayAt = %v, want %v", events[0].PlayAt, want)
	}
}

func TestPostRadioTransmission_AdvancesRadioHoldUntil(t *testing.T) {
	s := newSimWithRadio(t)

	s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "test", // 4 chars
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	d := s.EnsureTCWDisplay("TCW-1")
	if d.RadioHoldUntil.IsZero() {
		t.Fatal("RadioHoldUntil not advanced")
	}
	// Expected: PlayAt + 4*70ms + 3s readback pad
	playAt := s.State.SimTime.Add(playAtForwardBuffer)
	wantMin := playAt.Add(time.Duration(4) * msPerChar)
	if d.RadioHoldUntil.Before(wantMin) {
		t.Errorf("RadioHoldUntil %v earlier than minimum %v", d.RadioHoldUntil, wantMin)
	}
}

func TestPostRadioTransmission_BackToBackAnchorsToPrevious(t *testing.T) {
	s := newSimWithRadio(t)

	first := Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "first transmission",
		RadioTransmissionType: av.RadioTransmissionReadback,
	}
	s.postRadioTransmission(first)
	hold1 := s.EnsureTCWDisplay("TCW-1").RadioHoldUntil

	second := Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "second transmission",
		RadioTransmissionType: av.RadioTransmissionReadback,
	}
	s.postRadioTransmission(second)

	sub := s.eventStream.Subscribe()
	defer sub.Unsubscribe()

	// We need to inspect the second event's PlayAt; re-post and capture.
	// (Simpler: subscribe before the second post.)
	_ = hold1
	_ = sub
	// Implementation hint: this test will be reworked once the helper
	// returns PlayAt; see Step 3.
}

func TestPostRadioTransmission_DifferentTCWsIndependent(t *testing.T) {
	s := newSimWithRadio(t)

	s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "tcw-1 traffic",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	d2 := s.EnsureTCWDisplay("TCW-2")
	if !d2.RadioHoldUntil.IsZero() {
		t.Errorf("TCW-2 RadioHoldUntil should be untouched; got %v", d2.RadioHoldUntil)
	}
}
```

- [ ] **Step 2: Verify it fails**

Run: `go test ./sim -run "TestPostRadioTransmission_" -v`
Expected: FAIL — `s.postRadioTransmission undefined`, `playAtForwardBuffer undefined`, `msPerChar undefined`.

- [ ] **Step 3: Implement the helper**

In `sim/radio.go`, add at the top (after imports):

```go
// Pilot-transmission timing constants used to compute PlayAt and
// RadioHoldUntil for events posted to the TCW radio bus. These live
// here (single source of truth) instead of duplicated in the client
// TransmissionManager.
const (
	// playAtForwardBuffer is added to SimTime when stamping PlayAt so
	// listening peers reliably receive the event before its scheduled
	// start. ~200ms is comfortably above typical poll cadence on LAN
	// and Tailscale; well below the threshold of feeling delayed.
	playAtForwardBuffer = 200 * time.Millisecond

	// msPerChar approximates ATC speech rate: ~150 wpm × ~6 chars/word
	// ÷ 60 sec ≈ 15 chars/sec ≈ 67 ms/char. Rounded up to 70 ms/char
	// for a small safety margin.
	msPerChar = 70 * time.Millisecond

	// postReadbackPad is the silence window after a pilot readback,
	// giving the controller time to issue the next instruction.
	postReadbackPad = 3 * time.Second

	// postContactPad is the silence window after a pilot contact (initial
	// check-in), giving the controller longer to respond.
	postContactPad = 8 * time.Second
)

// postEventPadFor returns the post-event silence window for the given
// transmission type.
func postEventPadFor(t av.RadioTransmissionType) time.Duration {
	if t == av.RadioTransmissionContact {
		return postContactPad
	}
	return postReadbackPad
}

// pilotTransmissionDurationEstimate approximates how long it takes a
// TTS engine to render the given spoken text, based on text length.
func pilotTransmissionDurationEstimate(spoken string) time.Duration {
	return time.Duration(len(spoken)) * msPerChar
}

// postRadioTransmission stamps PlayAt on the event, advances the
// destination TCW's RadioHoldUntil, and posts the event. Caller fills
// in everything except PlayAt before calling.
//
// PlayAt is anchored to max(SimTime + buffer, current RadioHoldUntil)
// so back-to-back transmissions queue cleanly. RadioHoldUntil advances
// to PlayAt + spoken-duration estimate + post-event pad.
func (s *Sim) postRadioTransmission(e Event) Time {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	d := s.EnsureTCWDisplay(e.DestinationTCW)
	playAtFloor := s.State.SimTime.Add(playAtForwardBuffer)
	playAt := playAtFloor
	if d.RadioHoldUntil.After(playAt) {
		playAt = d.RadioHoldUntil
	}
	e.PlayAt = playAt

	endTime := playAt.Add(pilotTransmissionDurationEstimate(e.SpokenText)).Add(postEventPadFor(e.RadioTransmissionType))
	if endTime.After(d.RadioHoldUntil) {
		d.RadioHoldUntil = endTime
		d.Rev++
	}

	s.eventStream.Post(e)
	return playAt
}
```

- [ ] **Step 4: Refactor existing posting sites to use the helper**

In `sim/radio.go`, the existing `postReadbackTransmission` (line 19) currently calls `s.eventStream.Post` directly. Replace its body to use the helper:

```go
func (s *Sim) postReadbackTransmission(from av.ADSBCallsign, tr av.RadioTransmission, tcw TCW) Time {
	tr.Validate(s.lg)

	if ac, ok := s.Aircraft[from]; ok {
		ac.LastRadioTransmission = s.State.SimTime
	}

	tcp := s.State.PrimaryPositionForTCW(tcw)
	return s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		ADSBCallsign:          from,
		ToController:          tcp,
		DestinationTCW:        tcw,
		WrittenText:           tr.Written(s.Rand),
		SpokenText:            tr.Spoken(s.Rand),
		RadioTransmissionType: tr.Type,
	})
}
```

The signature now returns `Time` (the PlayAt) so callers can plumb it back through RPC results.

The other posting site at `radio.go:505` is inside a larger function that returns an `Event`. Wrap it the same way:

```go
playAt := s.postRadioTransmission(Event{
	Type:                  RadioTransmissionEvent,
	ADSBCallsign:          pc.ADSBCallsign,
	ToController:          pc.TCP,
	DestinationTCW:        s.State.TCWForPosition(pc.TCP),
	WrittenText:           baseWritten,
	SpokenText:            baseSpoken,
	RadioTransmissionType: rt.Type,
})
_ = playAt // returned to caller in Task 8 plumbing
```

For now we discard the return; Task 8 plumbs it through.

- [ ] **Step 5: Update existing callers if `postReadbackTransmission` is referenced elsewhere**

```bash
grep -rn "postReadbackTransmission\b" --include="*.go"
```
Expected: only call sites should be inside `sim/`. Update those to discard or use the returned `Time` based on their needs (Task 8 will do final plumbing).

- [ ] **Step 6: Run tests**

Run: `go test ./sim -run "TestPostRadioTransmission_" -v`
Expected: PASS for `StampsPlayAt`, `AdvancesRadioHoldUntil`, `DifferentTCWsIndependent`.

The `BackToBackAnchorsToPrevious` test is incomplete in Step 1; complete it now:

```go
func TestPostRadioTransmission_BackToBackAnchorsToPrevious(t *testing.T) {
	s := newSimWithRadio(t)

	playAt1 := s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "first transmission",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})
	playAt2 := s.postRadioTransmission(Event{
		Type:                  RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		SpokenText:            "second transmission",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	end1 := playAt1.Add(pilotTransmissionDurationEstimate("first transmission")).Add(postReadbackPad)
	if !playAt2.Equal(end1) && !playAt2.After(end1) {
		t.Errorf("second PlayAt %v should anchor to or after first end %v", playAt2, end1)
	}
}
```

Run again: `go test ./sim -run "TestPostRadioTransmission_" -v`
Expected: all four PASS.

- [ ] **Step 7: Run full sim tests**

Run: `go test ./sim/...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add sim/radio.go sim/radio_test.go
git commit -m "$(cat <<'EOF'
sim: postRadioTransmission stamps PlayAt + advances RadioHoldUntil

Single helper in sim/radio.go owns the per-TCW timing arithmetic. It
stamps PlayAt = max(SimTime+200ms, current hold), advances RadioHoldUntil
to PlayAt + duration-estimate + post-event pad, and posts the event.
Refactors postReadbackTransmission and the in-line radio.go event-post
site to route through it. Constants moved here from the client TM.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: PTT-side `RadioHoldUntil` writers in `sim/voice.go`

**Files:**
- Modify: `sim/voice.go`
- Test: `sim/voice_test.go` (extend)

- [ ] **Step 1: Write failing tests**

In `sim/voice_test.go`, append:

```go
func TestStartPTT_ExtendsRadioHoldUntil(t *testing.T) {
	s := newSimWithVoice(t)
	s.State.SimTime = NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))

	s.StartPTT("TCW-1", "tok-A")

	d := s.EnsureTCWDisplay("TCW-1")
	want := s.State.SimTime.Add(pttHoldExtension)
	if !d.RadioHoldUntil.Equal(want) {
		t.Errorf("RadioHoldUntil after StartPTT = %v, want %v", d.RadioHoldUntil, want)
	}
}

func TestStartPTT_DoesNotShrinkRadioHoldUntil(t *testing.T) {
	s := newSimWithVoice(t)
	s.State.SimTime = NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))

	d := s.EnsureTCWDisplay("TCW-1")
	d.RadioHoldUntil = s.State.SimTime.Add(2 * time.Hour) // far in the future

	s.StartPTT("TCW-1", "tok-A")

	if !d.RadioHoldUntil.Equal(s.State.SimTime.Add(2 * time.Hour)) {
		t.Errorf("StartPTT shrank RadioHoldUntil; got %v", d.RadioHoldUntil)
	}
}

func TestStopPTT_SetsRadioHoldUntilToCooldown(t *testing.T) {
	s := newSimWithVoice(t)
	s.State.SimTime = NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))

	s.StartPTT("TCW-1", "tok-A")
	s.StopPTT("TCW-1", "tok-A")

	d := s.EnsureTCWDisplay("TCW-1")
	want := s.State.SimTime.Add(pttCooldown)
	if !d.RadioHoldUntil.Equal(want) {
		t.Errorf("RadioHoldUntil after StopPTT = %v, want %v", d.RadioHoldUntil, want)
	}
}

func TestClearTalkerForToken_SetsRadioHoldUntilToCooldown(t *testing.T) {
	s := newSimWithVoice(t)
	s.State.SimTime = NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))

	s.StartPTT("TCW-1", "tok-A")
	s.ClearTalkerForToken("tok-A")

	d := s.EnsureTCWDisplay("TCW-1")
	want := s.State.SimTime.Add(pttCooldown)
	if !d.RadioHoldUntil.Equal(want) {
		t.Errorf("RadioHoldUntil after ClearTalkerForToken = %v, want %v", d.RadioHoldUntil, want)
	}
}
```

Make sure `"time"` is in the imports.

- [ ] **Step 2: Verify failure**

Run: `go test ./sim -run "TestStartPTT_Extends|TestStartPTT_DoesNot|TestStopPTT_Sets|TestClearTalkerForToken_Sets" -v`
Expected: FAIL — `pttHoldExtension undefined`, `pttCooldown undefined`, RadioHoldUntil not set.

- [ ] **Step 3: Implement writers in `sim/voice.go`**

At the top of the file (after imports), add the constants:

```go
const (
	// pttHoldExtension is the upper bound the talker slot extends
	// RadioHoldUntil while a controller is mid-PTT. Generous (60s) so
	// pilot transmissions stay parked through any plausible PTT
	// duration; replaced with pttCooldown the moment the talker
	// releases or disconnects.
	pttHoldExtension = 60 * time.Second

	// pttCooldown is the post-PTT silence window applied when a
	// controller releases or disconnects mid-PTT.
	pttCooldown = 2 * time.Second
)
```

Add `"time"` to imports.

Modify `StartPTT` to advance `RadioHoldUntil` on grant:

```go
func (s *Sim) StartPTT(tcw TCW, token string) bool {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if s.activeTalker == nil {
		s.activeTalker = make(map[TCW]string)
	}
	if existing, ok := s.activeTalker[tcw]; ok && existing != token {
		return false
	}
	s.activeTalker[tcw] = token

	d := s.EnsureTCWDisplay(tcw)
	target := s.State.SimTime.Add(pttHoldExtension)
	if target.After(d.RadioHoldUntil) {
		d.RadioHoldUntil = target
		d.Rev++
	}
	return true
}
```

Modify `StopPTT` to set the cooldown:

```go
func (s *Sim) StopPTT(tcw TCW, token string) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if s.activeTalker[tcw] != token {
		return
	}
	delete(s.activeTalker, tcw)

	d := s.EnsureTCWDisplay(tcw)
	d.RadioHoldUntil = s.State.SimTime.Add(pttCooldown)
	d.Rev++

	s.eventStream.Post(Event{
		Type:        PeerVoiceEvent,
		SourceTCW:   tcw,
		SenderToken: token,
		VoiceEnd:    true,
	})
}
```

Modify `ClearTalkerForToken` similarly — for each TCW that loses its talker, apply the cooldown:

```go
func (s *Sim) ClearTalkerForToken(token string) {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	for tcw, holder := range s.activeTalker {
		if holder == token {
			delete(s.activeTalker, tcw)

			d := s.EnsureTCWDisplay(tcw)
			d.RadioHoldUntil = s.State.SimTime.Add(pttCooldown)
			d.Rev++

			s.eventStream.Post(Event{
				Type:        PeerVoiceEvent,
				SourceTCW:   tcw,
				SenderToken: token,
				VoiceEnd:    true,
			})
		}
	}
}
```

- [ ] **Step 4: Run the new tests**

Run: `go test ./sim -run "TestStartPTT_Extends|TestStartPTT_DoesNot|TestStopPTT_Sets|TestClearTalkerForToken_Sets" -v`
Expected: PASS.

- [ ] **Step 5: Run all sim tests**

Run: `go test ./sim/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sim/voice.go sim/voice_test.go
git commit -m "$(cat <<'EOF'
sim: PTT writers extend and shrink RadioHoldUntil

StartPTT pushes RadioHoldUntil to SimTime+60s on grant (generous upper
bound while the controller talks). StopPTT and ClearTalkerForToken
replace it with SimTime+2s cooldown. Pilot transmissions on the same
TCW now park behind a live human PTT and resume after release.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 5: Two-client integration test for `RadioHoldUntil` and `PlayAt` propagation

**Files:**
- Modify: `sim/voice_integration_test.go`

- [ ] **Step 1: Write integration test**

Append to `sim/voice_integration_test.go`:

```go
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
```

- [ ] **Step 2: Run**

Run: `go test ./sim -run "TestRadioBus_" -v`
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add sim/voice_integration_test.go
git commit -m "$(cat <<'EOF'
sim: integration tests for radio-bus state propagation

Three scenarios: two subscribers see the same PlayAt for one event;
StartPTT advances RadioHoldUntil; different TCWs do not cross-pollute.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Client-side `PilotVoicePlayback` (observer-side TTS synthesis)

**Why this task exists:** Today only the requesting client synthesizes TTS for pilot readbacks/contacts (RPC-result path). Observer clients on the same TCW receive the `RadioTransmissionEvent` but never produce audio. To deliver the design's "same-TCW peers hear the same pilot voice," observers need a synthesis path. `PilotVoicePlayback` mirrors `PeerVoicePlayback` but for `RadioTransmissionEvent` instead of `PeerVoiceEvent`.

**Files:**
- Modify: `client/voice.go`
- Modify: `client/client.go`
- Test: `client/voice_test.go` (extend)

- [ ] **Step 1: Write failing test**

In `client/voice_test.go`, append (note: existing tests use stub PlaybackSink; reuse it):

```go
type recordingSink struct {
	chunks [][]int16
}

func (r *recordingSink) AppendSpeechPCM(pcm []int16) {
	r.chunks = append(r.chunks, append([]int16(nil), pcm...))
}

// (If recordingSink already exists in this file, skip redefining.)

func TestPilotVoicePlayback_SynthesizesForObservedEvent(t *testing.T) {
	es := sim.NewEventStream(nil)
	pv := NewPilotVoicePlayback(nil, "OBS_TCP")
	pv.SetEventStream(es)

	es.Post(sim.Event{
		Type:                  sim.RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		ToController:          sim.ControlPosition{TCP: "REQ_TCP"},
		SpokenText:            "ignored by stub synthesizer",
		RadioTransmissionType: av.RadioTransmissionReadback,
		PlayAt:                sim.NewSimTime(time.Now()),
	})

	var calls int
	pv.synthesize = func(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
		calls++
	}
	pv.Update()
	if calls != 1 {
		t.Fatalf("expected 1 synthesize call for observed event, got %d", calls)
	}
}

func TestPilotVoicePlayback_SkipsOwnTransmission(t *testing.T) {
	es := sim.NewEventStream(nil)
	pv := NewPilotVoicePlayback(nil, "MY_TCP")
	pv.SetEventStream(es)

	es.Post(sim.Event{
		Type:                  sim.RadioTransmissionEvent,
		DestinationTCW:        "TCW-1",
		ToController:          sim.ControlPosition{TCP: "MY_TCP"}, // self
		SpokenText:            "should skip",
		RadioTransmissionType: av.RadioTransmissionReadback,
	})

	var calls int
	pv.synthesize = func(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
		calls++
	}
	pv.Update()
	if calls != 0 {
		t.Errorf("self-transmission should be skipped (RPC-result path handles it); got %d calls", calls)
	}
}
```

Add `"time"` and `av "github.com/mmp/vice/aviation"` imports if missing.

- [ ] **Step 2: Verify failure**

Run: `go test ./client -run "TestPilotVoicePlayback_" -v`
Expected: FAIL — `NewPilotVoicePlayback undefined`.

- [ ] **Step 3: Implement `PilotVoicePlayback`**

In `client/voice.go`, add at the end:

```go
// PilotVoicePlayback synthesizes pilot TTS for RadioTransmissionEvents
// observed on the local event stream that did NOT originate from this
// controller (the observer-side counterpart to the RPC-result-driven
// requester synthesis in ControlClient.synthesizeAndEnqueue*). One per
// ControlClient; subscribed lazily to the same EventStream as
// TransmissionManager and PeerVoicePlayback.
//
// The actual TTS call is held behind a function pointer (`synthesize`)
// so tests can substitute a stub.
type PilotVoicePlayback struct {
	mu        sync.Mutex
	events    *sim.EventsSubscription
	myTCP     sim.TCP
	lg        *log.Logger
	synthesize func(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time)
}

// NewPilotVoicePlayback creates an observer-side synthesizer. myTCP is
// the local controller's primary TCP, used to skip the user's own
// transmissions (those are handled by the requester's RPC-result path).
func NewPilotVoicePlayback(lg *log.Logger, myTCP sim.TCP) *PilotVoicePlayback {
	return &PilotVoicePlayback{
		lg:    lg,
		myTCP: myTCP,
	}
}

// SetEventStream binds the playback to the local event stream.
func (p *PilotVoicePlayback) SetEventStream(es *sim.EventStream) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.events != nil {
		p.events.Unsubscribe()
	}
	p.events = es.Subscribe()
}

// SetMyTCP updates the local TCP after sign-on completes (in case it
// was unknown when the playback was first wired).
func (p *PilotVoicePlayback) SetMyTCP(tcp sim.TCP) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.myTCP = tcp
}

// Update drains pending RadioTransmissionEvents and asks the
// synthesize callback to render audio for each one not originating
// from the local controller. Call once per frame.
func (p *PilotVoicePlayback) Update() {
	p.mu.Lock()
	sub := p.events
	myTCP := p.myTCP
	syn := p.synthesize
	p.mu.Unlock()

	if sub == nil || syn == nil {
		return
	}
	for _, e := range sub.Get() {
		if e.Type != sim.RadioTransmissionEvent {
			continue
		}
		if e.ToController.TCP == myTCP {
			continue // requester's RPC-result path handles this one
		}
		syn(e.ADSBCallsign, e.RadioTransmissionType, e.SpokenText, "", e.PlayAt)
	}
}
```

(The `voice` argument is passed empty for now; observers don't have the voice name in the event today. Task 7 plumbs voice name into the event so observers get the same voice as the requester.)

- [ ] **Step 4: Wire `PilotVoicePlayback` into `ControlClient`**

In `client/client.go`, near the existing `peerVoice *PeerVoicePlayback` field on the `ControlClient` struct, add:

```go
pilotVoice *PilotVoicePlayback
```

In `ControlClient.GetUpdates` next to the existing `peerVoice` initialization (around the lazy-init block at line ~294):

```go
if c.peerVoice == nil {
	c.peerVoice = NewPeerVoicePlayback(c.lg)
}
c.peerVoice.SetEventStream(eventStream)

if c.pilotVoice == nil {
	c.pilotVoice = NewPilotVoicePlayback(c.lg, c.State.UserTCP())
	c.pilotVoice.synthesize = func(cs av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
		go c.synthesizeAndEnqueueObserved(cs, ty, text, voice, playAt)
	}
}
c.pilotVoice.SetMyTCP(c.State.UserTCP()) // re-set in case it changed across reconnect
c.pilotVoice.SetEventStream(eventStream)
```

Then add `c.pilotVoice.Update()` to `updateSpeech`:

```go
func (c *ControlClient) updateSpeech(p platform.Platform) {
	c.transmissions.Update(p, c.State.Paused, c.sttActive, c.State.SimTime, c.State.TCWDisplay)
	if c.peerVoice != nil {
		c.peerVoice.Update(p)
	}
	if c.pilotVoice != nil {
		c.pilotVoice.Update()
	}
}
```

Note `c.transmissions.Update` already grew new arguments — those land in Task 9. For now keep its existing call shape.

- [ ] **Step 5: Add `State.UserTCP()` helper**

If `c.State.UserTCP()` does not yet exist, add it. Locate the `SimState` struct (likely `server/manager.go` or `client/state.go`) and add:

```go
// UserTCP returns the primary TCP for the local controller, or empty
// if not signed on yet.
func (s *SimState) UserTCP() sim.TCP {
	for _, tcp := range s.PrimaryTCPs() {
		return tcp
	}
	return ""
}
```

If a similar accessor already exists (e.g., `s.PrimaryTCP()`), use that.

```bash
grep -n "PrimaryTCP\|UserTCP" client/*.go server/*.go sim/*.go | grep -v _test.go
```

- [ ] **Step 6: Add `synthesizeAndEnqueueObserved`**

In `client/client.go`, alongside `synthesizeAndEnqueueReadback` and `synthesizeAndEnqueueContact`, add a unified observer helper:

```go
// synthesizeAndEnqueueObserved synthesizes pilot TTS for an event that
// originated on another controller's command (observed via the event
// stream). Differs from synthesizeAndEnqueueReadback in that no Hold()
// is acquired — observers don't issue commands so there's no STT hold
// to release. Voice is left to local-default selection until the
// server stamps it on the event (Task 7).
func (c *ControlClient) synthesizeAndEnqueueObserved(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
	if *c.disableTTSPtr {
		return
	}
	radioSeed := uint32(util.HashString64(string(callsign)))
	pcm, err := tts.SynthesizeReadbackTTS(text, voice, radioSeed)
	if err != nil || pcm == nil {
		if err != nil {
			c.lg.Errorf("Observed TTS synth error for %s: %v", callsign, err)
		}
		return
	}
	if ty == av.RadioTransmissionContact {
		c.transmissions.EnqueueTransmissionPCM(callsign, ty, pcm, playAt)
	} else {
		c.transmissions.EnqueueReadbackPCM(callsign, ty, pcm, playAt)
	}
}
```

`EnqueueReadbackPCM` and `EnqueueTransmissionPCM` get a new `playAt` argument here — that's added in Task 9. To compile this task in isolation, temporarily call them without the new arg and accept the test failure; **OR** complete Task 9 in tandem. Recommendation: complete Task 9 immediately after this step rather than commit a half-broken state.

For the moment, comment out the synth call:

```go
_ = pcm
_ = playAt
// TM enqueue calls land in Task 9 once EnqueueReadbackPCM/EnqueueTransmissionPCM accept playAt.
```

- [ ] **Step 7: Run the new tests**

Run: `go test ./client -run "TestPilotVoicePlayback_" -v`
Expected: PASS.

- [ ] **Step 8: Build**

Run: `go build -tags vulkan ./...`
Expected: exit 0.

- [ ] **Step 9: Commit**

```bash
git add client/voice.go client/voice_test.go client/client.go
git commit -m "$(cat <<'EOF'
client: add PilotVoicePlayback for observer-side TTS

Today only the requesting client renders pilot TTS (via the
RunAircraftCommands RPC result). Observers on the same TCW saw the
text in the Messages pane but heard nothing. PilotVoicePlayback
subscribes to the local event stream and synthesizes audio for
RadioTransmissionEvents that did not originate from the local
controller. Wired into ControlClient.GetUpdates next to the existing
PeerVoicePlayback. Enqueue plumbing follows in a later task.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: Plumb voice name through the event for observer parity

**Why:** Observers should hear the same voice as the requester. Today the voice name is computed inside the dispatcher (`c.sim.VoiceAssigner.GetVoice`) and only flows back via the RPC result. To hand it to observers, it must ride on the event.

**Files:**
- Modify: `sim/eventstream.go` — add `SpokenVoice string` to Event.
- Modify: `sim/radio.go` — accept and stamp `voice` in `postRadioTransmission`.
- Modify: `client/voice.go` — `synthesize` callback uses `e.SpokenVoice`.

- [ ] **Step 1: Write failing test**

In `sim/eventstream_test.go`:

```go
func TestEvent_SpokenVoiceRoundTrip(t *testing.T) {
	e := Event{
		Type:        RadioTransmissionEvent,
		SpokenVoice: "am_adam",
	}
	if e.SpokenVoice != "am_adam" {
		t.Errorf("SpokenVoice not preserved")
	}
}
```

- [ ] **Step 2: Verify fail, add field, verify pass**

Run: `go test ./sim -run TestEvent_SpokenVoiceRoundTrip -v` — expect FAIL.

In `sim/eventstream.go`, add `SpokenVoice string` to the `Event` struct (next to `SpokenText`).

Run again — expect PASS.

- [ ] **Step 3: Update `postRadioTransmission` callers to set `SpokenVoice`**

The voice for a callsign comes from `s.VoiceAssigner.GetVoice(callsign, s.Rand)` (per `server/dispatcher.go:689`). The sim has a `VoiceAssigner`; let `postReadbackTransmission` and the in-line poster look it up:

```go
e.SpokenVoice = s.VoiceAssigner.GetVoice(e.ADSBCallsign, s.Rand)
```

Insert in both posting sites in `sim/radio.go` before `s.postRadioTransmission(e)`. (`postRadioTransmission` itself can do this lookup if `e.SpokenVoice == ""`, simplifying both call sites — let it.)

```go
func (s *Sim) postRadioTransmission(e Event) Time {
	s.mu.Lock(s.lg)
	defer s.mu.Unlock(s.lg)

	if e.SpokenVoice == "" && s.VoiceAssigner != nil {
		e.SpokenVoice = s.VoiceAssigner.GetVoice(e.ADSBCallsign, s.Rand)
	}
	d := s.EnsureTCWDisplay(e.DestinationTCW)
	// ... rest unchanged
}
```

- [ ] **Step 4: Update `PilotVoicePlayback.Update` to use `e.SpokenVoice`**

Replace `syn(..., "", e.PlayAt)` with `syn(e.ADSBCallsign, e.RadioTransmissionType, e.SpokenText, e.SpokenVoice, e.PlayAt)`.

- [ ] **Step 5: Run all sim and client tests**

Run: `go test ./sim/... ./client/...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add sim/eventstream.go sim/eventstream_test.go sim/radio.go client/voice.go
git commit -m "$(cat <<'EOF'
sim: stamp voice name on RadioTransmissionEvent

Adds SpokenVoice string on Event; postRadioTransmission fills it from
VoiceAssigner if the caller left it empty. Lets observer-side
PilotVoicePlayback render the same voice as the requester's
RPC-result-driven synthesis without an extra round-trip.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 8: Plumb `PlayAt` through the requester's RPC result path

**Files:**
- Modify: `server/dispatcher.go` — add `ReadbackPlayAt sim.Time` and `ContactPlayAt sim.Time` to result types; populate from sim functions.
- Modify: `sim/sim.go` — `PilotMixUp` and `RunAircraftControlCommands` return `PlayAt` alongside text.
- Modify: `sim/radio.go` — the in-line poster at `radio.go:505` now returns `PlayAt` to its caller.
- Modify: `client/control.go` — pass `PlayAt` to `synthesizeAndEnqueue*`.
- Modify: `client/client.go` — `synthesizeAndEnqueueReadback` and `synthesizeAndEnqueueContact` accept `playAt` and pass it through.

- [ ] **Step 1: Add fields to result types**

In `server/dispatcher.go` `AircraftCommandsResult`:

```go
type AircraftCommandsResult struct {
	ErrorMessage      string
	RemainingInput    string
	ReadbackText      string
	ReadbackVoiceName string
	ReadbackCallsign  av.ADSBCallsign
	ReadbackPlayAt    sim.Time
}
```

In `RequestContactResult` similarly:

```go
type RequestContactResult struct {
	// ... existing fields ...
	ContactPlayAt sim.Time
}
```

```bash
grep -n "type RequestContactResult struct" server/dispatcher.go
```
to find and edit the struct.

- [ ] **Step 2: Have sim functions return `PlayAt`**

`sim.PilotMixUp` currently returns `(string, error)`. Change to `(string, Time, error)` (in-package — `Time`, not `sim.Time`). Inside the function, capture the `Time` returned by `postRadioTransmission` (Task 3 already added that return value) and return it.

Same for `RunAircraftControlCommands`'s result — add `ReadbackPlayAt Time` to the result struct (likely `RunAircraftControlCommandsResult`).

```bash
grep -n "func.*PilotMixUp\|func.*RunAircraftControlCommands" sim/*.go
```

For each function: locate where `postRadioTransmission` is called (or `postReadbackTransmission`), capture the returned `Time`, propagate.

- [ ] **Step 3: Have dispatcher propagate**

In `server/dispatcher.go`'s `RunAircraftCommands` handler, where it sets `result.ReadbackText` etc., also set `result.ReadbackPlayAt`:

```go
if cmds.Multiple {
	spokenText, playAt, err := c.sim.PilotMixUp(c.tcw, callsign)
	if err != nil { rewriteError(err) }
	if cmds.EnableTTS && spokenText != "" {
		result.ReadbackText = spokenText
		result.ReadbackVoiceName = c.sim.VoiceAssigner.GetVoice(callsign, c.sim.Rand)
		result.ReadbackCallsign = callsign
		result.ReadbackPlayAt = playAt
	}
	return nil
}
```

Apply the same edit at the second call site (around line 716-721) for `RunAircraftControlCommands`.

For `RequestContactTransmission` (around line 1057): the contact-text producer must also return `PlayAt`. Find where contact transmissions are posted, plumb `PlayAt` through.

- [ ] **Step 4: Have client synthesize functions accept and pass `PlayAt`**

In `client/client.go`:

```go
func (c *ControlClient) synthesizeAndEnqueueReadback(callsign av.ADSBCallsign, text, voice string, playAt sim.Time) {
	radioSeed := uint32(util.HashString64(string(callsign)))
	if pcm, err := tts.SynthesizeReadbackTTS(text, voice, radioSeed); err != nil {
		c.lg.Errorf("TTS synthesis error for %s: %v", callsign, err)
		c.transmissions.Unhold()
	} else if pcm == nil {
		c.transmissions.Unhold()
	} else {
		c.lg.Infof("Synthesized readback for %s: %q (%d samples)", callsign, text, len(pcm))
		c.transmissions.EnqueueReadbackPCM(callsign, av.RadioTransmissionReadback, pcm, playAt)
	}
}

func (c *ControlClient) synthesizeAndEnqueueContact(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
	radioSeed := uint32(util.HashString64(string(callsign)))
	if pcm, err := tts.SynthesizeContactTTS(text, voice, radioSeed); err != nil {
		c.lg.Errorf("TTS synthesis error for %s: %v", callsign, err)
	} else if pcm != nil {
		c.lg.Infof("Synthesized contact for %s: %q (%d samples)", callsign, text, len(pcm))
		c.transmissions.EnqueueTransmissionPCM(callsign, ty, pcm, playAt)
	}
	c.transmissions.SetContactRequested(false)
}
```

The Enqueue methods grow `playAt sim.Time` — that lands in Task 9.

- [ ] **Step 5: Update call sites in `client/control.go`**

Around line 478:

```go
go c.synthesizeAndEnqueueReadback(result.ReadbackCallsign, result.ReadbackText, result.ReadbackVoiceName, result.ReadbackPlayAt)
```

Around line 525:

```go
go c.synthesizeAndEnqueueContact(result.ContactCallsign, result.ContactType,
	result.ContactText, result.ContactVoiceName, result.ContactPlayAt)
```

- [ ] **Step 6: Build (will fail until Task 9 lands the new Enqueue signatures)**

Run: `go build -tags vulkan ./...`
Expected: FAIL — `EnqueueReadbackPCM does not take 4 arguments`. Continue immediately into Task 9 in the same session.

- [ ] **Step 7: Skip commit until Task 9 lands**

Don't commit yet — the tree won't compile. Both changes are committed together at the end of Task 9.

---

### Task 9: `TransmissionManager` refactor — sim.Time, shared hold, per-entry `PlayAt`; remove dead methods

**Files:**
- Modify: `client/stt.go`
- Modify: `client/client.go` — `updateSpeech` passes new args.
- Modify: `client/control.go` — remove `HoldAfterSilentContact` call.
- Test: `client/stt_test.go` (new)

- [ ] **Step 1: Write failing tests**

Create `client/stt_test.go`:

```go
// client/stt_test.go
package client

import (
	"testing"
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"
)

func TestTransmissionManager_GatesOnRadioHoldUntil(t *testing.T) {
	tm := NewTransmissionManager(nil)
	tm.EnqueueReadbackPCM("AAL123", av.RadioTransmissionReadback, []int16{1, 2, 3}, sim.Time{})

	now := sim.NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	tcw := &sim.TCWDisplayState{RadioHoldUntil: now.Add(5 * time.Second)} // hold in future

	plat := &fakePlatform{}
	tm.Update(plat, false /*paused*/, false /*sttActive*/, now, tcw)

	if plat.enqueueCalls != 0 {
		t.Errorf("playback should be gated by RadioHoldUntil; got %d enqueue calls", plat.enqueueCalls)
	}
}

func TestTransmissionManager_DefersUntilPlayAt(t *testing.T) {
	tm := NewTransmissionManager(nil)
	now := sim.NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	tm.EnqueueReadbackPCM("AAL123", av.RadioTransmissionReadback, []int16{1, 2, 3}, now.Add(5*time.Second))

	plat := &fakePlatform{}
	tcw := &sim.TCWDisplayState{} // hold not in future
	tm.Update(plat, false, false, now, tcw)

	if plat.enqueueCalls != 0 {
		t.Errorf("playback should defer until PlayAt; got %d enqueue calls", plat.enqueueCalls)
	}

	// Advance sim-time past PlayAt.
	tm.Update(plat, false, false, now.Add(10*time.Second), tcw)
	if plat.enqueueCalls != 1 {
		t.Errorf("playback should fire once SimTime > PlayAt; got %d", plat.enqueueCalls)
	}
}

// fakePlatform implements the subset of platform.Platform that
// TransmissionManager.Update uses.
type fakePlatform struct {
	enqueueCalls int
}

func (f *fakePlatform) TryEnqueueSpeechPCM(pcm []int16, done func()) error {
	f.enqueueCalls++
	if done != nil {
		done()
	}
	return nil
}
```

(If the actual platform interface has more methods that `TransmissionManager.Update` calls, extend `fakePlatform` accordingly. Look at how `Update` uses the platform parameter.)

- [ ] **Step 2: Refactor `TransmissionManager`**

In `client/stt.go`:

a. Update `queuedTransmission`:

```go
type queuedTransmission struct {
	Callsign av.ADSBCallsign
	Type     av.RadioTransmissionType
	PCM      []int16
	PlayAt   sim.Time
}
```

(Drop `PTTReleaseTime` — no longer used.)

b. Remove the local `holdUntil time.Time` field from `TransmissionManager`. Keep `holdCount` (still needed for STT-processing holds).

c. Update `EnqueueReadbackPCM`:

```go
func (tm *TransmissionManager) EnqueueReadbackPCM(callsign av.ADSBCallsign, ty av.RadioTransmissionType, pcm []int16, playAt sim.Time) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if tm.holdCount > 0 {
		tm.holdCount--
	}
	if len(pcm) == 0 {
		tm.lg.Warnf("Skipping readback for %s due to empty PCM", callsign)
		return
	}
	tm.queue = slices.DeleteFunc(tm.queue, func(qt queuedTransmission) bool {
		return qt.Callsign == callsign && qt.Type == av.RadioTransmissionContact
	})
	qt := queuedTransmission{Callsign: callsign, Type: ty, PCM: pcm, PlayAt: playAt}
	tm.queue = append([]queuedTransmission{qt}, tm.queue...)
}
```

d. Update `EnqueueTransmissionPCM`:

```go
func (tm *TransmissionManager) EnqueueTransmissionPCM(callsign av.ADSBCallsign, ty av.RadioTransmissionType, pcm []int16, playAt sim.Time) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if len(pcm) == 0 {
		tm.lg.Warnf("Skipping transmission for %s due to empty PCM", callsign)
		return
	}
	qt := queuedTransmission{Callsign: callsign, Type: ty, PCM: pcm, PlayAt: playAt}
	tm.queue = append(tm.queue, qt)
}
```

e. Update `Update` to accept new args and use shared state:

```go
func (tm *TransmissionManager) Update(p platform.Platform, paused, sttActive bool, simTime sim.Time, tcw *sim.TCWDisplayState) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	if paused || sttActive {
		return
	}
	if tm.holdCount > 0 {
		return
	}
	// Shared TCW radio bus: any active TX or post-event quiet on this TCW pauses playback.
	if tcw != nil && simTime.Before(tcw.RadioHoldUntil) {
		return
	}
	if tm.playing || len(tm.queue) == 0 {
		return
	}
	// Front-of-queue PlayAt gating.
	if simTime.Before(tm.queue[0].PlayAt) {
		return
	}

	qt := tm.queue[0]
	tm.queue = tm.queue[1:]

	finishedCallback := func() {
		tm.mu.Lock()
		defer tm.mu.Unlock()
		tm.playing = false
		tm.lastCallsign = qt.Callsign
		tm.lastWasContact = qt.Type == av.RadioTransmissionContact
	}

	if err := p.TryEnqueueSpeechPCM(qt.PCM, finishedCallback); err == nil {
		tm.playing = true
	}
}
```

f. **Delete** `HoldAfterTransmission` and `HoldAfterSilentContact` entirely.

- [ ] **Step 3: Update call sites in `client/client.go` and `client/control.go`**

In `client/client.go`, find:

```go
func (c *ControlClient) HoldRadioTransmissions() {
	c.transmissions.HoldAfterTransmission()
}
```

Delete this method, since it now has nothing to call. Then grep for `HoldRadioTransmissions` repo-wide and remove the call sites:

```bash
grep -rn "HoldRadioTransmissions\|HoldAfterTransmission" --include="*.go"
```

Expected post-fix: zero matches.

In `client/control.go` line 518:

```go
if *c.disableTTSPtr {
	c.transmissions.SetContactRequested(false)
	c.transmissions.HoldAfterSilentContact(result.ContactCallsign)  // DELETE this line
	return
}
```

Delete the `HoldAfterSilentContact` call. Comment can be deleted too.

In `client/client.go` `updateSpeech`, update the call:

```go
func (c *ControlClient) updateSpeech(p platform.Platform) {
	c.transmissions.Update(p, c.State.Paused, c.sttActive, c.State.SimTime, c.State.TCWDisplay)
	if c.peerVoice != nil {
		c.peerVoice.Update(p)
	}
	if c.pilotVoice != nil {
		c.pilotVoice.Update()
	}
}
```

- [ ] **Step 4: Re-enable the `synthesizeAndEnqueueObserved` body left commented in Task 6**

In `client/client.go`:

```go
func (c *ControlClient) synthesizeAndEnqueueObserved(callsign av.ADSBCallsign, ty av.RadioTransmissionType, text, voice string, playAt sim.Time) {
	if *c.disableTTSPtr {
		return
	}
	radioSeed := uint32(util.HashString64(string(callsign)))
	pcm, err := tts.SynthesizeReadbackTTS(text, voice, radioSeed)
	if err != nil || pcm == nil {
		if err != nil {
			c.lg.Errorf("Observed TTS synth error for %s: %v", callsign, err)
		}
		return
	}
	if ty == av.RadioTransmissionContact {
		c.transmissions.EnqueueTransmissionPCM(callsign, ty, pcm, playAt)
	} else {
		c.transmissions.EnqueueReadbackPCM(callsign, ty, pcm, playAt)
	}
}
```

- [ ] **Step 5: Build the entire tree**

Run: `go build -tags vulkan ./...`
Expected: exit 0.

- [ ] **Step 6: Run tests**

Run: `go test ./client/... ./sim/...`
Expected: PASS.

- [ ] **Step 7: Commit (all of Task 8 + Task 9 together)**

```bash
git add server/dispatcher.go sim/sim.go sim/radio.go client/client.go client/control.go client/stt.go client/stt_test.go
git commit -m "$(cat <<'EOF'
client+server: TM consumes shared RadioHoldUntil and per-entry PlayAt

TransmissionManager.Update accepts SimTime + TCWDisplayState, gates on
the shared RadioHoldUntil instead of the per-instance holdUntil, and
defers each queue entry until SimTime >= PlayAt. PlayAt rides on the
queue entry from EnqueueReadbackPCM/EnqueueTransmissionPCM, which now
take it as an argument.

RPC results carry ReadbackPlayAt and ContactPlayAt; sim functions
return the PlayAt from postRadioTransmission and the dispatcher
propagates it to clients. synthesizeAndEnqueueReadback and
synthesizeAndEnqueueContact accept it; synthesizeAndEnqueueObserved
(the new observer-side path) uses it for same-TCW peers.

Removes HoldAfterTransmission, HoldAfterSilentContact, the local
post-event constants, and the per-TM holdUntil field. Single source
of truth for radio-bus timing now lives in sim/radio.go and
sim/voice.go.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: Manual end-to-end verification

**Files:** none (this is a hand-off task that requires Phase 0's voice-relay bug to be fixed first).

- [ ] **Step 1: Confirm Phase 0 has been resolved**

The user must have run the Phase 0 diagnostic, identified the controller-voice-relay bug, applied a fix in a follow-up plan, and verified that two real machines can hear each other's PTT. If that hasn't happened, do not proceed to manual testing — Phase 1 is correct in unit/integration tests but cannot be confirmed end-to-end without working PTT.

- [ ] **Step 2: Run the manual test matrix from the spec**

Server side: `./vice.exe -runserver -tags vulkan` (rebuild first with `go build -tags vulkan -o vice.exe ./cmd/vice`).

Client side: both controllers `./vice.exe -server <tailscale-ip>:8016`.

Test matrix (each row exercises a different code path):

1. **Both audio-enabled, same TCW.** Pilot transmits a readback. Both controllers hear the audio start within ~200ms of each other; the same voice (deterministic per callsign).
2. **A audio-enabled, B audio-disabled, same TCW.** A hears AI pilots; B sees text only. B's command pacing (the next-contact-eligibility holdUntil) matches A's silent intervals.
3. **Both press PTT in quick succession.** Arbitration grants one; the other gets a heterodyne tone. Pilot transmissions queued behind both.
4. **A presses PTT mid-pilot-transmission on B.** B's playback pauses for A's PTT, resumes after A releases.
5. **One client disconnects mid-PTT.** `ClearTalkerForToken` fires server-side; `RadioHoldUntil` shrinks to `+2s`; peers resume playback after the cooldown.

- [ ] **Step 3: Report outcomes to user**

Document any behavior that diverges from expectation. If a regression is found, file as a follow-up plan; do not patch in this plan.

---

## Self-Review Checklist

After implementing all tasks above, the agent should verify:

1. `grep -rn "ScopeSyncEnabled\|holdUntil time.Time\|HoldAfterTransmission\|HoldAfterSilentContact" --include="*.go"` returns zero matches.
2. `go test ./...` passes.
3. `go build -tags vulkan ./...` succeeds.
4. The five-row manual test matrix in Task 10 completes successfully (pending Phase 0 fix).
