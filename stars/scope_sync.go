// stars/scope_sync.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/panes"
)

// scopeRange returns the effective scope range. When the client opted
// into scope-view sync at sign-on, reads come from the shared
// TCWDisplay.ScopeView if it has been seeded; otherwise the local
// preference. When sync is off, always the local preference.
func (sp *STARSPane) scopeRange(c *client.ControlClient) float32 {
	ps := sp.currentPrefs()
	if c != nil && c.SyncScopeState {
		if d := c.State.TCWDisplay; d != nil && d.ScopeView.Range != 0 {
			return d.ScopeView.Range
		}
	}
	return ps.Range
}

func (sp *STARSPane) scopeUserCenter(c *client.ControlClient) math.Point2LL {
	ps := sp.currentPrefs()
	if c != nil && c.SyncScopeState {
		if d := c.State.TCWDisplay; d != nil && !d.ScopeView.UserCenter.IsZero() {
			return d.ScopeView.UserCenter
		}
	}
	return ps.UserCenter
}

func (sp *STARSPane) scopeRangeRingRadius(c *client.ControlClient) int {
	ps := sp.currentPrefs()
	if c != nil && c.SyncScopeState {
		if d := c.State.TCWDisplay; d != nil && d.ScopeView.RangeRingRadius != 0 {
			return d.ScopeView.RangeRingRadius
		}
	}
	return ps.RangeRingRadius
}

// setScopeRange routes a range mutation to either the shared TCWDisplay
// (when sync is on) or the local preference (when off). The shared path
// is fire-and-forget; the echoed SimStateUpdate will refresh
// State.TCWDisplay.ScopeView on the next poll.
func (sp *STARSPane) setScopeRange(ctx *panes.Context, v float32) {
	if ctx.Client != nil && ctx.Client.SyncScopeState {
		ctx.Client.SetTCWRange(v, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().Range = v
}

func (sp *STARSPane) setScopeUserCenter(ctx *panes.Context, p math.Point2LL) {
	if ctx.Client != nil && ctx.Client.SyncScopeState {
		ctx.Client.SetTCWUserCenter(p, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().UserCenter = p
}

func (sp *STARSPane) setScopeRangeRingRadius(ctx *panes.Context, v int) {
	if ctx.Client != nil && ctx.Client.SyncScopeState {
		ctx.Client.SetTCWRangeRingRadius(v, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().RangeRingRadius = v
}
