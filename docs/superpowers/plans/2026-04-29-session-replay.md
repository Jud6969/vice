# Session Replay Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add opt-in per-tick recording of `sim.Track` state during a live sim, plus a replay viewer that reuses the existing `MapPane` and adds a timeline bar.

**Architecture:** A new `client/replay/` package owns the on-disk format (msgpack header + msgpack frame stream), the `Recorder` wired through `*ControlClient.GetUpdates`, and the `Reader` / `Replay` types. `MapPane` is refactored to take a small `TrackSource` interface; `*client.ControlClient` gets a thin `LiveTrackSource` adapter, and the replay viewer constructs a `replayPlayer` that also implements `TrackSource`. A new replay-only timeline bar appears at the bottom of the canvas. The connect-or-benchmark dialog gets two new entries.

**Tech Stack:** Go, msgpack v5 (already a vice dep), cimgui-go, existing `MapPane` from the `map-window` branch. No new external deps.

**Branch:** `session-replay` (off `map-window` HEAD `47bb52d0`).

**Reference files:**
- `client/client.go:264–345` (`GetUpdates`) — the hook point for recording.
- `client/client.go:236` (`Disconnect`) — close hook for the recorder.
- `cmd/vice/dialogs.go:227` (`ScenarioSelectionModalClient`) — connect dialog where the new buttons go.
- `cmd/vice/ui.go:831` (`uiDrawSettingsWindow`) — settings panel.
- `panes/mappane.go` — drawer pipeline; will be refactored to take `TrackSource`.
- `util/rpc.go:23,73` — example of msgpack encoder/decoder usage in the repo.

---

## File structure

**New files:**
- `client/replay/format.go` — `Header`, `Frame` types; `EncodeHeader`, `EncodeFrame`, `DecodeHeader`, `DecodeFrame`. The serialization version constant.
- `client/replay/recorder.go` — `Recorder` type. Opens a file, encodes header on construction, encodes frames on `AppendFrame`, closes the file.
- `client/replay/reader.go` — `Replay` (loaded result), `Load(path) (*Replay, error)`.
- `client/replay/prune.go` — `Prune(dir string, keep int, lg) error`.
- `client/replay/format_test.go` — round-trip tests, prune behavior tests.
- `panes/trackdata.go` — `TrackSource` interface, `LiveTrackSource` adapter.
- `panes/mappane_replay.go` — `replayPlayer` type implementing `TrackSource`, plus the timeline-bar draw helper.
- `cmd/vice/replaydialog.go` — `ReplayPickerModalClient` (the file picker for "Replay session…").

**Modified files:**
- `client/client.go` — `ControlClient` gets a `recorder *replay.Recorder` field; `GetUpdates` calls `recorder.AppendFrame(...)` after the periodic update or any completed call applies new state; `Disconnect` calls `recorder.Close()`.
- `cmd/vice/config.go` — three new fields: `RecordReplay`, `AutoPruneReplays`, `ReplayKeepCount`.
- `cmd/vice/ui.go` — settings checkboxes in `uiDrawSettingsWindow`; ui struct gains `replayPlayer *panes.ReplayPlayer`; menu-bar map-window draw passes `LiveTrackSource` or the `replayPlayer` to `MapPane.DrawWindow`.
- `cmd/vice/dialogs.go` — `ScenarioSelectionModalClient.Buttons()` adds "Replay last session" and "Replay session…" entries.
- `cmd/vice/main.go` — calls `replay.Prune` at startup if `config.AutoPruneReplays` is true; constructs the recorder when a sim is connected if `config.RecordReplay` is true.
- `panes/mappane*.go` — every drawer that takes `*client.ControlClient` is refactored to take `TrackSource`. (`mappane.go`, `mappane_aircraft.go`, `mappane_overlays.go`, `mappane_selection.go`.)

---

## Task 1: replay package format + tests

End state: a self-contained `client/replay/` package that can encode/decode a header and frames, with passing tests. No integration with the rest of the codebase yet.

**Files:**
- Create: `client/replay/format.go`
- Create: `client/replay/format_test.go`

- [ ] **Step 1.1: Create `client/replay/format.go`**

```go
// pkg/client/replay/format.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

// Package replay handles on-disk recording and playback of vice sim sessions.
package replay

import (
	"fmt"
	"io"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"

	"github.com/vmihailenco/msgpack/v5"
)

// FormatVersion is the on-disk format version for replay files. Bump it
// whenever the Header / Frame shape changes incompatibly.
const FormatVersion = 1

// Header is the first msgpack value in every replay file.
type Header struct {
	FormatVersion int
	Facility      string
	StartTimeUnix int64 // unix nanoseconds at the start of the session
	SerVersion    int   // server.ViceSerializeVersion at record time
}

// Frame is one recorded tick. Frames stream after the header until EOF.
type Frame struct {
	SimTimeUnix int64
	Tracks      map[av.ADSBCallsign]*sim.Track
}

// EncodeHeader writes h to w as a single msgpack value.
func EncodeHeader(w io.Writer, h Header) error {
	enc := msgpack.NewEncoder(w)
	return enc.Encode(h)
}

// EncodeFrame writes f to w as a single msgpack value (appended after the header).
func EncodeFrame(w io.Writer, f Frame) error {
	enc := msgpack.NewEncoder(w)
	return enc.Encode(f)
}

// DecodeHeader reads the first msgpack value from r and returns it as a Header.
func DecodeHeader(r io.Reader) (Header, error) {
	var h Header
	dec := msgpack.NewDecoder(r)
	if err := dec.Decode(&h); err != nil {
		return Header{}, fmt.Errorf("decode header: %w", err)
	}
	return h, nil
}

// DecodeFrame reads one Frame from r. Returns io.EOF when the stream ends.
func DecodeFrame(r io.Reader, dec *msgpack.Decoder) (Frame, error) {
	var f Frame
	if dec == nil {
		dec = msgpack.NewDecoder(r)
	}
	if err := dec.Decode(&f); err != nil {
		return Frame{}, err
	}
	return f, nil
}
```

- [ ] **Step 1.2: Create `client/replay/format_test.go`**

```go
// pkg/client/replay/format_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"bytes"
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"

	"github.com/vmihailenco/msgpack/v5"
)

func TestHeaderRoundtrip(t *testing.T) {
	in := Header{FormatVersion: FormatVersion, Facility: "ZNY", StartTimeUnix: 1700000000000000000, SerVersion: 42}
	var buf bytes.Buffer
	if err := EncodeHeader(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, err := DecodeHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("header drift: in=%+v out=%+v", in, out)
	}
}

func TestFrameRoundtrip(t *testing.T) {
	var buf bytes.Buffer
	if err := EncodeHeader(&buf, Header{FormatVersion: FormatVersion, Facility: "ZNY"}); err != nil {
		t.Fatal(err)
	}

	frames := []Frame{
		{SimTimeUnix: 1, Tracks: map[av.ADSBCallsign]*sim.Track{
			"AAL1": {RadarTrack: av.RadarTrack{ADSBCallsign: "AAL1"}},
		}},
		{SimTimeUnix: 2, Tracks: map[av.ADSBCallsign]*sim.Track{
			"AAL1": {RadarTrack: av.RadarTrack{ADSBCallsign: "AAL1"}},
			"DAL2": {RadarTrack: av.RadarTrack{ADSBCallsign: "DAL2"}},
		}},
	}
	for _, f := range frames {
		if err := EncodeFrame(&buf, f); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := DecodeHeader(&buf); err != nil {
		t.Fatal(err)
	}

	dec := msgpack.NewDecoder(&buf)
	for i, want := range frames {
		got, err := DecodeFrame(&buf, dec)
		if err != nil {
			t.Fatalf("decode frame %d: %v", i, err)
		}
		if got.SimTimeUnix != want.SimTimeUnix {
			t.Fatalf("frame %d simtime: got %d want %d", i, got.SimTimeUnix, want.SimTimeUnix)
		}
		if len(got.Tracks) != len(want.Tracks) {
			t.Fatalf("frame %d len: got %d want %d", i, len(got.Tracks), len(want.Tracks))
		}
	}
}
```

- [ ] **Step 1.3: Build + test**

```
go build -tags vulkan ./...
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v
rm replay_test.exe
```
Expected: build clean, both tests pass.

- [ ] **Step 1.4: Commit**

```bash
git add client/replay/format.go client/replay/format_test.go
git commit -m "client/replay: format types + roundtrip tests

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Recorder type

Wraps an open file. Exposes `New`, `AppendFrame`, `Close`. No client-side wiring yet.

**Files:**
- Create: `client/replay/recorder.go`
- Extend: `client/replay/format_test.go`

- [ ] **Step 2.1: Failing test for Recorder**

Append to `client/replay/format_test.go`:

```go
import (
	"os"
	"path/filepath"
	"time"
)

func TestRecorderEndToEnd(t *testing.T) {
	dir := t.TempDir()
	rec, path, err := NewRecorder(dir, "ZNY", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("recorder path %q not in dir %q", path, dir)
	}
	if err := rec.AppendFrame(time.Unix(1700000001, 0), map[av.ADSBCallsign]*sim.Track{
		"AAL1": {RadarTrack: av.RadarTrack{ADSBCallsign: "AAL1"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rec.AppendFrame(time.Unix(1700000002, 0), map[av.ADSBCallsign]*sim.Track{
		"AAL1": {RadarTrack: av.RadarTrack{ADSBCallsign: "AAL1"}},
		"DAL2": {RadarTrack: av.RadarTrack{ADSBCallsign: "DAL2"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	h, err := DecodeHeader(f)
	if err != nil {
		t.Fatal(err)
	}
	if h.Facility != "ZNY" || h.FormatVersion != FormatVersion {
		t.Fatalf("bad header: %+v", h)
	}
	dec := msgpack.NewDecoder(f)
	count := 0
	for {
		_, err := DecodeFrame(f, dec)
		if err != nil {
			break
		}
		count++
	}
	if count != 2 {
		t.Fatalf("want 2 frames, got %d", count)
	}
}
```

- [ ] **Step 2.2: Run test to fail**

```
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v -test.run TestRecorderEndToEnd
rm replay_test.exe
```
Expected: FAIL — `NewRecorder` undefined.

- [ ] **Step 2.3: Implement `client/replay/recorder.go`**

```go
// pkg/client/replay/recorder.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"
)

// Recorder appends frames to an open replay file.
type Recorder struct {
	f    *os.File
	path string
}

// NewRecorder opens (or creates) a replay file in dir for the given facility
// and start time, writes the header, and returns the open Recorder plus the
// chosen path. Returns (nil, "", err) on failure.
func NewRecorder(dir, facility string, startTime time.Time) (*Recorder, string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", fmt.Errorf("mkdir %q: %w", dir, err)
	}
	cleanFacility := strings.ReplaceAll(facility, string(filepath.Separator), "_")
	if cleanFacility == "" {
		cleanFacility = "session"
	}
	name := fmt.Sprintf("%s-%s.bin", cleanFacility, startTime.UTC().Format("20060102-150405"))
	path := filepath.Join(dir, name)
	f, err := os.Create(path)
	if err != nil {
		return nil, "", fmt.Errorf("create %q: %w", path, err)
	}
	h := Header{
		FormatVersion: FormatVersion,
		Facility:      facility,
		StartTimeUnix: startTime.UnixNano(),
		// SerVersion left zero; caller can set if available before AppendFrame.
	}
	if err := EncodeHeader(f, h); err != nil {
		f.Close()
		os.Remove(path)
		return nil, "", fmt.Errorf("write header: %w", err)
	}
	return &Recorder{f: f, path: path}, path, nil
}

// AppendFrame serializes one frame to the file.
func (r *Recorder) AppendFrame(simTime time.Time, tracks map[av.ADSBCallsign]*sim.Track) error {
	if r == nil || r.f == nil {
		return nil
	}
	return EncodeFrame(r.f, Frame{SimTimeUnix: simTime.UnixNano(), Tracks: tracks})
}

// Close flushes and closes the underlying file.
func (r *Recorder) Close() error {
	if r == nil || r.f == nil {
		return nil
	}
	err := r.f.Close()
	r.f = nil
	return err
}
```

- [ ] **Step 2.4: Run test**

```
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v
rm replay_test.exe
```
Expected: all replay tests PASS.

- [ ] **Step 2.5: Commit**

```bash
git add client/replay/recorder.go client/replay/format_test.go
git commit -m "client/replay: Recorder open/append/close

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: Reader + Prune helpers

Loads a replay file into memory; lists replay files mtime-sorted; prunes oldest beyond keep.

**Files:**
- Create: `client/replay/reader.go`
- Create: `client/replay/prune.go`
- Extend: `client/replay/format_test.go`

- [ ] **Step 3.1: Failing test for Load and Prune**

Append to `client/replay/format_test.go`:

```go
func TestLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	rec, path, err := NewRecorder(dir, "ZNY", time.Unix(1700000000, 0))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := rec.AppendFrame(time.Unix(1700000000+int64(i+1), 0), map[av.ADSBCallsign]*sim.Track{
			"AAL1": {RadarTrack: av.RadarTrack{ADSBCallsign: "AAL1"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := rec.Close(); err != nil {
		t.Fatal(err)
	}

	rp, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Header.Facility != "ZNY" {
		t.Fatalf("facility %q", rp.Header.Facility)
	}
	if len(rp.Frames) != 3 {
		t.Fatalf("want 3 frames, got %d", len(rp.Frames))
	}
	if rp.Frames[0].SimTimeUnix != time.Unix(1700000001, 0).UnixNano() {
		t.Fatalf("frame 0 simtime %d", rp.Frames[0].SimTimeUnix)
	}
}

func TestListMostRecent(t *testing.T) {
	dir := t.TempDir()
	mkfile := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}
	mkfile("a.bin", time.Unix(1700000001, 0))
	mkfile("b.bin", time.Unix(1700000002, 0))
	mkfile("c.bin", time.Unix(1700000003, 0))
	mkfile("ignore.txt", time.Unix(1700000004, 0))

	got, err := ListMostRecent(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("want 3 .bin files, got %d", len(got))
	}
	if filepath.Base(got[0].Path) != "c.bin" {
		t.Fatalf("newest should be c.bin, got %q", got[0].Path)
	}
	if filepath.Base(got[2].Path) != "a.bin" {
		t.Fatalf("oldest should be a.bin, got %q", got[2].Path)
	}
}

func TestPruneKeepsNewest(t *testing.T) {
	dir := t.TempDir()
	mkfile := func(name string, mtime time.Time) {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o644)
		os.Chtimes(p, mtime, mtime)
	}
	mkfile("a.bin", time.Unix(1700000001, 0))
	mkfile("b.bin", time.Unix(1700000002, 0))
	mkfile("c.bin", time.Unix(1700000003, 0))
	mkfile("d.bin", time.Unix(1700000004, 0))

	if _, err := Prune(dir, 2); err != nil {
		t.Fatal(err)
	}
	remaining := func() []string {
		ents, _ := os.ReadDir(dir)
		var n []string
		for _, e := range ents {
			n = append(n, e.Name())
		}
		return n
	}()
	gotSet := map[string]bool{}
	for _, n := range remaining {
		gotSet[n] = true
	}
	if !gotSet["c.bin"] || !gotSet["d.bin"] {
		t.Fatalf("expected c.bin+d.bin to survive, got %v", remaining)
	}
	if gotSet["a.bin"] || gotSet["b.bin"] {
		t.Fatalf("expected a.bin+b.bin pruned, got %v", remaining)
	}
}
```

- [ ] **Step 3.2: Run tests to fail**

```
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v -test.run "TestLoad|TestList|TestPrune"
rm replay_test.exe
```
Expected: FAIL — `Load`, `ListMostRecent`, `Prune` undefined.

- [ ] **Step 3.3: Create `client/replay/reader.go`**

```go
// pkg/client/replay/reader.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"fmt"
	"io"
	"os"

	"github.com/vmihailenco/msgpack/v5"
)

// Replay is a fully-loaded replay file.
type Replay struct {
	Header Header
	Frames []Frame // sorted ascending by SimTimeUnix
}

// Load reads the entire replay file at path.
func Load(path string) (*Replay, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}
	defer f.Close()

	h, dec, err := DecodeHeader(f)
	if err != nil {
		return nil, err
	}
	var frames []Frame
	for {
		fr, err := DecodeFrame(f, dec)
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode frame %d: %w", len(frames), err)
		}
		frames = append(frames, fr)
	}
	return &Replay{Header: h, Frames: frames}, nil
}
```

(Note: `DecodeHeader` returns `(Header, *msgpack.Decoder, error)` because msgpack
buffers ahead from `*os.File`; reusing the same decoder for `DecodeFrame` is
required so frame reads pick up where the header left off. The unused `msgpack`
import in this file becomes redundant after this change — remove it.)

- [ ] **Step 3.4: Create `client/replay/prune.go`**

```go
// pkg/client/replay/prune.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FileEntry is a replay file in a directory.
type FileEntry struct {
	Path  string
	MTime time.Time
	Size  int64
}

// ListMostRecent lists *.bin files in dir, newest first.
func ListMostRecent(dir string) ([]FileEntry, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []FileEntry
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".bin") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, FileEntry{
			Path:  filepath.Join(dir, e.Name()),
			MTime: info.ModTime(),
			Size:  info.Size(),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].MTime.After(out[j].MTime)
	})
	return out, nil
}

// Prune deletes the oldest .bin files in dir, keeping at most `keep`. Returns
// the list of deleted paths.
func Prune(dir string, keep int) ([]string, error) {
	if keep < 0 {
		keep = 0
	}
	entries, err := ListMostRecent(dir)
	if err != nil {
		return nil, err
	}
	if len(entries) <= keep {
		return nil, nil
	}
	var deleted []string
	for _, e := range entries[keep:] {
		if err := os.Remove(e.Path); err != nil {
			return deleted, fmt.Errorf("remove %q: %w", e.Path, err)
		}
		deleted = append(deleted, e.Path)
	}
	return deleted, nil
}
```

- [ ] **Step 3.5: Run tests to pass**

```
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v
rm replay_test.exe
```
Expected: all pass.

- [ ] **Step 3.6: Commit**

```bash
git add client/replay/reader.go client/replay/prune.go client/replay/format_test.go
git commit -m "client/replay: Load, ListMostRecent, Prune

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: Wire Recorder into ControlClient lifecycle

Adds the `recorder` field, hooks `GetUpdates` to call `AppendFrame`, hooks `Disconnect` to `Close`. Recorder is constructed by the caller and passed in via a setter.

**Files:**
- Modify: `client/client.go`

- [ ] **Step 4.1: Add `recorder` field + setter**

In `client/client.go`, find the `type ControlClient struct {` block and add a new field at the end:

```go
recorder *replay.Recorder
```

Add the import: `"github.com/mmp/vice/client/replay"`. (Same package; this works even though `replay` is a subpackage of `client`.)

Below `NewControlClient`, add:

```go
// SetRecorder attaches an open Recorder. Pass nil to detach (e.g., on settings
// toggle off mid-session). Safe to call before connect.
func (c *ControlClient) SetRecorder(r *replay.Recorder) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.recorder = r
}
```

- [ ] **Step 4.2: Hook AppendFrame in GetUpdates**

In `GetUpdates`, after the existing block:

```go
if updateCallFinished != nil {
    updateCallFinished.InvokeCallback(eventStream, &c.State)
}
for _, call := range completedCalls {
    call.InvokeCallback(eventStream, &c.State)
}
```

…add (still outside the lock):

```go
// Append a replay frame whenever new state was applied this cycle.
if c.recorder != nil && (updateCallFinished != nil || len(completedCalls) > 0) {
    if err := c.recorder.AppendFrame(c.State.SimTime.Time(), c.State.Tracks); err != nil {
        c.lg.Warnf("replay: AppendFrame failed (recording stops): %v", err)
        c.recorder.Close()
        c.recorder = nil
    }
}
```

Note: `c.State.SimTime.Time()` may need adjustment — verify the `sim.Time` API. Grep `sim/time.go` for the method that returns a `time.Time` value (likely `Time()` or `WallClock()`). Use the one that already exists.

- [ ] **Step 4.3: Hook Disconnect**

In `Disconnect()`, before the existing nil-out block, add:

```go
if c.recorder != nil {
    c.recorder.Close()
    c.recorder = nil
}
```

- [ ] **Step 4.4: Build**

```
go build -tags vulkan ./...
```
Expected: clean.

- [ ] **Step 4.5: Commit**

```bash
git add client/client.go
git commit -m "client: optional Recorder for session replay; AppendFrame on state change

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: Config fields + settings UI

**Files:**
- Modify: `cmd/vice/config.go`
- Modify: `cmd/vice/ui.go`

- [ ] **Step 5.1: Add Config fields**

In `cmd/vice/config.go`, locate the `ConfigNoSim` struct. Add three fields beside the existing `ShowMap`:

```go
RecordReplay     bool
AutoPruneReplays bool
ReplayKeepCount  int
```

In `getDefaultConfig`, beside `ShowMap: false`:

```go
RecordReplay:     false,
AutoPruneReplays: false,
ReplayKeepCount:  10,
```

- [ ] **Step 5.2: Add settings section**

In `cmd/vice/ui.go` `uiDrawSettingsWindow`, find the existing `if imgui.CollapsingHeaderBoolPtr("Display", nil) { ... }` block. After it, before the next existing CollapsingHeader, add:

```go
if imgui.CollapsingHeaderBoolPtr("Session Replay", nil) {
    imgui.Checkbox("Record this session", &config.RecordReplay)
    imgui.Checkbox("Auto-prune old replays at startup", &config.AutoPruneReplays)
    if config.AutoPruneReplays {
        if config.ReplayKeepCount < 1 {
            config.ReplayKeepCount = 1
        }
        imgui.SetNextItemWidth(80)
        imgui.InputIntV("Keep most recent", &config.ReplayKeepCount, 1, 5, 0)
        if config.ReplayKeepCount < 1 {
            config.ReplayKeepCount = 1
        }
    }
    imgui.TextDisabled("Files: ~/.vice/replays/")
}
```

(`imgui.InputIntV` signature is `(label, valuePtr, step, stepFast, flags)`. Verify in cimgui-go; if the signature differs, adapt minimally.)

- [ ] **Step 5.3: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 5.4: Commit**

```bash
git add cmd/vice/config.go cmd/vice/ui.go
git commit -m "cmd/vice: settings toggles for record + auto-prune session replays

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Wire recorder construction at connect time + prune at startup

When the user connects to a sim and `config.RecordReplay` is true, construct a `Recorder` and attach it via `SetRecorder`. Run prune at startup if `config.AutoPruneReplays`.

**Files:**
- Modify: `cmd/vice/main.go`

- [ ] **Step 6.1: Helper for replay directory**

In `cmd/vice/main.go`, add at the top of the file (after imports):

```go
func replayDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".vice", "replays")
	}
	return filepath.Join(home, ".vice", "replays")
}
```

Add `"path/filepath"` to the imports if missing.

- [ ] **Step 6.2: Prune at startup**

Find the place in `main` (or `runGUI`) where `config` has been loaded — search for `LoadOrMakeDefaultConfig`. After that returns successfully, add:

```go
if config.AutoPruneReplays {
    if deleted, err := replay.Prune(replayDir(), config.ReplayKeepCount); err != nil {
        lg.Warnf("replay prune: %v", err)
    } else if len(deleted) > 0 {
        lg.Infof("replay prune: deleted %d old files", len(deleted))
    }
}
```

Add `"github.com/mmp/vice/client/replay"` to the imports.

- [ ] **Step 6.3: Construct recorder on connect**

The connection flow lives in `client/connectmgr.go` and `cmd/vice/dialogs.go`. The simplest hook: wherever the `ControlClient` becomes available (search `runGUI` for `mgr.ControlClient()` or the equivalent), add right after:

```go
if controlClient != nil && config.RecordReplay && controlClient.GetRecorder() == nil {
    rec, path, err := replay.NewRecorder(replayDir(), controlClient.State.Facility, time.Now())
    if err != nil {
        lg.Warnf("replay: failed to start recording: %v", err)
    } else {
        lg.Infof("replay: recording to %s", path)
        controlClient.SetRecorder(rec)
    }
}
```

Verify the `controlClient` accessor name. It may be `mgr.ControlClient` (a method) or `mgr.controlClient` (a field). Add:

```go
// in client/client.go
func (c *ControlClient) GetRecorder() *replay.Recorder {
    c.mu.Lock()
    defer c.mu.Unlock()
    return c.recorder
}
```

- [ ] **Step 6.4: Build + commit**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

```bash
git add cmd/vice/main.go client/client.go
git commit -m "cmd/vice: prune replays at startup; start recorder on connect

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

- [ ] **Step 6.5: Smoke test recording (manual)**

Build and run vice. In settings → Session Replay → enable "Record this session". Start a scenario, let it run for ~30 seconds, disconnect. Verify a `<facility>-*.bin` file appears in `~/.vice/replays/`. (No replay viewer yet — just verify the file is being written.)

---

## Task 7: TrackSource interface + LiveTrackSource adapter

The load-bearing refactor. Replace `*client.ControlClient` parameters in MapPane drawers with `TrackSource`. Provide a `LiveTrackSource` that wraps the live client.

**Files:**
- Create: `panes/trackdata.go`
- Modify: `panes/mappane.go`, `panes/mappane_aircraft.go`, `panes/mappane_overlays.go`, `panes/mappane_selection.go`

- [ ] **Step 7.1: Create `panes/trackdata.go`**

```go
// pkg/panes/trackdata.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package panes

import (
	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/sim"
)

// TrackSource is the minimal interface MapPane consumes. Implemented by
// LiveTrackSource (live client) and ReplayPlayer (replay viewer).
type TrackSource interface {
	Connected() bool
	Tracks() map[av.ADSBCallsign]*sim.Track
	UserTCW() sim.TCW
	NmPerLongitude() float32
	Facility() string
	Airports() map[string]*av.Airport
	Controllers() map[sim.ControlPosition]*av.Controller
}

// LiveTrackSource adapts a *client.ControlClient to TrackSource.
type LiveTrackSource struct {
	C *client.ControlClient
}

func (l LiveTrackSource) Connected() bool {
	return l.C != nil && l.C.Connected()
}
func (l LiveTrackSource) Tracks() map[av.ADSBCallsign]*sim.Track {
	if l.C == nil {
		return nil
	}
	return l.C.State.Tracks
}
func (l LiveTrackSource) UserTCW() sim.TCW {
	if l.C == nil {
		return ""
	}
	return l.C.State.UserTCW
}
func (l LiveTrackSource) NmPerLongitude() float32 {
	if l.C == nil {
		return 45.5
	}
	return l.C.State.NmPerLongitude
}
func (l LiveTrackSource) Facility() string {
	if l.C == nil {
		return ""
	}
	return l.C.State.Facility
}
func (l LiveTrackSource) Airports() map[string]*av.Airport {
	if l.C == nil {
		return nil
	}
	return l.C.State.Airports
}
func (l LiveTrackSource) Controllers() map[sim.ControlPosition]*av.Controller {
	if l.C == nil {
		return nil
	}
	return l.C.State.Controllers
}
```

- [ ] **Step 7.2: Refactor MapPane drawers — Step 1: signatures**

Change every drawer in `panes/mappane*.go` that takes `c *client.ControlClient` to take `src TrackSource` instead. The list:

- `MapPane.DrawWindow`: keep `*client.ControlClient` for the menu-bar caller (unchanged) — but inside, immediately wrap as `LiveTrackSource{C: c}` and pass that to `drawCanvas`. Signature: `DrawWindow(show *bool, c *client.ControlClient, p platform.Platform, ...)` becomes `DrawWindow(show *bool, src TrackSource, p platform.Platform, ...)`.
- `drawCanvas(c *client.ControlClient, ...)` → `drawCanvas(src TrackSource, ...)`.
- `drawToolbar(c *client.ControlClient)` → `drawToolbar(src TrackSource)`.
- `drawFacilityBoundary(c, ...)` → `drawFacilityBoundary(src, ...)`.
- `drawAirportLabels(c, ...)` → `drawAirportLabels(src, ...)`.
- `drawAircraft(c, ...)` → `drawAircraft(src, ...)`.
- `findHoveredAircraft(c, ...)` → `findHoveredAircraft(src, ...)`.
- `handleSelection(c, ...)` → `handleSelection(src, ...)`.
- `drawSelectedRoute(c, ...)` → `drawSelectedRoute(src, ...)`.
- `drawHoverTooltip(c)` → `drawHoverTooltip(src)`.
- `drawCornerInfoPanel(c)` → `drawCornerInfoPanel(src)`.
- `updateTrails(c)` → `updateTrails(src)`.

Inside each function, replace:

| old | new |
|-----|-----|
| `c == nil \|\| !c.Connected()` | `!src.Connected()` |
| `c.State.Tracks` | `src.Tracks()` |
| `c.State.UserTCW` | `src.UserTCW()` |
| `c.State.NmPerLongitude` | `src.NmPerLongitude()` |
| `c.State.Facility` | `src.Facility()` |
| `c.State.Airports` | `src.Airports()` |
| `c.State.Controllers` | `src.Controllers()` |

In `drawCanvas`, the `nmPerLon` resolution becomes:

```go
nmPerLon := src.NmPerLongitude()
if nmPerLon == 0 {
    nmPerLon = defaultNmPerLongitude
}
```

In `mappane_overlays.go drawFacilityBoundary`, replace the `av.DB.LookupFacility(c.State.Facility)` lookup with `av.DB.LookupFacility(src.Facility())`.

- [ ] **Step 7.3: Update the call site in `cmd/vice/ui.go`**

In `cmd/vice/ui.go`, find:

```go
config.MapPane.DrawWindow(&ui.showMap, controlClient, p, config.UnpinnedWindows, lg)
```

Wrap the client:

```go
config.MapPane.DrawWindow(&ui.showMap, panes.LiveTrackSource{C: controlClient}, p, config.UnpinnedWindows, lg)
```

- [ ] **Step 7.4: Build + run tests**

```
go build -tags vulkan ./cmd/vice
go test -c -tags vulkan -o panes_test.exe ./panes/
./panes_test.exe -test.v
rm panes_test.exe
```
Expected: build clean, all panes tests still pass.

- [ ] **Step 7.5: Smoke test live mode (manual)**

Run vice, connect, open the Map window. Everything should work exactly as before — basemap, boundary, airports, aircraft glyphs, hover, click, info panel, trail, route, filter combo. If anything regressed, the refactor missed a spot.

- [ ] **Step 7.6: Commit**

```bash
git add panes/trackdata.go panes/mappane*.go cmd/vice/ui.go
git commit -m "panes: factor TrackSource interface; LiveTrackSource adapter

Drawers no longer take *client.ControlClient directly. The live
caller wraps the client in LiveTrackSource. This unblocks the
replay player having a parallel implementation.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 8: ReplayPlayer

Implements `TrackSource` from a loaded `*replay.Replay`. Has `Tick(time.Time)` to advance based on wall-clock × speed, `SeekTo(int)`, `Step(delta int)`, plus play/pause and speed.

**Files:**
- Create: `panes/mappane_replay.go`
- Extend: `panes/mappane_test.go`

- [ ] **Step 8.1: Failing test for ReplayPlayer.Tick**

Append to `panes/mappane_test.go`:

```go
import (
	// add to existing imports if not present:
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/client/replay"
)

func TestReplayPlayerTickAdvances(t *testing.T) {
	// Synthesize a replay with frames at t=0,1,2,3 seconds (in unix nanos).
	base := time.Unix(1700000000, 0)
	rp := &replay.Replay{
		Header: replay.Header{StartTimeUnix: base.UnixNano()},
		Frames: []replay.Frame{
			{SimTimeUnix: base.UnixNano() + 0, Tracks: map[av.ADSBCallsign]*sim.Track{}},
			{SimTimeUnix: base.UnixNano() + int64(time.Second), Tracks: map[av.ADSBCallsign]*sim.Track{}},
			{SimTimeUnix: base.UnixNano() + int64(2*time.Second), Tracks: map[av.ADSBCallsign]*sim.Track{}},
			{SimTimeUnix: base.UnixNano() + int64(3*time.Second), Tracks: map[av.ADSBCallsign]*sim.Track{}},
		},
	}
	p := NewReplayPlayer(rp)
	p.SetPlaying(true)
	p.SetSpeed(1.0)

	wallStart := time.Unix(2_000_000_000, 0)
	p.SetWallReference(wallStart, 0)
	// Advance wall by 1.5s; expect frame index = 1 (1.5s past frame 1, before frame 2).
	p.Tick(wallStart.Add(1500 * time.Millisecond))
	if got := p.CurFrame(); got != 1 {
		t.Fatalf("after 1.5s want cur=1, got %d", got)
	}
	// Another 1s → 2.5s total → cur=2.
	p.Tick(wallStart.Add(2500 * time.Millisecond))
	if got := p.CurFrame(); got != 2 {
		t.Fatalf("after 2.5s want cur=2, got %d", got)
	}
}
```

- [ ] **Step 8.2: Run test to fail**

```
go test -c -tags vulkan -o panes_test.exe ./panes/
./panes_test.exe -test.v -test.run TestReplayPlayer
rm panes_test.exe
```
Expected: FAIL — `NewReplayPlayer` undefined.

- [ ] **Step 8.3: Implement `panes/mappane_replay.go`**

```go
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
	if idx >= len(p.rp.Frames) {
		idx = len(p.rp.Frames) - 1
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

// CurrentSimTime returns the wall-time of the current frame, or the
// recording's start time if the replay is empty.
func (p *ReplayPlayer) CurrentSimTime() time.Time {
	if p.rp == nil || len(p.rp.Frames) == 0 {
		return time.Time{}
	}
	return time.Unix(0, p.rp.Frames[p.cur].SimTimeUnix)
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
func (p *ReplayPlayer) UserTCW() sim.TCW       { return "" } // not recorded
func (p *ReplayPlayer) NmPerLongitude() float32 { return 45.5 }
func (p *ReplayPlayer) Facility() string         { if p.rp == nil { return "" }; return p.rp.Header.Facility }
func (p *ReplayPlayer) Airports() map[string]*av.Airport {
	// Replay state didn't capture airports; the Map will fall back to
	// rendering nothing for airport labels in replay mode (graceful degrade).
	return nil
}
func (p *ReplayPlayer) Controllers() map[sim.ControlPosition]*av.Controller {
	return nil
}

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
```

- [ ] **Step 8.4: Run test to pass**

```
go test -c -tags vulkan -o panes_test.exe ./panes/
./panes_test.exe -test.v -test.run TestReplayPlayer
rm panes_test.exe
```
Expected: PASS.

- [ ] **Step 8.5: Wire timeline bar into MapPane**

In `panes/mappane.go` `DrawWindow`, after `mp.drawToolbar(src)` and before `mp.drawCanvas(src, p, lg)`, add:

```go
// Replay mode: render the timeline bar above the canvas.
if rp, ok := src.(*ReplayPlayer); ok {
    rp.Tick(time.Now())
    rp.DrawTimelineBar()
}
```

(Add `"time"` to the imports of `mappane.go` if needed.)

- [ ] **Step 8.6: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 8.7: Commit**

```bash
git add panes/mappane.go panes/mappane_replay.go panes/mappane_test.go
git commit -m "panes: ReplayPlayer (TrackSource impl) + timeline bar UI

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 9: Connect dialog buttons + replay opening flow

**Files:**
- Modify: `cmd/vice/dialogs.go`
- Modify: `cmd/vice/ui.go`
- Create: `cmd/vice/replaydialog.go`

- [ ] **Step 9.1: Add "Replay last session" button to ScenarioSelectionModalClient**

In `cmd/vice/dialogs.go` `ScenarioSelectionModalClient.Buttons()`, before the `next` button, append the two replay buttons:

```go
b = append(b, ModalDialogButton{
    text: "Replay last session",
    action: func() bool {
        entries, _ := replay.ListMostRecent(replayDir())
        if len(entries) == 0 {
            uiShowModalDialog(NewModalDialogBox(
                &MessageModalClient{title: "No replays", message: "No replay files found in ~/.vice/replays/"},
                c.platform), true)
            return false
        }
        rp, err := replay.Load(entries[0].Path)
        if err != nil {
            uiShowModalDialog(NewModalDialogBox(
                &MessageModalClient{title: "Replay error", message: err.Error()},
                c.platform), true)
            return false
        }
        ui.replayPlayer = panes.NewReplayPlayer(rp)
        ui.showMap = true
        return true
    },
})
b = append(b, ModalDialogButton{
    text: "Replay session…",
    action: func() bool {
        picker := &ReplayPickerModalClient{platform: c.platform, lg: c.lg}
        uiShowModalDialog(NewModalDialogBox(picker, c.platform), false)
        return true
    },
})
```

Add the imports `"github.com/mmp/vice/client/replay"` and `"github.com/mmp/vice/panes"` to `dialogs.go`. Note: `replayDir()` is defined in `main.go`; it's package-level so it's accessible.

- [ ] **Step 9.2: Create `cmd/vice/replaydialog.go`**

```go
// cmd/vice/replaydialog.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package main

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/mmp/vice/client/replay"
	"github.com/mmp/vice/log"
	"github.com/mmp/vice/panes"
	"github.com/mmp/vice/platform"

	"github.com/AllenDang/cimgui-go/imgui"
)

// ReplayPickerModalClient renders the file-picker for "Replay session…".
type ReplayPickerModalClient struct {
	platform platform.Platform
	lg       *log.Logger
	entries  []replay.FileEntry
	chosen   int // -1 if none
	err      error
}

func (c *ReplayPickerModalClient) Title() string { return "Replay session" }

func (c *ReplayPickerModalClient) Opening() {
	c.chosen = -1
	c.entries, c.err = replay.ListMostRecent(replayDir())
}

func (c *ReplayPickerModalClient) Buttons() []ModalDialogButton {
	return []ModalDialogButton{
		{text: "Cancel"},
		{
			text:     "Open",
			disabled: c.chosen < 0 || c.chosen >= len(c.entries),
			action: func() bool {
				rp, err := replay.Load(c.entries[c.chosen].Path)
				if err != nil {
					c.err = err
					return false
				}
				ui.replayPlayer = panes.NewReplayPlayer(rp)
				ui.showMap = true
				return true
			},
		},
	}
}

func (c *ReplayPickerModalClient) Draw() int {
	if c.err != nil {
		imgui.TextColored(imgui.Vec4{X: 1, Y: 0.4, Z: 0.4, W: 1}, c.err.Error())
		imgui.Separator()
	}
	if len(c.entries) == 0 {
		imgui.TextDisabled("No replay files in ~/.vice/replays/")
		return -1
	}
	for i, e := range c.entries {
		label := fmt.Sprintf("%s   (%s, %.1f MB)",
			filepath.Base(e.Path),
			e.MTime.Local().Format(time.RFC822),
			float64(e.Size)/1e6)
		if imgui.SelectableBoolV(label, c.chosen == i, 0, imgui.Vec2{}) {
			c.chosen = i
		}
	}
	return -1
}
```

- [ ] **Step 9.3: Add replayPlayer field to ui struct + drive into MapPane**

In `cmd/vice/ui.go`, in the `ui struct {` block near the top, add:

```go
replayPlayer *panes.ReplayPlayer
```

Add `"github.com/mmp/vice/panes"` import if not present.

In the `if ui.showMap { ... }` block of `uiDraw`, replace:

```go
config.MapPane.DrawWindow(&ui.showMap, panes.LiveTrackSource{C: controlClient}, p, config.UnpinnedWindows, lg)
```

with:

```go
var src panes.TrackSource
if ui.replayPlayer != nil {
    src = ui.replayPlayer
} else {
    src = panes.LiveTrackSource{C: controlClient}
}
config.MapPane.DrawWindow(&ui.showMap, src, p, config.UnpinnedWindows, lg)
```

- [ ] **Step 9.4: Build**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

- [ ] **Step 9.5: Commit**

```bash
git add cmd/vice/dialogs.go cmd/vice/replaydialog.go cmd/vice/ui.go
git commit -m "cmd/vice: connect-dialog 'Replay last session' + 'Replay session…' picker

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Task 10: Close replay on live-sim connect

When the user starts a new sim while a replay is open, close the replay first.

**Files:**
- Modify: `cmd/vice/ui.go` (or wherever the post-connect callback lives)

- [ ] **Step 10.1: Hook the connect path**

Search the codebase for where a successful connect transitions the UI to live mode — typically in `ConfigurationModalClient.Buttons()` action, or `simConfig.Start(...)`. After the call that triggers a connect, add:

```go
ui.replayPlayer = nil
```

Where exactly: search `cmd/vice/dialogs.go` and `cmd/vice/simconfig.go` for `simConfig.Start(`. Place the reset immediately before that call (so a connect always wipes out any pending replay).

- [ ] **Step 10.2: Build + commit**

```
go build -tags vulkan ./cmd/vice
```
Expected: clean.

```bash
git add cmd/vice/dialogs.go cmd/vice/simconfig.go
git commit -m "cmd/vice: close any open replay when a live sim is started

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

(If only one of the two files was actually touched, omit the other from `git add`.)

---

## Task 11: Final pass + push

- [ ] **Step 11.1: Run all tests**

```
go test -c -tags vulkan -o replay_test.exe ./client/replay/
./replay_test.exe -test.v
rm replay_test.exe
go test -c -tags vulkan -o panes_test.exe ./panes/
./panes_test.exe -test.v
rm panes_test.exe
go build -tags vulkan ./cmd/vice
```
Expected: all green.

- [ ] **Step 11.2: Manual checklist**

- [ ] Settings → "Session Replay" section shows. Toggling "Record this session" + "Auto-prune old replays" updates config.
- [ ] Connect to a scenario with recording enabled. Run for ~1 min. Disconnect. Verify a `<facility>-*.bin` exists in `~/.vice/replays/`.
- [ ] Restart vice. Click "Replay last session" on the entry dialog. Map opens, timeline at the bottom, play/pause works, scrub works, speed combo (0.25× through 8×) changes pace, step buttons advance/back one frame.
- [ ] During replay, hover an aircraft → yellow ring + tooltip. Click → cyan ring + top-right info panel.
- [ ] Filter combo works (All / Untracked / Tracked / My TCW / Specific TCW).
- [ ] Past trail and future route render for selected aircraft (route may be empty if `Track.Route` was empty at the recorded moment — that's fine).
- [ ] "Replay session…" picker opens, shows .bin files newest-first with timestamp + size, selecting and clicking Open loads that one.
- [ ] Click "Connect" while a replay is open → replay closes, sim starts, no crash.
- [ ] With auto-prune enabled and keep=2, restart vice. Files beyond the most recent 2 are deleted.
- [ ] Settings → toggle "Record this session" off → run a session → no new file appears.
- [ ] Disconnect while recording → file is closed cleanly (decode it via the test exe to confirm).

- [ ] **Step 11.3: Push**

```bash
git push -u origin session-replay
```

- [ ] **Step 11.4: Save memory note**

Path: `C:\Users\judlo\.claude\projects\C--Users-judlo-Documents-vice-vice\memory\session_replay_branch.md`

```markdown
---
name: Session-replay branch state
description: session-replay @<sha> — opt-in per-tick recording of sim.Track snapshots; replay viewer reuses MapPane via TrackSource interface; settings toggles for record + auto-prune; connect dialog has Replay last session + Replay session… picker. Local-only (not for upstream PR).
type: project
---

`session-replay` @<sha> (pushed to origin) — branched off map-window. Adds:
- `client/replay/` package: Recorder, Reader, Prune, msgpack format with version header.
- ControlClient hooked at GetUpdates to AppendFrame each state change; closed on Disconnect.
- panes.TrackSource interface + LiveTrackSource adapter; MapPane drawers refactored to consume it.
- panes.ReplayPlayer implements TrackSource; timeline bar (play/pause/scrub/speed/step).
- Settings UI: "Record this session" + "Auto-prune old replays" + keep-count.
- Connect dialog: "Replay last session" quick button + "Replay session…" file picker.
- Auto-prune at startup deletes oldest .bin in ~/.vice/replays/ beyond keep-count.

Why: user wanted to record a session and scrub through it on the Map afterward.
How to apply: spec at docs/superpowers/specs/2026-04-29-session-replay-design.md;
plan at docs/superpowers/plans/2026-04-29-session-replay.md.
```

Also append to `MEMORY.md`:

```markdown
- [Session-replay branch state](session_replay_branch.md) — `session-replay` @<sha> (pushed to origin) — opt-in recording + replay viewer reusing MapPane.
```

---

## Self-review

**Spec coverage:**

| Spec section | Covered by |
|---|---|
| Settings: record toggle | Task 5 |
| Settings: auto-prune toggle + keep count | Task 5 |
| Connect dialog: Replay last session | Task 9.1 |
| Connect dialog: Replay session… picker | Task 9.1, 9.2 |
| Recorder format + msgpack stream | Task 1 |
| Recorder: open / append / close | Task 2 |
| Reader / Load | Task 3 |
| Prune helper | Task 3 |
| Recorder ↔ ControlClient lifecycle | Task 4 |
| Auto-prune at startup | Task 6 |
| Recorder constructed on connect | Task 6 |
| TrackSource interface | Task 7 |
| LiveTrackSource adapter | Task 7 |
| ReplayPlayer with Tick + SeekTo + Step | Task 8 |
| Timeline bar UI | Task 8.5 |
| Connect-closes-replay | Task 10 |
| Manual + unit tests | Tasks 1, 2, 3, 8 (unit); Task 11 (manual) |

**Placeholder scan:** No "TBD"/"TODO"/"add error handling". Two flagged spots where the engineer must verify a method name (`SimTime.Time()` in Task 4.2, `simConfig.Start(` location in Task 10.1) — both with explicit instructions for how to verify.

**Type consistency:** `TrackSource` methods are consistent across `panes/trackdata.go`, `panes/mappane_replay.go`, and the refactored drawers. `replay.Header` / `replay.Frame` / `replay.Replay` types are used consistently. `ReplayPlayer` is the public name everywhere; matches the field on the `ui` struct.

**Scope:** single feature, single branch, single plan.

---

**Plan complete and saved to `docs/superpowers/plans/2026-04-29-session-replay.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
