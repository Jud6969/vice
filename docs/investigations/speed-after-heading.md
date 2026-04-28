# Investigation: aircraft speeds up after heading change post speed assignment

## Symptom (user report)

> After giving an aircraft a specific speed to fly, if you give them a new
> heading to fly they will start speeding up for no reason.

## Branch / starting point

- Branch: `fix-speed-on-heading-change` (forked from `upstream/master` @9d1e2ad3)
- Probe tests added at `nav/heading_speed_repro_test.go` (kept for follow-up)

## What's been ruled out

The "minimal" reproduction does **not** reproduce the bug. With:

- Arrival on STAR, IAS=280, alt=11000
- `AssignSpeed(MakeAtSpeedRestriction(220))` (exact 220)
- 30 ticks (IAS settles at 220 first)
- `AssignHeading(270, TurnClosest)`
- 120 more ticks

…IAS stays nailed at 220 the entire time. Same result with
`MakeAtOrAboveSpeedRestriction(220)` and `MakeAtOrBelowSpeedRestriction(220)`.
Adding altitude restrictions to the waypoints (`/a10000`, etc.) doesn't change
anything — `Speed.Assigned` is preserved across `AssignHeading`, and
`TargetSpeed` returns it unchanged when `Heading.Assigned != nil`.

Also probed: `AssignAltitude` then `AssignSpeed` (which moves the *altitude*
into `AfterSpeed` via the >=20kt deferral in
`AssignSpeed` at `nav/commands.go:228`). Speed stays at 220 after heading.

## What hasn't been probed yet (next session)

The most plausible remaining path is the **inverse** ordering:

1. `AssignSpeed(220)` (aircraft at 280)  → `Speed.Assigned = 220`
2. `AssignAltitude(6000)` while still ≥20kt from target →
   `prepareAltitudeAssignment` (`nav/commands.go:71-79`) **stashes**
   `Speed.Assigned` into `Speed.AfterAltitude` and clears `Speed.Assigned`
3. `AssignHeading(270)` → `Heading.Assigned` set, `Speed.Assigned` is nil
4. In `TargetSpeed` (`nav/speed.go:103`), `Speed.Assigned == nil`, so the
   function falls through past the `MaintainSlowestPractical` /
   `MaintainMaximumForward` / `Speed.Assigned` early-returns. With
   `Heading.Assigned != nil` the upcoming-fix-restriction branch is gated off
   (`speed.go:193`), and the function eventually returns
   `targetAltitudeIAS()` — i.e. natural cruise (≥250kt).

The unfinished probe `TestProbeSpeedAltHeading_Sequence` in
`nav/heading_speed_repro_test.go` was added to test exactly this. **Run it
next.**

If that confirms the hypothesis, the bug is: assigning altitude *after* a
speed assignment silently parks the speed in `AfterAltitude`, and the
subsequent heading change exposes that the aircraft is no longer being held
to the speed (the route-derived restrictions that would otherwise have kept
it slow are now ignored because `Heading.Assigned != nil` gates them off).

The "speed up because of heading" framing is the symptom; the real cause is
the altitude-then-heading interaction with a stashed speed.

## Other paths considered but unlikely

- `assignHeading` itself (`nav/commands.go:471`) — does NOT touch `nav.Speed`.
  Only sets `nav.Approach.Cleared = false`, `PassedApproachFix = false`, and
  for STAR/approach arrivals with no assigned altitude, sets
  `Altitude.Cleared = current` and `RequestAltitude = true`.
- `EnqueueHeading` (`nav/nav.go:512`) — sets `DeferredNavHeading`. No speed.
- The `inside 5 NM final` clear at `nav/speed.go:113-116` — gated on
  `DistanceToEndOfApproach()` returning no error, which it can't when
  `Heading.Assigned != nil` (it returns `ErrNotFlyingRoute`).
- Range-speed clamp from commit 44a796f3 (`speed.go:137-139`) — returns
  `clamp(IAS, lo, hi)`, behaves correctly across heading changes.

## Files touched

- `nav/heading_speed_repro_test.go` (NEW — probe scaffolding, keep)
- `docs/investigations/speed-after-heading.md` (NEW — this file)

## Picking it up next time

```sh
git checkout fix-speed-on-heading-change
go test -tags vulkan -v -run TestProbeSpeedAltHeading_Sequence github.com/mmp/vice/nav
```

If IAS climbs back toward natural cruise after the heading is given, the
hypothesis above is confirmed and the fix likely belongs in
`prepareAltitudeAssignment` (don't stash speed if a heading is/has-been
assigned, OR keep `Speed.Assigned` pointing at the stash so `TargetSpeed`
still sees a target). Discuss with the user before choosing the fix
location — the deferral is intentional behavior for normal route flying;
the bug is the gap that opens when heading mode kicks in afterwards.
