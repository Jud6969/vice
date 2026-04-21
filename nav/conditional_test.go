package nav

import (
	"testing"
)

func TestNavHasPendingConditionalCommandField(t *testing.T) {
	var n Nav
	if n.PendingConditionalCommand != nil {
		t.Fatalf("PendingConditionalCommand should default to nil, got %+v", n.PendingConditionalCommand)
	}
	n.PendingConditionalCommand = &PendingConditionalCommand{
		Kind:     ConditionalLeaving,
		Altitude: 3000,
	}
	if n.PendingConditionalCommand.Kind != ConditionalLeaving {
		t.Fatalf("expected ConditionalLeaving, got %d", n.PendingConditionalCommand.Kind)
	}
	if n.PendingConditionalCommand.Altitude != 3000 {
		t.Fatalf("expected 3000, got %v", n.PendingConditionalCommand.Altitude)
	}
}
