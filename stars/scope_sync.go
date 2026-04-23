// stars/scope_sync.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/panes"
)

// scopeSyncActive reports whether the client is currently at a TCW
// that has flipped to shared-scope mode. The flag is server-owned and
// arrives via SimStateUpdate; it is sticky once enabled. A relief
// joining with the "Sync Scope Setup" checkbox turns it on for every
// controller at the TCW, including the primary.
func scopeSyncActive(c *client.ControlClient) bool {
	if c == nil {
		return false
	}
	d := c.State.TCWDisplay
	return d != nil && d.ScopeSyncEnabled
}

// scopeRange returns the effective scope range. Under shared-scope
// mode, reads come from TCWDisplay.ScopeView when it has been seeded;
// otherwise the local preference is used as a fallback so the scope
// never jumps to a zero value before the first shared write.
func (sp *STARSPane) scopeRange(c *client.ControlClient) float32 {
	ps := sp.currentPrefs()
	if scopeSyncActive(c) && c.State.TCWDisplay.ScopeView.Range != 0 {
		return c.State.TCWDisplay.ScopeView.Range
	}
	return ps.Range
}

func (sp *STARSPane) scopeUserCenter(c *client.ControlClient) math.Point2LL {
	ps := sp.currentPrefs()
	if scopeSyncActive(c) && !c.State.TCWDisplay.ScopeView.UserCenter.IsZero() {
		return c.State.TCWDisplay.ScopeView.UserCenter
	}
	return ps.UserCenter
}

func (sp *STARSPane) scopeRangeRingRadius(c *client.ControlClient) int {
	ps := sp.currentPrefs()
	if scopeSyncActive(c) && c.State.TCWDisplay.ScopeView.RangeRingRadius != 0 {
		return c.State.TCWDisplay.ScopeView.RangeRingRadius
	}
	return ps.RangeRingRadius
}

// setScopeRange routes a range mutation to either the shared TCWDisplay
// (when the TCW is in shared-scope mode) or the local preference. The
// shared path is fire-and-forget; the echoed SimStateUpdate refreshes
// State.TCWDisplay.ScopeView on the next poll.
func (sp *STARSPane) setScopeRange(ctx *panes.Context, v float32) {
	if scopeSyncActive(ctx.Client) {
		ctx.Client.SetTCWRange(v, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().Range = v
}

func (sp *STARSPane) setScopeUserCenter(ctx *panes.Context, p math.Point2LL) {
	if scopeSyncActive(ctx.Client) {
		ctx.Client.SetTCWUserCenter(p, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().UserCenter = p
}

func (sp *STARSPane) setScopeRangeRingRadius(ctx *panes.Context, v int) {
	if scopeSyncActive(ctx.Client) {
		ctx.Client.SetTCWRangeRingRadius(v, func(err error) { sp.displayError(err, ctx, "") })
		return
	}
	sp.currentPrefs().RangeRingRadius = v
}
