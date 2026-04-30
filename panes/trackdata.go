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
// LiveTrackSource (live client) and ReplayPlayer (replay viewer, Task 8).
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
