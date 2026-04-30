// client/replay/format_test.go
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
