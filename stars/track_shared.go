// stars/track_shared.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"github.com/mmp/vice/panes"
	"github.com/mmp/vice/sim"
)

// SharedTrackAnnotationFields is the authoritative list of
// sim.TrackAnnotations field names that are synced across relief
// controllers. Keep in sync with the struct in sim/tcw_display.go; a
// reflective test guards against drift.
var SharedTrackAnnotationFields = []string{
	"JRingRadius",
	"ConeLength",
	"LeaderLineDirection",
	"FDAMLeaderLineDirection",
	"UseGlobalLeaderLine",
	"DisplayFDB",
	"DisplayPTL",
	"DisplayTPASize",
	"DisplayATPAMonitor",
	"DisplayATPAWarnAlert",
	"DisplayRequestedAltitude",
	"DisplayLDBBeaconCode",
}

// annotations returns the shared TCW annotations for the given ACID,
// or a zero-value TrackAnnotations if no entry exists. Callers read
// fields unconditionally; the zero value is the semantic default for
// every synced field.
func (sp *STARSPane) annotations(ctx *panes.Context, acid sim.ACID) sim.TrackAnnotations {
	d := ctx.Client.State.TCWDisplay
	if d == nil || d.Annotations == nil {
		return sim.TrackAnnotations{}
	}
	return d.Annotations[acid]
}

// annotationsForTrack returns the shared TCW annotations for an
// associated track, or a zero-value TrackAnnotations for unassociated
// tracks (which have no ACID to key on).
func (sp *STARSPane) annotationsForTrack(ctx *panes.Context, trk sim.Track) sim.TrackAnnotations {
	if !trk.IsAssociated() {
		return sim.TrackAnnotations{}
	}
	return sp.annotations(ctx, trk.FlightPlan.ACID)
}
