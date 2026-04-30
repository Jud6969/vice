# Real-World Schedule — Design

**Branch:** `schedule-traffic` (off `upstream/master` @ `6aedbfbc`)
**Status:** Spec — pending implementation plan

## Goal

Replace vice's constant-rate IFR spawning with per-airport, per-15-minute-bucket
schedules driven from authored CSV files. A "Use real-world schedule"
checkbox on the scenario configuration screen enables the feature for
scenarios whose airports have schedule data; sim time then advances from
a user-picked (month, day-of-week, hour:minute) start. A histogram on the
picker shows the total IFR traffic curve for the scenario's covered
airports/types so the user can pick a shift to work.

For the user's own testing — not destined for upstream PR. Branched fresh
from upstream/master to keep this independent of the map-window /
session-replay branches.

## Non-Goals

- **VFR / overflights / pattern traffic stay on the existing rate engine.**
  Only IFR (departures + arrivals) is schedule-driven in v1.
- **No flow-level rates.** Schedule is per-airport totals (arr/dep). The
  spawn engine already knows how to distribute arrivals across the
  scenario's active inbound flows; we feed it a *total* per airport per
  bucket and let it route.
- **No holiday overrides in v1.** Layer 3 from brainstorming
  (`{"2026-12-25": {"use_day": "sunday", "multiplier": 0.4}}`) is deferred.
  Monthly multipliers (layer 2) cover most seasonal swings; named holidays
  can come in v2.
- **No real-world callsign / aircraft type matching.** The schedule emits
  rates only; vice's existing pattern of generating callsigns + types from
  the airport's airline mix continues unchanged.
- **No live data feed.** Schedules are statically authored CSV files
  committed to the repo.
- **No per-runway scheduling.** Runway choice remains wind/config driven
  at connect time.

## User-facing summary

1. Author a `schedules.json` per ARTCC under
   `resources/configurations/<ARTCC>/`, plus per-airport
   `<ICAO>-schedule.csv` files in the same folder.
2. On the scenario configuration screen (the second screen of the connect
   dialog), a new "Use real-world schedule" checkbox appears below the
   existing knobs. It is enabled iff at least one of the scenario's
   airports has a schedule entry. Hovering a disabled checkbox shows the
   tooltip "No schedules specified for this scenario."
3. When the checkbox is on, three controls + a histogram appear:
   - **Month slider** (Jan–Dec). Default: current month.
   - **Day slider** (Mon–Sun). Default: current weekday.
   - **Time slider** (00:00–23:45 in 15-min steps). Default: current
     wall-clock time, rounded to the nearest 15 minutes.
   - **Histogram** above the time slider — 96 bars, two-color stacked
     (arrivals bottom, departures top), height = scenario-aggregated
     totals for the picked (month, day). Hover any bar → tooltip listing
     per-airport `(arr, dep)` for that bucket. Clicking a bar snaps the
     time slider to that bucket.
4. When the user starts the sim, the sim wall clock starts at the picked
   moment (next occurrence of that month/day/time in the future-ish; see
   "Sim-time mapping" below). The spawn engine reads `(weekday, bucket)`
   from the live sim clock each tick and uses the corresponding rate.
5. Sim time advances naturally; the user can work a "shift" of any length.
   When sim time crosses bucket / hour / day / month boundaries, rates
   update transparently.

## Architecture

### Components

```
resources/configurations/<ARTCC>/
   schedules.json           Manifest: per-airport metadata + monthly multipliers.
   <ICAO>-schedule.csv      Per-airport 15-min bucket data.

sim/schedule/                (new package)
   format.go                Schedule, AirportSchedule, Bucket types + JSON/CSV parsers.
   loader.go                LoadARTCC(dir string, lg) -> *Schedule, error.
   loader_test.go           Round-trip + edge-case tests.
   rate.go                  RateAt(simTime, airport) -> (depPerHr, arrPerHr).
   rate_test.go             Tests for bucket lookup, monthly multiplier, missing data.

sim/spawn.go (modify)        LaunchConfig gains a non-persisted *Schedule field
                             plus a SchedulePickedTime time.Time. When set,
                             the periodic-rate path consults the schedule
                             instead of the static rate.
sim/spawn_arrivals.go (modify)  same path
sim/spawn_departures.go (modify) same path

cmd/vice/simconfig.go (modify)  Configuration screen: add "Use real-world
                                schedule" checkbox + month/day/time sliders +
                                histogram.

panes/schedulehist.go (new, *or* cmd/vice helper)  Histogram drawer.
                                Reuses imgui.PlotHistogram with custom
                                tooltip for per-airport breakdown.
```

### File format

#### `schedules.json` (per ARTCC)

```json
{
  "airports": {
    "KJFK": {
      "csv": "KJFK-schedule.csv",
      "monthlyMultiplier": {
        "1": 0.90, "2": 0.92, "3": 1.00, "4": 1.05,
        "5": 1.10, "6": 1.20, "7": 1.25, "8": 1.20,
        "9": 1.05, "10": 1.00, "11": 1.05, "12": 0.85
      }
    },
    "KLGA": {
      "csv": "KLGA-schedule.csv",
      "monthlyMultiplier": { "1": 0.90, "7": 1.20, "12": 0.85 }
    },
    "KEWR": { "csv": "KEWR-schedule.csv" }
  }
}
```

- `csv` is the filename relative to the ARTCC folder.
- `monthlyMultiplier` is optional; missing months default to `1.0`.
- Keys "1"–"12" are calendar months (January = 1).
- **Monthly multiplier is the only "seasonal" lever in v1** — it scales every
  bucket of every day uniformly for the picked month. Per-month *shape*
  changes (e.g., longer evening pushes in summer than in winter) are NOT
  expressible in v1; they would need either per-season CSVs or a
  per-month-per-bucket grid, both deferred to v2.

#### `<ICAO>-schedule.csv` (per airport)

```csv
day,bucket,dep,arr
MON,00:00,0,0
MON,00:15,0,0
…
MON,07:00,8,12
MON,07:15,8,8
MON,07:30,7,3
MON,07:45,5,2
MON,08:00,8,12
…
SAT,07:00,4,3
…
```

- `day`: `MON`, `TUE`, `WED`, `THU`, `FRI`, `SAT`, `SUN`.
- `bucket`: `HH:MM` in 15-minute increments (`00:00`, `00:15`, ..., `23:45`).
- `dep`, `arr`: integer aircraft-per-hour rates for that 15-min bucket
  (so a row with `dep=8` means "during this 15-minute window, departures
  spawn at the rate of 8 per hour" — about 2 departures in those 15
  minutes on average).
- Missing rows for a (day, bucket) → rate of 0 (dead bucket).
- Missing rows for an entire day → that day mirrors no-traffic; if you
  want Tue to mirror Mon, copy the rows. (Versus the JSON-aliasing form
  we discussed; CSVs are easier to author with explicit copy/paste.)

### Loader

`sim.schedule.LoadARTCC(dir string, lg *log.Logger)` walks
`<dir>/schedules.json`, reads each referenced CSV, validates row shapes,
and returns a `*Schedule` keyed by ICAO. Missing `schedules.json` → returns
nil + nil error (scenarios in that ARTCC simply don't get the feature).
Parse errors → returns the error and the scenario falls back to constant
rates (the checkbox stays disabled).

`Schedule.HasAirport(icao) bool` — used to decide whether the checkbox is
enabled for a given scenario.

### Rate lookup

`Schedule.RateAt(simTime time.Time, icao string) (dep, arr float32)`:

1. `weekday` = `simTime.Weekday()` mapped to "MON".."SUN".
2. `bucket` = floor(simTime.Hour:Minute) to nearest 15-min, formatted `HH:MM`.
3. Look up `airports[icao].buckets[weekday+bucket]`.
4. If found: `dep` and `arr` are the raw values × `monthlyMultiplier[simTime.Month()]`.
5. If not found: `(0, 0)`.

Missing airport → `(0, 0)`.

### Engine integration

`sim.LaunchConfig` gains two non-persisted fields:

```go
schedule       *schedule.Schedule  // nil = no schedule mode
scheduleStart  time.Time           // sim-clock origin chosen by user
```

The Poisson-rate paths in `spawn_arrivals.go` and `spawn_departures.go`
currently compute a per-tick spawn probability from the static
`InboundFlowRates` / `DepartureRates`. When `schedule != nil`:

- For each airport relevant to the spawn step, call
  `schedule.RateAt(simTime, icao)` to get current `(dep, arr)`.
- Replace the static rate with the dynamic one for this tick. The rest
  of the spawn engine (Poisson roll, runway selection, fix selection,
  inbound-flow distribution for arrivals) is unchanged.

The schedule's `(dep, arr)` values are **per-airport totals**. For
arrivals, distribute across active inbound flows in the scenario using
the same proportional weights the existing `InboundFlowRates` carry —
so if a scenario has flows ARRIVAL_NORTH:ARRIVAL_SOUTH at a 1:3 weight
ratio and the schedule says KJFK arrivals = 24/hr at 0700, we feed
ARRIVAL_NORTH 6/hr and ARRIVAL_SOUTH 18/hr. Same logic the engine uses
today; only the total is dynamic.

### Sim-time mapping

When the user picks `(month=Jul, day=Tue, time=07:00)` and clicks
Connect, vice computes a concrete date in the past or future that
satisfies all three (e.g., the most recent Tuesday in July of the
current year, or the next future one if "current" is in the past) and
sets the sim's start `time.Time` to that. From there the sim advances
normally. METAR / sun position / time-of-day rendering all use the sim
clock as they do today, so the world looks coherent at the picked
moment.

If the user fast-forwards (×2, ×4, etc.), the sim clock moves faster
relative to wall time and the schedule keeps up — picking 0700 and
running ×4 means at wall-clock 0815 the sim is at 11:00 and reads the
1100 bucket.

### UI

The new controls live on the scenario configuration screen
(`ConfigurationModalClient` in `cmd/vice/dialogs.go` / `simconfig.go`).
After the existing options:

```
[ ] Use real-world schedule    (disabled if Schedule.HasAirport returns
                                false for every scenario airport;
                                hover → "No schedules specified for this
                                scenario.")

  ── (only when checkbox is on:)
  Month:    [Jan]──────────[Dec]    Jul
  Day:      [Mon]──────────[Sun]    Tue
  Time:     [00:00]────────[23:45]  07:00

  ┌──────────────────────────────────────────┐
  │ ▆▆▆▆ ▆▆▆▆▆ ▆▆▆▆▆▆▆▆▆ ▆▆▆▆▆▆ ▆▆▆▆▆▆ ▆▆▆▆ │  ← histogram (96 stacked bars)
  └──────────────────────────────────────────┘
       06    08    10    12    14    16    18  20  22  hour ticks
```

- Bars are two-color stacked: bottom = arrivals (e.g., desaturated
  blue), top = departures (e.g., desaturated orange). Same colors used
  consistently so the viewer learns them.
- Hover a bar → tooltip lists per-airport `(arr, dep)` totals for that
  bucket and the resolved date.
- Click a bar → time slider snaps to that bucket.
- Sliders update the histogram live (only month/day actually re-aggregate
  the data; time slider just moves a position marker).

### Error handling

- **Schedule file unparseable** (malformed CSV, bad JSON): log a warning
  on scenario-load, treat the airport as if it had no schedule. Other
  airports in the same `schedules.json` still load.
- **CSV references missing file**: log warning, skip that airport.
- **Bad bucket value** (e.g., `07:13`): log warning per row, skip the row.
- **Negative or non-integer rate**: log warning per row, clamp to zero
  and continue.
- **Schedule defined but the picked airport has no rows for the picked
  (day, bucket)**: rate = 0. Histogram shows an empty bar, picker still
  allows it.

### Testing

**Unit:**
- `schedule.format_test`: round-trip a small JSON + CSV through the
  loader, assert the in-memory shape matches.
- `schedule.rate_test`: monotonic time-walk through 24 hours assertions
  (rate at 07:14 == rate at 07:00 bucket; rate at 07:15 != 07:00 if the
  next bucket differs). Monthly multiplier applied. Missing airport → 0.
- Invalid input: malformed CSV row, bad bucket, negative rate, missing
  CSV file. All should log + degrade, not crash.

**Manual:**
- Create a small ZNY schedule covering KJFK and KLGA only. Open a
  scenario covering all four airports. Verify the checkbox is enabled.
  Toggle on; verify histogram shows traffic for KJFK + KLGA only
  (KEWR + KTEB contribute zero).
- Pick Tue 0700, July. Connect. Watch traffic ramp up over 0700–0900
  per the authored schedule.
- Connect at 02:00 (a dead hour). Verify no IFR traffic spawns until
  the schedule's first non-zero bucket. Optionally fast-forward.
- Open a scenario in an ARTCC with no `schedules.json`. Verify the
  checkbox is disabled with the tooltip.

**Build:** `go build -tags vulkan ./cmd/vice` must succeed.

## Strict adherence (added 2026-04-30)

Originally the spec said only IFR (departures + arrivals) is schedule-driven;
VFR / overflights / pattern stay on the existing rate engine. Testing showed
that produced a confusing UX: starting the sim at a dead IFR period still
showed overflights, prespawned aircraft, and full-rate VFR — so the
"dead period = empty sky" expectation was violated.

Updated behavior: when `Schedule` is on, **all traffic** (IFR + overflights
+ VFR + prespawn) scales with a single per-bucket busyness factor.

### The busyness factor

At every 15-min bucket boundary, `applyScheduledRates` computes:

```
factor = clamp(currentTotalScheduled / peakTotalScheduled, 0.05, 1.0)
```

- `currentTotalScheduled` = sum of `(dep + arr)` over all schedule-covered
  airports at the current sim time.
- `peakTotalScheduled` = max of that sum across all 96 buckets of the
  current weekday (cached once per day).
- `0.05` floor keeps overflights / VFR from going completely silent overnight
  (real airspace at 04:00 still sees the occasional flight).

Stored on `Sim` as `scheduleBusyness float32`.

### What `factor` controls

| Component | Mechanism |
|---|---|
| IFR departures | Already schedule-driven (per-runway share × airport's scheduled dep total). |
| IFR arrivals | Already schedule-driven (per-flow share × airport's scheduled arr total). |
| Overflights | `runtimeInboundFlowRates[group]["overflights"] = staticOverflightRate × factor`. Re-applied at each bucket crossing. |
| VFR departures | A new `Sim.scheduleVFRScale float32` (default 1.0) is multiplied INTO the existing `LaunchConfig.VFRDepartureRateScale` at the spawn site. Avoids stomping the user's authored scale. |
| Prespawn (sim launch) | At `Sim.Prespawn`, when `Schedule` is non-nil, the targeted aircraft count is multiplied by `factor` evaluated at `s.State.StartTime`. Sky starts as empty as the schedule says. |

Day/night cycle emerges naturally from the IFR schedule's overnight-trough
shape — no separate VFR curve required for v1.

### Restored custom histogram

The temporary switch to `imgui.PlotHistogramFloatPtrV` was a workaround for
a perceived rendering issue that turned out to be data (the user was on a
non-Monday weekday with no authored buckets). The custom dual-color stacked
histogram (`drawScheduleHistogram` with `AddRectFilled` per bar) is
restored — same shape as originally specified.

## Open items / risks

- **Authoring effort.** A full week × 96 buckets × ~5 airports = ~3360
  rows per ARTCC. Acceptable in a spreadsheet but won't be done by the
  developer in one sitting.
- **eATS as a seed-data source (v2 tool).** ATSim 2020's eATS data lives
  under `%LOCALAPPDATA%\ATSim2020\eATS\` and contains
  `<TRACON>-FlightPlans.txt` files where each row is a flight-pattern
  template: `FPX TRACON AIRPORT TYPE 01 START_HHMM END_HHMM RATE DAYS_BITMASK
  CALLSIGN ACTYPE ALT TAS ROUTE`. A planned `cmd/eats2schedule` tool
  could parse these, aggregate per (airport, weekday, 15-min bucket),
  and emit our CSV format. Direction is one-way (eATS templates →
  vice CSVs); fine because the templates carry strictly more
  information than our totals need. Not part of v1 — but the v1 file
  shape is intentionally compatible with this future ingestion path.
  Hand-authored CSVs and tool-generated CSVs are interchangeable.
- **Histogram data point: 96 × N airports.** Aggregating 96 buckets
  across a handful of airports is trivial CPU. The hover-tooltip per-
  airport breakdown means we keep per-airport data resident, not just
  the aggregate.
- **Sim-clock origin choice** for the picked (month, weekday, time):
  vice's existing sim clock starts at "now" by default. We need to
  override that when the schedule mode is on. Verify this hook point
  fits cleanly into `Sim.StartTime` or wherever the wall-clock origin
  lives in the sim engine.
- **Scenario configuration screen layout.** Adding a checkbox + three
  sliders + histogram below the existing controls increases the modal's
  vertical size. May need to scroll on small displays. Verify the modal
  size constraints (`MaxHeight` from `dialogs.go:170` is `vpSize.Y *
  19/20`) leave room.
- **Mid-session toggle.** The checkbox is a connect-time choice; we
  don't support flipping mid-sim. (Consistent with how scenario config
  works today.)
