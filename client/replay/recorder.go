// client/replay/recorder.go
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
func NewRecorder(dir, facility string, startTime time.Time, serVersion int) (*Recorder, string, error) {
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
		SerVersion:    serVersion,
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
