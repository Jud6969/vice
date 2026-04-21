// nav/conditional.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package nav

import (
	"math/rand/v2"

	av "github.com/mmp/vice/aviation"
)

// ConditionalKind identifies which altitude-event triggers the deferred action.
type ConditionalKind uint8

const (
	// ConditionalLeaving fires once the aircraft's altitude has passed the
	// trigger by more than a small tolerance in the direction of current
	// vertical motion.
	ConditionalLeaving ConditionalKind = iota

	// ConditionalReaching fires on first contact within 100 ft of the trigger
	// altitude, regardless of vertical rate.
	ConditionalReaching
)

// ConditionalAction is the deferred action to execute when a LV/RC trigger
// fires. Concrete types cover the closed set of supported inner commands
// (heading, direct-fix, speed, mach).
type ConditionalAction interface {
	// Execute mutates nav to carry out the deferred action. Called with the
	// PendingConditionalCommand slot already cleared, so re-entry is safe.
	Execute(nav *Nav, simTime Time)

	// Render emits the action-specific readback fragment (e.g., "fly heading
	// 010") used inside ConditionalCommandIntent.
	Render(rt *av.RadioTransmission, r *rand.Rand)
}

// PendingConditionalCommand is the single slot on Nav that stores a
// deferred LV/RC action. A new LV/RC command supersedes any prior slot;
// successful trigger firing clears it.
type PendingConditionalCommand struct {
	Kind     ConditionalKind
	Altitude float32 // feet MSL
	Action   ConditionalAction
}
