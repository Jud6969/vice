// sim/practice_test.go
package sim

import (
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/vmihailenco/msgpack/v5"
)

// TestAircraft_PracticeFieldsRoundTrip verifies that the four IFR
// practice-approach fields round-trip through msgpack serialization
// (the project's real serialization mechanism — sim.Time only implements
// msgpack, not gob, because its underlying time.Time is held in an
// unexported field).
func TestAircraft_PracticeFieldsRoundTrip(t *testing.T) {
	ac := &Aircraft{
		ADSBCallsign:               "AAL123",
		MissedApproachesRemaining:  3,
		PracticeApproachID:         "I22L",
		PracticeApproachController: "1A",
		PendingPracticeRequest:     true,
	}

	buf, err := msgpack.Marshal(ac)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	var got Aircraft
	if err := msgpack.Unmarshal(buf, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.MissedApproachesRemaining != 3 {
		t.Errorf("MissedApproachesRemaining: want 3, got %d", got.MissedApproachesRemaining)
	}
	if got.PracticeApproachID != "I22L" {
		t.Errorf("PracticeApproachID: want %q, got %q", "I22L", got.PracticeApproachID)
	}
	if got.PracticeApproachController != "1A" {
		t.Errorf("PracticeApproachController: want %q, got %q", "1A", got.PracticeApproachController)
	}
	if !got.PendingPracticeRequest {
		t.Errorf("PendingPracticeRequest: want true, got false")
	}
	_ = av.ADSBCallsign("") // keep import even if unused
}
