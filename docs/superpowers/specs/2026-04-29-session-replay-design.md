# Session Replay — Design

**Branch:** `session-replay` (off `map-window`)
**Status:** Spec — pending implementation plan

## Goal

Add the ability to record per-tick aircraft state during a live sim and replay
it later inside the existing `MapPane`. Recording is opt-in via a settings
toggle; replays are opened from the connect-or-benchmark dialog (either
"Replay last session" quick-button or "Replay session…" file-picker). During
replay the Map's existing UX (pan/zoom, hover tooltip, click-to-select with
top-right info panel, past trail, future route) all work as if live, plus a
timeline bar at the bottom of the canvas with play/pause, scrub, speed, and
step controls.

For the user's own testing — not destined for upstream PR.

## Non-Goals

- **Not** an event-track / bookmark / "jump to next handoff" replay. Pure scrub
  + play of recorded state.
- **Not** an editor: replays are read-only.
- **Not** a debugger of the sim engine: this records what the *client* saw, not
  what the server *was*. Server-side scenario replay already exists via the
  CLI flag `--replay`; that is a different and orthogonal feature.
- **Not** a network-replay protocol: there is no wire format here, just a
  flat on-disk file written by the client.

## User-facing summary

1. **Settings panel** (`uiDrawSettingsWindow` in `cmd/vice/ui.go:831`) gets a
   "Session replay" subsection with two checkboxes:
   - "Record this session" — when on, the client appends a snapshot to a
     replay file every time the server pushes a state update.
   - "Auto-prune old replays" — when on, vice keeps only the most recent N
     `.bin` files in `~/.vice/replays/` (default 10) and deletes the rest at
     startup. A small int input lets the user change N.
2. **Connect-or-benchmark dialog** (`uiShowConnectOrBenchmarkDialog`) gets two
   new buttons beside the existing connect/benchmark choices:
   - **"Replay last session"** — loads the newest `.bin` file in
     `~/.vice/replays/` directly.
   - **"Replay session…"** — opens a file picker showing all `.bin` files
     newest-first, listing facility name + start timestamp + duration. User
     selects one.
3. **Map** opens with the recording loaded. A bottom toolbar appears (only in
   replay mode) with: timeline slider, play/pause, speed combo
   (0.25× / 0.5× / 1× / 2× / 4× / 8×), step-by-tick (◀◀ / ▶▶), and a
   `MM:SS / MM:SS` label. All other Map UX (filter combo, hover, click-info,
   trail, route, airport labels, basemap, facility boundary) work identically
   to live mode, sourced from the recorded state at the current scrub time.
4. **Live sim launches close the replay.** Starting "Connect" closes the
   replay-mode UI; the user can re-open the replay later from the connect
   dialog.

## Architecture

### Components

```
client/replay/                              (new package)
   ├── recorder.go      Recorder type. Owns the open file handle. Append-only.
   ├── reader.go        Replay loader: read header + frames into memory.
   ├── format.go        Serialization version + msgpack frame envelope.
   └── prune.go         Auto-prune helper (sorts by mtime, deletes oldest).

panes/
   ├── trackdata.go     (new) TrackSource interface + a thin LiveTrackSource
   │                    wrapper around *client.ControlClient.
   ├── mappane.go       Refactored to take TrackSource instead of
   │                    *client.ControlClient through the draw chain.
   ├── mappane_replay.go (new) Bottom-bar timeline UI + replay-clock owner;
   │                    invoked from drawCanvas only when the source is a
   │                    *replayPlayer.
   └── (all other mappane_* files unchanged in shape; the parameter type
        passed in changes from *client.ControlClient to TrackSource)

cmd/vice/
   ├── config.go        Add Config.RecordReplay bool, Config.AutoPruneReplays
   │                    bool, Config.ReplayKeepCount int.
   ├── ui.go            Settings checkboxes + "Replay last session" / "Replay
   │                    session…" buttons on the connect dialog.
   └── main.go          Recorder lifecycle: open in connectmgr post-connect
                        callback, close on disconnect; prune at startup.

resources/
   (no new resources)
```

### TrackSource interface

The Map drawers currently take `*client.ControlClient`. They use
`c.Connected()`, `c.State.Tracks`, `c.State.UserTCW`, `c.State.NmPerLongitude`,
`c.State.Facility`, `c.State.Airports`, `c.State.Controllers`. These are the
methods we factor:

```go
// panes/trackdata.go (new)
type TrackSource interface {
    Connected() bool
    Tracks() map[av.ADSBCallsign]*sim.Track
    UserTCW() sim.TCW
    NmPerLongitude() float32
    Facility() string                              // STARS facility id, e.g. "ZNY"
    Airports() map[string]*av.Airport
    Controllers() map[sim.ControlPosition]*av.Controller
}

// LiveTrackSource adapts a *client.ControlClient to TrackSource.
type LiveTrackSource struct {
    Client *client.ControlClient
}
```

Both the live client and the replay player implement `TrackSource`. The Map
drawers don't care which is which.

### Recording

Hook point: `client/client.go` has `update.Apply(&state.SimState, es)` at
line 174 (called inside `pendingCall.InvokeCallback`). Immediately after this
call, the Recorder (if configured on the client) is given the freshly-updated
`SimState` and writes a frame.

```go
// client/replay/recorder.go (new)
type Recorder struct {
    f         *os.File
    enc       *msgpack.Encoder
    startTime time.Time
    facility  string
    closed    bool
    lg        *log.Logger
}

func NewRecorder(facility string, startTime time.Time, lg *log.Logger) (*Recorder, error)
// Returns nil + nil if recording is disabled (so call sites don't need to
// branch). Filename: ~/.vice/replays/<facility>-<YYYYMMDD-HHMMSS>.bin

func (r *Recorder) AppendFrame(simTime sim.Time, tracks map[av.ADSBCallsign]*sim.Track) error
func (r *Recorder) Close() error
```

`*client.ControlClient` grows a `recorder *replay.Recorder` field. The
`ConnectionManager` constructs a Recorder when `config.RecordReplay == true`
at connect time and passes it to `ControlClient`. On disconnect, `Close()`
flushes and closes the file.

### File format

```
[Header]   one msgpack-encoded value:
             Version       int    (currently 1)
             Facility      string
             StartTime     int64  (unix nanoseconds)
             SerVersion    int    (vice's existing ViceSerializeVersion)

[Frame 0]  one msgpack-encoded value:
             SimTimeNanos  int64
             Tracks        map[ADSBCallsign]*sim.Track
[Frame 1]  ...
[Frame N]  ...
```

The msgpack stream is appended frame-by-frame; reading is
`for { dec.Decode(&frame); if err == io.EOF { break }; ... }`.

`SerVersion` mismatch on read: log a warning and skip the file (or attempt
best-effort decode if the version differs by a known compatible amount).
Vice already uses `ViceSerializeVersion` for the same purpose in its config
file (`cmd/vice/config.go`), so we hang off that.

### Replay player

```go
// client/replay/reader.go (new)
type Replay struct {
    Header   Header        // Facility, StartTime, etc.
    Frames   []Frame       // sorted by SimTime ascending
}

func Load(path string, lg *log.Logger) (*Replay, error)

// panes/mappane_replay.go (new)
type replayPlayer struct {
    rp           *replay.Replay
    cur          int      // current frame index
    playing      bool
    speed        float32  // 1.0 = real-time relative to recording's wall-clock
    lastAdvance  time.Time

    // cached "current state" exposed via TrackSource
    tracks            map[av.ADSBCallsign]*sim.Track
    userTCW           sim.TCW   // from header (the recording user's TCW)
    facility          string
    nmPerLongitude    float32
    airports          map[string]*av.Airport
    controllers       map[sim.ControlPosition]*av.Controller
}

func newReplayPlayer(r *replay.Replay) *replayPlayer
func (p *replayPlayer) Connected() bool { return p.rp != nil }
// ... TrackSource impl
func (p *replayPlayer) Tick(now time.Time)              // advances cur if playing
func (p *replayPlayer) SeekTo(frame int)
func (p *replayPlayer) Step(delta int)
func (p *replayPlayer) DrawTimelineBar(canvasOrigin, canvasSize [2]float32)
```

`replayPlayer.Tick` is called every frame from `MapPane.drawCanvas` (only in
replay mode). It reads `(now - lastAdvance) * speed` and advances
`cur` by the number of recorded frames whose `SimTime` falls within that
elapsed range. This gives smooth time-correct playback even when frames are
unevenly spaced (which they will be, since we record on server-update events,
not fixed cadence).

### Where the replay state lives

The `ui` struct in `cmd/vice/ui.go` already holds transient UI state
(`showMap`, `showSettings`, etc.). It gains a single non-persisted field
`replayPlayer *panes.replayPlayer`. When non-nil, the menu-bar driver in
`uiDraw` constructs a `replayTrackSource` wrapping it and passes that to
`MapPane.DrawWindow`. When live `Connect` is invoked, the player is closed
and the field reset to nil.

### Auto-prune

At startup, after config load, if `config.AutoPruneReplays` is true:

```go
// client/replay/prune.go
func Prune(dir string, keep int, lg *log.Logger) error
// Lists *.bin files, sorts by mtime desc, deletes everything past index `keep-1`.
```

Logged at info level so the user can see what was deleted.

## Data flow (summary)

**Live mode + recording on:**
```
server -> RPC -> ControlClient.checkPendingRPCs ->
  pendingCall.InvokeCallback ->
    update.Apply(&state.SimState, es) ->
    recorder.AppendFrame(state.SimTime, state.Tracks)
                                       (sync, append-only msgpack write)
```

**Replay mode:**
```
connect dialog "Replay session…" -> replay.Load(path) -> *Replay
  -> ui.replayPlayer = newReplayPlayer(rp)
  -> MapPane.DrawWindow(replayTrackSource, ...)
       drawCanvas per frame:
         replayPlayer.Tick(time.Now()) advances cur
         replayPlayer.tracks reflects rp.Frames[cur].Tracks
         all existing drawers iterate replayPlayer.tracks
         replayPlayer.DrawTimelineBar(...) at bottom of canvas
```

## Error handling

- **Recorder open fails** (disk full, permission denied): log warning, set
  `client.recorder = nil`, the live session continues.
- **AppendFrame fails midway** (disk full): log warning once, set
  `client.recorder = nil` (drop the rest of the recording), live session
  continues.
- **Replay file missing / corrupt**: error dialog ("Could not open replay:
  <reason>"), return to the connect dialog. Don't crash.
- **Replay file from incompatible vice version**: error dialog with the
  version mismatch info. Don't try to half-decode.
- **Auto-prune deletes a file that's currently being recorded**: not possible;
  prune runs at startup, before the new recording opens.
- **Replay loaded but the recorded facility's airports / controllers aren't
  in `av.DB`** (unlikely but possible across vice versions): airport labels
  silently degrade to nothing; the replay still plays.

## Persistence

These three fields persist via the existing JSON config:
- `Config.RecordReplay bool` (default false)
- `Config.AutoPruneReplays bool` (default false)
- `Config.ReplayKeepCount int` (default 10)

The `replayPlayer` itself is transient — never persisted. Closing vice with
a replay open returns to a normal state next launch.

## Testing

**Unit:**
- `replay.format` round-trip: encode a header + 3 frames, decode, assert
  equality.
- `replay.prune` mtime-sort and delete: create 5 dummy files with staggered
  mtimes, prune to keep=2, assert the right 2 survive.
- `replayPlayer.Tick` advances correctly: synth a `*Replay` with frames at
  t=0, 1, 2, 3 seconds. Tick at speed=1.0 with a 1.5s `time.Sleep` simulator,
  assert `cur` advances from 0 to 1 (1.5s past t=1, before t=2).

**Manual:**
1. Settings → enable Record. Run a scenario for ~2 minutes. Disconnect.
   Verify a `<facility>-*.bin` file appears in `~/.vice/replays/`.
2. Connect dialog → "Replay last session". Map opens, timeline at bottom,
   aircraft moving as recorded. Hover/click/info-panel/filter/trail/route all
   work.
3. Scrub the slider. Aircraft positions update to the scrubbed time.
4. Speed combo → 4×. Aircraft move 4× faster. Step buttons advance one
   frame. Pause holds.
5. Open "Replay session…" picker, see the file listed with facility +
   timestamp + duration.
6. Disconnect → Connect a new sim → replay closes, live Map shows live
   data.
7. Settings → enable Auto-prune, keep=2. Restart vice. Older replay files
   beyond the most recent 2 are gone.
8. Open a replay, then click "Connect" → replay closes, sim starts. No
   crash.
9. Disable record → run another session → no new file appears. Old files
   intact.

**Build:** `go build -tags vulkan ./cmd/vice` must succeed.

## Open items / risks

- **`sim.Track` already serializes via gob/msgpack on the wire** (it crosses
  the RPC boundary). So we know msgpack works for it. But it carries
  `*NASFlightPlan` and `*ATPAVolume` pointers that may have unexported
  fields; verify a round-trip end-to-end before locking the format.
- **Frame-rate of recordings is variable.** Server pushes are not perfectly
  periodic, so a session may have 3000 frames in one hour and 2200 in
  another. The timeline shows wall time, not frame count, so this is fine.
- **Refactoring the Map drawers to take `TrackSource`** is the load-bearing
  refactor. It touches 7 files (`mappane*.go`). Done once carefully it
  unblocks everything else; done sloppily it could regress live-mode
  behavior. Plan must cover this with a passing-tests gate.
- **"Replay session…" picker UI** has to use Windows file-dialog (`zenity`
  package, already imported in `ui.go`) or imgui-based row list. The latter
  is more consistent with vice's existing dialog style.
- **Replay file size**: at full `sim.Track` ~250 B/track × 50 aircraft × ~1
  frame/sec × 3600 sec ≈ 45 MB/hr. Acceptable for testing. Will revisit if
  someone records a 12-hour session.
