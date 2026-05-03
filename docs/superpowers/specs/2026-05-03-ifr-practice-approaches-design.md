# IFR Practice Approaches — Design

**Date:** 2026-05-03
**Branch base:** `practice-approaches` (off `upstream/master @227c6d29`)
**Status:** design approved; implementation plan pending.

## Summary

Add scenario-author-driven IFR practice-approach traffic. An aircraft spawned as practice traffic flies its assigned approach, executes the published miss, gets vectored back around by the controller, flies the same approach again, and repeats N times before landing on the final approach. The pilot announces a preferred approach on initial contact and after every miss; on the final approach the pilot adds "this will be a full stop." Tower owns each landing attempt; approach owns the missed segment. No new RPCs. No new visual indicators. VFR practice approaches are out of scope for v1.

## Problem

Vice today models IFR arrivals as one-shot: an aircraft is cleared for an approach, lands, and is deleted. There is no way for an aircraft to fly multiple approaches at the same airport — neither for training scenarios (instructors drilling repeat customers) nor for the realistic real-world phenomenon of practice approaches.

The blockers in the current code are concentrated in three places:

- `sim/goaround.go:34-72` (`goAround()`): when an arrival goes missed today, it sets `WentAround = true`, gets handed to a "go-around controller," and is treated as a departure. There is no path back into arrival-land.
- `nav/approach.go:456-1046`: the approach state machine assumes a single clearance per aircraft. Once `Approach.Cleared = true` and the aircraft reaches the missed-approach point, it lands.
- `sim/spawn_arrivals.go:118` (`InitializeArrival`) and the `InboundFlow` config: scenarios have no field for "this aircraft is practice traffic with N misses planned."

## Architecture

Three deltas. No new RPCs, no new event types.

### Aircraft state (sim/aircraft.go)

```go
type Aircraft struct {
    // ...existing...
    MissedApproachesRemaining  int    // 0 = normal arrival; >0 = practice traffic; decrements per completed miss
    PracticeApproachID         string // av.Approach.Id the AI requests on every loop (e.g. "I22L"); empty for non-practice
    PracticeApproachController string // callsign of the approach controller to hand back to on miss; refreshed on every C<approach>
    PendingPracticeRequest     bool   // set true when the pilot owes a practice-approach request transmission; cleared when the transmission fires
}
```

**Counter semantics:** `MissedApproachesRemaining = N` means the AI will go missed exactly N times, then land on approach N+1. So `N=3` ⇒ 3 misses + 1 landing = 4 total approaches. The field decrements on each completed missed approach.

### Scenario config (server/scenario.go and InboundFlow)

```go
type PracticeApproachConfig struct {
    Probability         float32 // 0.0..1.0; per-spawn chance the aircraft is practice
    MinMissedApproaches int     // inclusive lower bound
    MaxMissedApproaches int     // inclusive upper bound
}

type InboundFlow struct {
    // ...existing...
    PracticeApproaches *PracticeApproachConfig // nil = no practice traffic from this flow
}
```

### Pilot transmission (sim/aircraft.go pending-transmission tag set)

```go
type PendingTransmissionPracticeApproachReq struct {
    ApproachID string // matches av.Approach.Id
    FullStop   bool   // true only on the final approach (MissedApproachesRemaining == 0 at queue time)
}
```

Reuses the existing `PendingTransmission*` machinery — same TTS pipeline as VFR flight-following requests.

## Spawning

In `sim/spawn_arrivals.go` (`InitializeArrival`), after the flight plan is set up, run:

```go
if flow.PracticeApproaches != nil && rand.Float32() < flow.PracticeApproaches.Probability {
    n := flow.PracticeApproaches.MinMissedApproaches
    spread := flow.PracticeApproaches.MaxMissedApproaches - n
    if spread > 0 {
        n += rand.Intn(spread + 1)
    }
    ac.MissedApproachesRemaining = n
    ac.PracticeApproachID = pickPracticeApproach(ac.FlightPlan.ArrivalAirport, scenarioActiveRunways)
}
```

`pickPracticeApproach(airport, activeRunways)` enumerates the published approaches in `av.Approach` data that match the inbound-flow's active arrival runways (the "ATIS-advertised" set), picks one at random, returns its `Id`. Returns `""` if no match — in which case the aircraft is silently demoted to a normal arrival (defensive; should not happen with sane scenario config).

### Scenario validation

Add a sanity check in the existing scenario-validator path. Reject loads with:
- `Probability` outside `[0, 1]`
- `MinMissedApproaches > MaxMissedApproaches`
- Either bound negative

Same place existing inbound-flow validation runs. Failure produces a clear error message naming the inbound-flow.

## Pilot request transmission

### Phraseology, fixed for v1

- **Misses** (`FullStop: false`): *"Approach, N123AB, request the ILS Runway 22 Left for the practice."*
- **Final** (`FullStop: true`): *"Approach, N123AB, request the ILS Runway 22 Left, this will be a full stop."*

The final-approach phrasing pre-answers the FAA Order 7110.65 controller question "how will the approach terminate?" — controller never has to ask in v1.

### Fire points (state-driven, never random)

**Fire #1 — initial contact with the approach controller.**
When the aircraft is first handed to the approach controller, the existing check-in transmission ("with you, [altitude]") fires. For practice traffic, the practice request is queued back-to-back with the check-in. Same TTS pipeline. The controller hears one continuous transmission ending in "...request the [approach] for the practice." `FullStop` is `(MissedApproachesRemaining == 0)` at queue time.

**Fire #2 — after each miss, on level-off.**
In `practiceMissedApproach()` (below), `PendingPracticeRequest` is set to `true`. The aircraft flies the published miss; once the AI has reached the missed-approach altitude and is wings-level on the miss segment, the pending flag triggers the transmission, then clears. Fires once per miss, deterministic, no re-fires from prolonged vectoring.

### Authority

The transmission is purely cosmetic. It does **not** set `Approach.Cleared`, alter flight rules, or otherwise mutate aircraft state. The user issues `C<approach>` (or any other clearance) normally; the AI complies with whatever the controller actually clears, regardless of what the pilot requested.

## The missed-approach loop

### Single decision point in `goAround()`

```go
func (s *Sim) goAround(ac *Aircraft) {
    if ac.MissedApproachesRemaining > 0 {
        s.practiceMissedApproach(ac)
        return
    }
    // ...existing goAround unchanged...
}
```

### Two routes into `goAround()` for practice aircraft

1. **Natural — MAP reached.** In `nav/approach.go`, when an approach-following aircraft reaches the missed-approach point, today's code transitions to landing. Add a check there: if `MissedApproachesRemaining > 0`, call `goAround()` instead of landing. The existing landing path is unchanged for `== 0`.
2. **User-induced.** A spacing-driven go-around (`GoAroundForSpacing`) on a practice aircraft mid-approach lands in the same `goAround()` and gets the practice branch. The miss still counts.

### `practiceMissedApproach()` body

```go
func (s *Sim) practiceMissedApproach(ac *Aircraft) {
    ac.MissedApproachesRemaining--                       // this miss counts

    ac.Nav.flyPublishedMiss()                            // fly published miss if Approach data
                                                         // includes one; otherwise reuse the
                                                         // existing go-around heading/altitude
                                                         // assignment path (current heading,
                                                         // climb to standard missed altitude)

    ac.Nav.Approach.Cleared = false                      // re-clearable
    ac.Nav.Approach.InterceptState = NotIntercepting
    ac.Nav.Approach.AssignedId = ""
    ac.Nav.Approach.Assigned = nil
    // ac.PracticeApproachID stays — pilot still wants the same approach next time

    s.handBackToApproachController(ac)                   // see Handoffs section

    ac.PendingPracticeRequest = true                     // triggers post-miss pilot transmission
                                                         // once the aircraft is level on the miss
}
```

`WentAround` is **not** set. That flag means "treat as departure" in the existing code, which is the opposite of what we want. Practice aircraft remain in arrival-land throughout.

### Published miss vs. fallback heading/altitude

Today's `Approach` data does not consistently include a published-miss waypoint segment — vice's existing `goAround()` already assigns a fallback heading + climb-to-altitude rather than flying a published miss procedure. `flyPublishedMiss()` follows the same convention: fly the published miss if the data is there, else reuse the existing go-around heading/altitude assignment logic. In v1 the fallback path will be the common case. Modeling published-miss procedures as first-class waypoint segments is a future-work item that would benefit normal go-arounds too — it is not part of this feature.

## Handoffs

The user agreed: tower owns each landing attempt, approach owns the missed segment, hand-back on miss, tower keeps the final landing.

### Stash on clearance

When the user issues `C<approach>` to a practice aircraft and the existing code hands the aircraft to tower, record the *handing-off* controller (the approach controller who cleared them):

```go
ac.PracticeApproachController = handingOffController.Callsign
```

Set on every `C<approach>` for practice aircraft, so it stays current across loops even if the user reassigns positions mid-session.

### Hand-back on miss

`handBackToApproachController(ac)` issues a handoff request from tower (current controlling controller) back to `ac.PracticeApproachController` using the existing `HandoffTrack` RPC. The receiving controller gets a normal incoming-handoff display; they accept; vectoring resumes. No new wire format.

### Final landing

When `MissedApproachesRemaining == 0` and the aircraft is on the approach, no practice-loop branch runs — the aircraft is on tower's frequency, lands normally, and the existing arrival cleanup deletes it. Tower never hands back. No special case.

### Edge case — controller signed off mid-loop

If the stashed `PracticeApproachController` has disconnected by the time the aircraft misses, the hand-back lands on the airspace's current owner via the existing fallback path the current `getGoAroundController` already uses.

## Cleanup & persistence

**Counter exhaustion** — the aircraft on its final approach takes the existing landing branch and is deleted by the existing arrival cleanup path. The new fields go away with it.

**No new cleanup code.** Practice aircraft live and die through the same arrival lifecycle as normal aircraft.

**Persistence.** The four new aircraft fields are part of the `Aircraft` struct already gob-encoded for sim save/restore and observer-state propagation. Adding fields to a gob struct is forward- and backward-compatible — older peers see zero values, newer peers read zero from older peers. No version migration, no defaults file.

**Pre-existing edges unaffected:**
- Aircraft sign-out / DM/DR delete: works as today.
- Sim pause/resume: state freezes and resumes correctly.

## Visual indication

None for v1. The controller infers practice traffic from the pilot transmissions ("request the ILS for the practice") and from the fact that the aircraft goes missed and asks for another. Scratchpad and FDB are unchanged.

## Quirk to be aware of

The `FullStop` flag is set at request-queue time, based on `MissedApproachesRemaining` at that instant. Consequences:

- A 1-approach aircraft (`Min/Max = 0`) — i.e., zero misses planned — fires its initial-contact transmission with `FullStop = true`. Correct: they are landing on the only approach.
- A 2-approach aircraft (`Min = 1, Max = 1`) — one miss planned — fires initial contact with `FullStop = false` ("low approach"), misses, decrements to 0, fires the post-miss request with `FullStop = true` ("full stop"). Correct.
- The post-miss request always reflects the upcoming approach's termination: at queue time the counter has just decremented, so the value is for the *next* approach to be flown. This is the desired semantics — the pilot is announcing what they intend to do on the next pass.

## Testing

### Unit tests (`sim/practice_test.go`, new)

- `pickPracticeApproach` returns one of the active-runway approaches; `""` for an airport with no matching approaches; respects deterministic seeding.
- Spawn-side: `Probability: 1.0` with fixed `Min/Max` ⇒ every spawned aircraft from the flow has practice fields set; `Probability: 0` ⇒ none do.
- `practiceMissedApproach()` on an aircraft with `MissedApproachesRemaining: 3` leaves it at `2`, clears `Approach.Cleared/InterceptState/AssignedId/Assigned`, leaves `PracticeApproachID` and `PracticeApproachController` intact.
- Final-approach branch: an aircraft at MAP with `MissedApproachesRemaining: 0` does not enter the practice branch; takes the existing landing path.
- Pilot transmission text builder: produces "...for the practice" for `FullStop: false`; "...this will be a full stop" for `FullStop: true`; correct spoken approach name from `av.Approach`.

### Validation tests (scenario load)

- `PracticeApproaches{Probability: -0.1, ...}` ⇒ load error.
- `PracticeApproaches{MinMissedApproaches: 5, MaxMissedApproaches: 3}` ⇒ load error.
- Negative bounds ⇒ load error.
- `PracticeApproaches: nil` (default) ⇒ loads fine, no practice traffic.

### Integration test (`sim/practice_integration_test.go`, new)

End-to-end: spawn a practice aircraft with `MissedApproachesRemaining: 2`. Simulate clearances + miss completions. Verify:
- Two pilot-request transmissions fire pre-miss (initial + after miss 1) with `FullStop: false`.
- One transmission fires after miss 2 with `FullStop: true`.
- Tower-→-approach hand-back fires twice (after miss 1, after miss 2).
- On the third approach the aircraft lands and is deleted.
- Counter never goes negative.

### Manual end-to-end

Spawn a scenario flow with practice config. Confirm:
- Pilot says "request the [approach] for the practice" on check-in.
- User clears, aircraft flies approach, misses, hands back to approach with the pilot request firing on level-off.
- Repeat for N misses.
- On the final approach the pilot says "full stop"; aircraft lands.
- No new visual indicators (scratchpad unchanged).

## Non-goals

- **VFR practice approaches.** Different separation rules ("approach approved, maintain VFR"), different state. Out of scope; can stack on later.
- **Pilot-driven approach switching.** The pilot always requests the same approach picked at spawn. If variety is wanted, the scenario author spawns multiple practice aircraft with different picks.
- **Pilot-driven loop-back.** The AI does not self-vector back to final. The user vectors them around just like a real go-around.
- **On-demand controller command** to convert an in-flight aircraft into a practice customer. Future v2 if anyone asks.
- **Visual indicators / scratchpad markers.** The controller learns from transmissions and behavior. Future v2 if anyone asks.
- **Stop-and-go / touch-and-go** as termination types. Only "low approach" and "full stop" are modeled in v1.
- **The 7110.65 controller question** ("how will the approach terminate?"). The pilot pre-answers in the request, so this controller-side question is never needed in v1.
