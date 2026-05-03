// sim/practice_test.go
package sim

import (
	"strings"
	"testing"

	av "github.com/mmp/vice/aviation"
	"github.com/mmp/vice/rand"
	"github.com/vmihailenco/msgpack/v5"
)

// TestAircraft_PracticeFieldsRoundTrip verifies that the four IFR
// practice-approach fields round-trip through msgpack serialization
// (the project's real serialization mechanism — sim.Time only implements
// msgpack, not gob, because its underlying time.Time is held in an
// unexported field).
func TestAircraft_PracticeFieldsRoundTrip(t *testing.T) {
	ac := &Aircraft{
		ADSBCallsign:               av.ADSBCallsign("AAL123"),
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
}

func TestPickPracticeApproach_PicksMatchingActiveRunway(t *testing.T) {
	approaches := map[string]*av.Approach{
		"I22L": {Id: "I22L", Runway: "22L"},
		"I22R": {Id: "I22R", Runway: "22R"},
		"R4":   {Id: "R4", Runway: "4"},
	}
	active := []string{"22L", "22R"}
	r := rand.Make()

	got := pickPracticeApproach(approaches, active, r)
	if got != "I22L" && got != "I22R" {
		t.Errorf("expected one of {I22L, I22R}, got %q", got)
	}
}

func TestPickPracticeApproach_NoMatchReturnsEmpty(t *testing.T) {
	approaches := map[string]*av.Approach{
		"I22L": {Id: "I22L", Runway: "22L"},
	}
	active := []string{"31R"}
	r := rand.Make()

	if got := pickPracticeApproach(approaches, active, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestPickPracticeApproach_EmptyApproachesReturnsEmpty(t *testing.T) {
	r := rand.Make()
	if got := pickPracticeApproach(nil, []string{"22L"}, r); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

func TestBuildPracticeApproachRequest_LowApproach(t *testing.T) {
	rt := buildPracticeApproachRequest("AAL123", &av.Approach{Id: "I22L", FullName: "ILS Runway 22 Left"}, false)
	if rt == nil {
		t.Fatalf("expected non-nil RadioTransmission")
	}
	written := rt.Written(nil)
	if !strings.Contains(strings.ToLower(written), "ils runway 22 left") {
		t.Errorf("expected approach name in transmission; got %q", written)
	}
	if !strings.Contains(strings.ToLower(written), "for the practice") {
		t.Errorf("expected 'for the practice' phrase; got %q", written)
	}
	if strings.Contains(strings.ToLower(written), "full stop") {
		t.Errorf("low-approach variant should not say 'full stop'; got %q", written)
	}
}

func TestBuildPracticeApproachRequest_FullStop(t *testing.T) {
	rt := buildPracticeApproachRequest("AAL123", &av.Approach{Id: "I22L", FullName: "ILS Runway 22 Left"}, true)
	written := rt.Written(nil)
	if !strings.Contains(strings.ToLower(written), "full stop") {
		t.Errorf("full-stop variant should say 'full stop'; got %q", written)
	}
}
