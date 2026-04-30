// client/replay/format_test.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"
)

func TestHeaderRoundtrip(t *testing.T) {
	in := Header{FormatVersion: FormatVersion, Facility: "ZNY", StartTimeUnix: 1700000000000000000, SerVersion: 42}
	var buf bytes.Buffer
	if err := EncodeHeader(&buf, in); err != nil {
		t.Fatal(err)
	}
	out, _, err := DecodeHeader(&buf)
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

	_, dec, err := DecodeHeader(&buf)
	if err != nil {
		t.Fatal(err)
	}

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

func TestRecorderEndToEnd(t *testing.T) {
	dir := t.TempDir()
	rec, path, err := NewRecorder(dir, "ZNY", time.Unix(1700000000, 0), 0)
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

	h, dec, err := DecodeHeader(f)
	if err != nil {
		t.Fatal(err)
	}
	if h.Facility != "ZNY" || h.FormatVersion != FormatVersion {
		t.Fatalf("bad header: %+v", h)
	}
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

func TestLoadRoundtrip(t *testing.T) {
	dir := t.TempDir()
	rec, path, err := NewRecorder(dir, "ZNY", time.Unix(1700000000, 0), 0)
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
