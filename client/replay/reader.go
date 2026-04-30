// client/replay/reader.go
// Copyright(c) 2022-2024 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package replay

import (
	"fmt"
	"io"
	"os"
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
	if h.FormatVersion != FormatVersion {
		return nil, fmt.Errorf("replay format version %d unsupported (expected %d)", h.FormatVersion, FormatVersion)
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
