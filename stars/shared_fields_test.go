package stars

import (
	"reflect"
	"strings"
	"testing"
)

// TestEveryPreferencesFieldIsCategorized walks every field in Preferences
// (including embedded CommonPreferences) and fails if any field is not
// explicitly listed in either SyncedPreferenceFields or
// UnsyncedPreferenceFields. This prevents silent drift when new
// preferences are added.
func TestEveryPreferencesFieldIsCategorized(t *testing.T) {
	synced := map[string]bool{}
	for _, f := range SyncedPreferenceFields {
		synced[f] = true
	}
	unsynced := map[string]bool{}
	for _, f := range UnsyncedPreferenceFields {
		unsynced[f] = true
	}

	var visit func(typ reflect.Type, prefix string)
	visit = func(typ reflect.Type, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			sf := typ.Field(i)
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
				// Flatten embedded structs (CommonPreferences).
				visit(sf.Type, prefix)
				continue
			}
			name := prefix + sf.Name
			if !synced[name] && !unsynced[name] {
				t.Errorf("Preferences field %q is not categorized in "+
					"SyncedPreferenceFields or UnsyncedPreferenceFields "+
					"(add it to stars/shared_fields.go)", name)
			}
			if synced[name] && unsynced[name] {
				t.Errorf("Preferences field %q is in both synced and unsynced lists", name)
			}
		}
	}

	visit(reflect.TypeOf(Preferences{}), "")

	// Sanity check: lists shouldn't name fields that don't exist.
	all := map[string]bool{}
	var collect func(typ reflect.Type, prefix string)
	collect = func(typ reflect.Type, prefix string) {
		for i := 0; i < typ.NumField(); i++ {
			sf := typ.Field(i)
			if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
				collect(sf.Type, prefix)
				continue
			}
			all[prefix+sf.Name] = true
		}
	}
	collect(reflect.TypeOf(Preferences{}), "")
	for _, name := range append(SyncedPreferenceFields, UnsyncedPreferenceFields...) {
		if !all[name] && !strings.HasPrefix(name, "_deprecated_") {
			t.Errorf("stars/shared_fields.go lists unknown Preferences field %q", name)
		}
	}
}
