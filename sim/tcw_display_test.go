// sim/tcw_display_test.go
// Copyright(c) 2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package sim

import (
	"testing"
	"time"
)

func TestEnsureTCWDisplay_HasZeroRadioHoldUntil(t *testing.T) {
	s := &Sim{}
	d := s.EnsureTCWDisplay("TCW-1")
	if !d.RadioHoldUntil.IsZero() {
		t.Errorf("RadioHoldUntil should be zero on a fresh TCWDisplayState; got %v", d.RadioHoldUntil)
	}
}

func TestEnsureTCWDisplay_RadioHoldUntilPersists(t *testing.T) {
	s := &Sim{}
	d := s.EnsureTCWDisplay("TCW-1")
	target := NewSimTime(time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC))
	d.RadioHoldUntil = target

	d2 := s.EnsureTCWDisplay("TCW-1")
	if !d2.RadioHoldUntil.Equal(target) {
		t.Errorf("RadioHoldUntil not preserved across EnsureTCWDisplay; want %v got %v", target, d2.RadioHoldUntil)
	}
}
