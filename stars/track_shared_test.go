// stars/track_shared_test.go
// Copyright(c) 2022-2026 vice contributors, licensed under the GNU Public License, Version 3.
// SPDX: GPL-3.0-only

package stars

import (
	"reflect"
	"testing"

	"github.com/mmp/vice/sim"
)

// TestSharedTrackAnnotationFieldsMatchStruct guards against drift
// between SharedTrackAnnotationFields and the fields on
// sim.TrackAnnotations. Adding or removing a field there without
// updating the list here should break this test.
func TestSharedTrackAnnotationFieldsMatchStruct(t *testing.T) {
	structFields := map[string]bool{}
	rt := reflect.TypeOf(sim.TrackAnnotations{})
	for i := 0; i < rt.NumField(); i++ {
		structFields[rt.Field(i).Name] = true
	}

	listFields := map[string]bool{}
	for _, name := range SharedTrackAnnotationFields {
		if listFields[name] {
			t.Errorf("SharedTrackAnnotationFields contains %q twice", name)
		}
		listFields[name] = true
	}

	for name := range structFields {
		if !listFields[name] {
			t.Errorf("sim.TrackAnnotations has field %q not in SharedTrackAnnotationFields", name)
		}
	}
	for name := range listFields {
		if !structFields[name] {
			t.Errorf("SharedTrackAnnotationFields lists %q but sim.TrackAnnotations has no such field", name)
		}
	}
}
