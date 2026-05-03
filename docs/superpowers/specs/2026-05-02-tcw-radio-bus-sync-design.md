# TCW Radio Bus Sync — Design

**Date:** 2026-05-02
**Branch base:** `shared-tcw-display`
**Status:** design approved; implementation plan pending.

## Summary

Treat each TCW as the authoritative "radio bus." Hold-state and per-transmission start times live on `sim.TCWDisplayState` (server-authoritative) and reach clients through the same poll-based snapshot that already syncs scope prefs and track annotations. Each client's `TransmissionManager` becomes a passive consumer: it reads "when may I dequeue?" and "when does this item start?" from the shared state and respects them.

Scope sync today gives same-TCW reliefs identical scopes. This spec extends the same-TCW illusion to the audio path: same pilot voices, same start times, same silence windows.

## Problem

Today, two relief controllers on the same TCW receive the same `RadioTransmissionEvent` stream but render audio independently:

- Each client locally synthesizes pilot TTS via `tts.SynthesizeReadbackTTS` from the server's `SpokenText`. Voice selection is deterministic per callsign-hash, so PCM bytes are essentially identical — but timing is not.
- Each client's `TransmissionManager` owns its own `holdUntil` / `holdCount`, so post-readback (3s), post-contact (8s), and post-PTT (2s) holds drift apart whenever any client gets a hold the others didn't.
- A controller's PTT to peers (the existing `PeerVoiceEvent` relay) has no effect on peer `TransmissionManager` queues, so AI pilot audio can play over a human controller's transmission on the listener's side.
- A TTS-disabled relief calls `HoldAfterSilentContact` to fake duration — but only locally. The audible peer drifts independently.

The result: same words, same voice, audibly out-of-sync; controller voice and pilot voice can collide on the listener's side.

## Architecture

Three deltas. No new RPCs. No server-side TTS. No raw audio for AI pilots over the wire.

1. New field on `sim.TCWDisplayState`:
   ```go
   // RadioHoldUntil is the sim-time before which the TCW radio is busy
   // or in post-event quiet. All TransmissionManagers at this TCW pause
   // playback while SimTime < RadioHoldUntil. Source-agnostic: pilot
   // transmissions, controller PTTs, and post-event holds all write here.
   RadioHoldUntil sim.Time
   ```

2. New field on `sim.Event` (only meaningful for `RadioTransmissionEvent`):
   ```go
   // PlayAt is the sim-time when listening clients should start audio
   // playback. Server stamps this when the event is queued. Late-arriving
   // clients (SimTime > PlayAt) play immediately without drop.
   PlayAt sim.Time
   ```

3. Three server-side write paths into `RadioHoldUntil`, all keyed by **destination TCW**.

`RadioHoldUntil` and `PlayAt` ride into clients on the existing `SimStateUpdate` poll. No new transport.

### Why sim-time, not wall-clock

Sim-time is already streamed in every `SimStateUpdate` and is the canonical reference between server and all clients. Using it sidesteps NTP / clock-skew between machines. Drift is bounded by poll cadence (≤1s; typically <300ms over LAN/Tailscale), and a sub-second start skew is inaudible against multi-second pilot transmissions.

### Why one `RadioHoldUntil`, not separate flags per source

The `TransmissionManager` only cares "may I dequeue?" The answer collapses to "is `SimTime` past whatever event was last on the air?" Each writer applies `max(current, eventEnd)`, which is monotonic and race-free. Mirrors today's local `holdUntil` field, just shared.

## Server-side write paths

All three writers operate on the destination TCW.

### A. Pilot transmission queued

Where the sim emits a `RadioTransmissionEvent` for TCW X (today: `sim/radio.go`):

1. Read current `RadioHoldUntil` for X.
2. `PlayAt = max(SimTime + 200ms, RadioHoldUntil)` — small forward buffer so listeners receive the event before its scheduled start.
3. Estimate spoken duration from `len(SpokenText)` at ~70ms/char (calibrated to ATC speech rate of ~150 wpm).
4. `endTime = PlayAt + spokenDuration + postEventPad` where `postEventPad` is 3s for `RadioTransmissionReadback` and 8s for `RadioTransmissionContact`.
5. `RadioHoldUntil = max(RadioHoldUntil, endTime)`.
6. Stamp `event.PlayAt = PlayAt` before posting to the event stream.

Back-to-back transmissions queue cleanly: each `PlayAt` anchors to the previous event's `endTime`.

### B. Controller PTT start

In `Sim.StartPTT` (sim/voice.go) on grant:

- `RadioHoldUntil = max(RadioHoldUntil, SimTime + 60s)`.

Generous upper bound so pilot transmissions stay parked while the human talks. We deliberately do not re-extend on every audio chunk — that would write the locked state at audio rates. The 60s cap is large enough for any plausible PTT and is narrowed at release.

### C. Controller PTT release or disconnect

In `Sim.StopPTT` and `Sim.ClearTalkerForToken`:

- `RadioHoldUntil = SimTime + 2s` — replaces the 60s upper bound with the standard post-PTT cooldown.

### Why text-length duration estimation, not client roundtrip

Client TTS engines may differ in render speed; round-tripping actual duration adds latency and a fragile dependency. ~150 wpm is well-established for ATC speech; per-character estimate hits within ±300ms of true duration. The 3s/8s post-event pad masks any sub-second underestimate.

## Client-side consumption

The `TransmissionManager` (`client/stt.go`) becomes a passive consumer.

### a. Replace local `holdUntil` reads with shared `RadioHoldUntil`

Today (`stt.go:142`):
```go
if time.Now().Before(tm.holdUntil) { return }
```
After:
```go
if simTime < state.TCWDisplay.RadioHoldUntil { return }
```

The local `holdUntil` field is removed. The 3s/8s/2s constants likewise — the server already encodes those into the shared timestamp.

### b. Defer playback until `PlayAt`

When the TM is about to dequeue:
```go
if simTime < qt.PlayAt { return }   // not time yet; check again next Update tick
```
Late arrivals (`SimTime > PlayAt`) play immediately — no drop. `Update` runs every frame (~16ms at 60fps), well below audible threshold.

### c. `PlayAt` rides into the queue with the PCM

`EnqueueReadbackPCM` and `EnqueueTransmissionPCM` get an extra `playAt sim.Time` argument plumbed from the `RadioTransmissionEvent` into the queue entry struct.

### d. TTS-disabled side stays in lockstep

When TTS is disabled, the client today calls `HoldAfterSilentContact` to fake duration. With shared holds, this becomes a no-op — the TM is already pausing on `RadioHoldUntil`, which the server has already advanced. The TTS-disabled side is silent but its "next contact can fire" timestamp stays aligned with the audible side. The local `HoldAfterSilentContact` path is removed entirely.

### e. Local `HoldAfterTransmission` (post-PTT 2s) is removed

Today, when the user releases their own PTT, their TM holds locally for 2s. Under this design, the same 2s now comes from the server's `StopPTT` write to `RadioHoldUntil`, which fans out to all same-TCW peers — including the talker. The local hold is redundant.

### Net effect on `TransmissionManager`

Removed (~30 lines): `holdUntil`, `HoldAfterTransmission`, `HoldAfterSilentContact`, the 3s/8s/2s constants.
Added (~5 lines): `simTime < state.TCWDisplay.RadioHoldUntil` gate; `simTime < qt.PlayAt` defer check; `PlayAt` field on the queue entry.

## Phase 0 — Fix the existing controller voice relay (prerequisite)

The shared-hold logic in this design depends on `StartPTT` / `StopPTT` actually firing on the server when a controller presses PTT. The integration tests pass, but real two-machine testing produces silence — neither peer hears the other. Until that's working, the radio-bus sync can't be tested end-to-end.

**Scope:** find why the existing `PeerVoiceEvent` relay fails in production with two real clients on Tailscale, and fix it. **Not** rewrite the relay — only debug and patch.

**Diagnostic plan:**

1. Add temporary structured logs at four points (similar to the `DBG_SYNC` lines used in the live debug session):
   - Server `StartPTT`: log `tcw, token, granted/denied`.
   - Server `StreamPTTAudio`: log `tcw, token, samples`, sampled (every Nth chunk) to avoid flood.
   - Server `prepareRadioTransmissions` filter: when a `PeerVoiceEvent` is dropped, log why (`SourceTCW != tcw` vs `SenderToken == token`).
   - Client `PeerVoicePlayback.Update`: log `chunks_drained, total_samples_appended` per frame.
2. Reproduce with two real machines on Tailscale, both on `shared-tcw-display` HEAD.
3. Read the logs to identify which stage breaks.
4. Patch.
5. Remove the diagnostic logs.

**Likely culprits, in order of probability:**

- A peer's vice binary is built from an older commit on `shared-tcw-display` and missing some of the relay wiring.
- Same-TCW filter mistakenly comparing `SourceTCW` to listener's TCW value when relief inherited a different TCW from the connect dialog than the primary.
- `AppendSpeechPCM` getting called but the platform's audio device is busy or muted.

**Exit criteria:** with two clients on two machines on the same TCW, controller A presses PTT and speaks; controller B hears A's voice. Both directions verified.

## Cleanup (no dead code)

Removed as part of this work — both already-deprecated and newly-redundant:

**Already-deprecated:**
- `TCWDisplayState.ScopeSyncEnabled bool` (sim/tcw_display.go:64). Comment says "kept for gob compat until a later cleanup." This is that cleanup. Gob is forgiving of removed fields, so peers on slightly different builds still decode.

**Made dead by this design:**
- `TransmissionManager.holdUntil time.Time` field — replaced by reading `state.TCWDisplay.RadioHoldUntil`.
- `TransmissionManager.HoldAfterTransmission()` method — replaced by server's `StopPTT` write.
- `TransmissionManager.HoldAfterSilentContact()` method — redundant; shared timestamp covers TTS-disabled clients.
- The local 3s/8s/2s post-event constants in `client/stt.go` — moved into `sim/radio.go` next to the writer that computes `RadioHoldUntil`. Single source of truth.
- All call sites of the three removed methods (grep `HoldAfterTransmission`, `HoldAfterSilentContact` and prune).

**Kept (looks similar but not dead):**
- `TransmissionManager.Hold()` / `Unhold()` / `holdCount` — gate playback during local STT processing while the local STT engine returns a transcript. Per-client concern, not subsumed by the shared timestamp.
- `synthesizeAndEnqueue{Readback,Contact}` — local TTS rendering still happens.

## Testing plan

### Unit tests (sim package)

- `RadioHoldUntil` is set correctly by each writer (`prepareRadioTransmissions`, `StartPTT`, `StopPTT`, `ClearTalkerForToken`).
- `PlayAt` is monotonically non-decreasing across back-to-back transmissions for the same TCW.
- Different TCWs have independent `RadioHoldUntil` values.
- Late-arriving events (`SimTime > PlayAt`) play immediately, not dropped.

### Integration tests (sim package)

Following the pattern of `sim/voice_integration_test.go`:
- Two clients same TCW: a pilot transmission's `PlayAt` is identical on both clients' state-update payloads.
- Two clients same TCW: controller A's `StartPTT` causes B's subsequent state-update to carry an extended `RadioHoldUntil`.
- Two clients different TCWs: A's pilot transmissions don't move B's `RadioHoldUntil`.

### Client-side unit tests (`stt_test.go`)

- TM with a queue entry whose `PlayAt` is in the future: `Update` returns without dequeuing.
- TM where `RadioHoldUntil` has passed: queue resumes.
- TM with TTS disabled: `RadioHoldUntil` still gates queue; `HoldAfterSilentContact` calls absent (path removed).

### Manual test matrix (two real machines, same TCW)

- Both audio-enabled: both hear AI pilot audio start within ~200ms of each other; voices match.
- A audio-enabled, B audio-disabled: A hears AI pilots; B sees text; B's command pacing (next contact eligibility) matches.
- Both PTT in quick succession: arbitration grants one, other gets heterodyne tone; pilot transmissions queued behind both.
- A PTTs while AI pilot transmission is mid-play on B: B's playback pauses for A's PTT, resumes after release.
- One client disconnects mid-PTT: `ClearTalkerForToken` runs, `RadioHoldUntil` shrinks to `+2s`, peers resume.

## Edge cases

- **Client receives event after `PlayAt` already passed** → play immediately; no drop.
- **Server has no observers (single-controller TCW)** → state still updates; cost is one extra timestamp field.
- **Server restart / sim reload** → `TCWDisplayState` reseeds; `RadioHoldUntil` resets to zero; in-flight transmissions are not recovered (acceptable; rare).
- **Cross-TCW pilot transmission** → only the destination TCW's `RadioHoldUntil` advances.
- **Duration estimate too short** → `postEventPad` (3s/8s) absorbs any underestimate.
- **Duration estimate too long** → next transmission's `PlayAt` is pushed back, slight excess silence between transmissions. Acceptable.

## Non-goals

- Server-side TTS rendering. Pilot audio stays per-client.
- Sample-accurate audio sync between clients. Sub-100ms drift is acceptable.
- Cross-TCW audio sync. Different TCWs are different radios.
- Bit-perfect text/audio cross-medium sync. Text appears at event-arrival time; audio at `PlayAt`. Difference is ≤200ms, below the threshold of feeling weird.
- `ScopeSyncEnabled` is deleted; it was already unused.
