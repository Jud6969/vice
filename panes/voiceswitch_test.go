package panes

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/sim"
)

func TestNewVoiceSwitchPane_DefaultsAndGuardConstant(t *testing.T) {
	vs := NewVoiceSwitchPane()
	if vs == nil {
		t.Fatal("NewVoiceSwitchPane returned nil")
	}
	if vs.FontSize == 0 {
		t.Errorf("FontSize = 0, want non-zero default")
	}
	if vs.seeded {
		t.Errorf("seeded = true on fresh pane, want false")
	}
	if len(vs.rows) != 0 {
		t.Errorf("rows length = %d on fresh pane, want 0", len(vs.rows))
	}
	if GuardFrequency != av.NewFrequency(121.500) {
		t.Errorf("GuardFrequency = %v, want %v", GuardFrequency, av.NewFrequency(121.500))
	}
}

// makeStateWithControllers builds a minimal *sim.CommonState for testing reconcile.
// owned positions are consolidated under the given tcw; others are present in
// Controllers but not in the TCW's consolidation.
func makeStateWithControllers(tcw sim.TCW, owned map[sim.ControlPosition]av.Frequency, others map[sim.ControlPosition]av.Frequency) *sim.CommonState {
	controllers := map[sim.ControlPosition]*av.Controller{}
	for pos, freq := range owned {
		controllers[pos] = &av.Controller{Callsign: string(pos), Frequency: freq}
	}
	for pos, freq := range others {
		controllers[pos] = &av.Controller{Callsign: string(pos), Frequency: freq}
	}

	primary := sim.TCP("")
	var secondaries []sim.SecondaryTCP
	for pos := range owned {
		if primary == "" {
			primary = sim.TCP(pos)
		} else {
			secondaries = append(secondaries, sim.SecondaryTCP{TCP: sim.TCP(pos), Type: sim.ConsolidationFull})
		}
	}

	return &sim.CommonState{
		Controllers: controllers,
		DynamicState: sim.DynamicState{
			CurrentConsolidation: map[sim.TCW]*sim.TCPConsolidation{
				tcw: {PrimaryTCP: primary, SecondaryTCPs: secondaries},
			},
		},
	}
}

func TestReconcile_AutoSeedAddsGuardAndOwnedRows(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{
			"JFK_TWR": av.NewFrequency(124.350),
			"JFK_GND": av.NewFrequency(121.900),
		}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)

	if !vs.seeded {
		t.Fatal("seeded=false after first reconcile")
	}
	if len(vs.rows) != 3 {
		t.Fatalf("rows=%d, want 3 (guard + 2 owned)", len(vs.rows))
	}

	// Find each expected row
	freqs := map[av.Frequency]voiceSwitchRow{}
	for _, r := range vs.rows {
		freqs[r.Freq] = r
	}
	guard, ok := freqs[GuardFrequency]
	if !ok || !guard.Guard || !guard.RX || !guard.TX {
		t.Errorf("guard row missing or wrong: %+v", guard)
	}
	twr, ok := freqs[av.NewFrequency(124.350)]
	if !ok || !twr.Owned || !twr.RX || !twr.TX {
		t.Errorf("JFK_TWR row missing or wrong: %+v", twr)
	}
	gnd, ok := freqs[av.NewFrequency(121.900)]
	if !ok || !gnd.Owned || !gnd.RX || !gnd.TX {
		t.Errorf("JFK_GND row missing or wrong: %+v", gnd)
	}
}

func TestReconcile_DefersSeedUntilTCWAssigned(t *testing.T) {
	state := &sim.CommonState{
		Controllers:  map[sim.ControlPosition]*av.Controller{},
		DynamicState: sim.DynamicState{},
	}
	vs := NewVoiceSwitchPane()
	vs.reconcile(state, "") // empty TCW → should defer
	if vs.seeded {
		t.Fatal("seeded=true with empty UserTCW")
	}
	if len(vs.rows) != 0 {
		t.Fatalf("rows=%d before seed, want 0", len(vs.rows))
	}
}

func TestReconcile_LosingPositionFlipsRowOff(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)

	// Drop the position
	state.CurrentConsolidation[tcw] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state, tcw)

	var twr *voiceSwitchRow
	for i := range vs.rows {
		if vs.rows[i].Freq == av.NewFrequency(124.350) {
			twr = &vs.rows[i]
		}
	}
	if twr == nil {
		t.Fatal("JFK_TWR row removed; should remain")
	}
	if twr.Owned || twr.RX || twr.TX {
		t.Errorf("after losing position: %+v, want Owned/RX/TX all false", *twr)
	}
}

func TestReconcile_GainingPositionRestoresRow(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)
	// Lose
	state.CurrentConsolidation[tcw] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state, tcw)
	// Regain
	state.CurrentConsolidation[tcw] = &sim.TCPConsolidation{PrimaryTCP: "JFK_TWR"}
	vs.reconcile(state, tcw)

	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(124.350) {
			if !r.Owned || !r.RX || !r.TX {
				t.Errorf("after regain: %+v, want Owned/RX/TX all true", r)
			}
			return
		}
	}
	t.Fatal("JFK_TWR row not found after regain")
}

func TestReconcile_GuardSurvivesReconcile(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)

	// User toggles guard RX off
	for i := range vs.rows {
		if vs.rows[i].Guard {
			vs.rows[i].RX = false
		}
	}

	// Reconcile after some change
	state.CurrentConsolidation[tcw] = &sim.TCPConsolidation{PrimaryTCP: ""}
	vs.reconcile(state, tcw)

	for _, r := range vs.rows {
		if r.Guard {
			if r.RX {
				t.Errorf("guard RX flipped back to true after reconcile; user toggle should persist")
			}
			return
		}
	}
	t.Fatal("guard row missing after reconcile")
}

func TestReconcile_DedupesSharedFrequency(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{
			"JFK_TWR_1": av.NewFrequency(124.350),
			"JFK_TWR_2": av.NewFrequency(124.350),
		}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)

	count := 0
	for _, r := range vs.rows {
		if r.Freq == av.NewFrequency(124.350) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared-freq rows = %d, want 1 (deduped)", count)
	}
}

func TestIsRX_OwnedDefaultsTrue(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)
	if !vs.IsRX("JFK_TWR", state, tcw) {
		t.Errorf("IsRX(JFK_TWR) = false on freshly-seeded pane, want true")
	}
}

func TestIsRX_RXOffSuppresses(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)
	for i := range vs.rows {
		if vs.rows[i].Freq == av.NewFrequency(124.350) {
			vs.rows[i].RX = false
		}
	}
	if vs.IsRX("JFK_TWR", state, tcw) {
		t.Errorf("IsRX = true after RX toggled off, want false")
	}
}

func TestIsRX_UnresolvableFallsBackToTCWControlsPosition(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)}, nil)

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)
	// "_TOWER" sentinel is not in Controllers map.
	got := vs.IsRX("_TOWER", state, tcw)
	want := state.TCWControlsPosition(tcw, "_TOWER")
	if got != want {
		t.Errorf("IsRX(_TOWER) = %v, want fallback %v", got, want)
	}
}

func TestIsRX_ResolvableButNoRowFallsBack(t *testing.T) {
	tcw := sim.TCW("TEST")
	state := makeStateWithControllers(tcw,
		map[sim.ControlPosition]av.Frequency{"JFK_TWR": av.NewFrequency(124.350)},
		map[sim.ControlPosition]av.Frequency{"BOS_TWR": av.NewFrequency(127.750)})

	vs := NewVoiceSwitchPane()
	vs.reconcile(state, tcw)
	got := vs.IsRX("BOS_TWR", state, tcw)
	want := state.TCWControlsPosition(tcw, "BOS_TWR")
	if got != want {
		t.Errorf("IsRX(BOS_TWR) = %v, want fallback %v", got, want)
	}
}
