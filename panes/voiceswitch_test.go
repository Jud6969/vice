package panes

import (
	"testing"

	av "github.com/mmp/vice/aviation"
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
