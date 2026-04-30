// client/replay/format.go
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
