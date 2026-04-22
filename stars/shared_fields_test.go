package stars

import (
	"reflect"
	"strings"
	"testing"

	"github.com/mmp/vice/client"
	"github.com/mmp/vice/math"
	"github.com/mmp/vice/panes"
	"github.com/mmp/vice/sim"
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

// newCtxWithClient returns a panes.Context whose Client is non-nil but
// otherwise zero-valued — enough for the sync helpers, which only read
// ctx.Client.State.TCWDisplay.
func newCtxWithClient() *panes.Context {
	return &panes.Context{Client: &client.ControlClient{}}
}

func TestSyncedRangePrefersTCWDisplay(t *testing.T) {
	sp := &STARSPane{prefSet: &PreferenceSet{Current: Preferences{}}}
	sp.prefSet.Current.Range = 10
	ctx := newCtxWithClient()

	if got := sp.syncedRange(ctx); got != 10 {
		t.Errorf("syncedRange without TCWDisplay = %v, want 10", got)
	}

	ctx.Client.State.TCWDisplay = &sim.TCWDisplayState{ScopeView: sim.ScopeViewState{Range: 50}}
	if got := sp.syncedRange(ctx); got != 50 {
		t.Errorf("syncedRange with TCWDisplay = %v, want 50", got)
	}
}

func TestSyncedUserCenterPrefersTCWDisplay(t *testing.T) {
	sp := &STARSPane{prefSet: &PreferenceSet{Current: Preferences{}}}
	sp.prefSet.Current.UserCenter = math.Point2LL{1, 2}
	ctx := newCtxWithClient()

	if got := sp.syncedUserCenter(ctx); got != (math.Point2LL{1, 2}) {
		t.Errorf("syncedUserCenter without TCWDisplay = %+v, want {1,2}", got)
	}

	ctx.Client.State.TCWDisplay = &sim.TCWDisplayState{
		ScopeView: sim.ScopeViewState{UserCenter: math.Point2LL{-73.7, 40.6}},
	}
	if got := sp.syncedUserCenter(ctx); got != (math.Point2LL{-73.7, 40.6}) {
		t.Errorf("syncedUserCenter with TCWDisplay = %+v, want {-73.7,40.6}", got)
	}
}

func TestSyncedRangeRingRadiusPrefersTCWDisplay(t *testing.T) {
	sp := &STARSPane{prefSet: &PreferenceSet{Current: Preferences{}}}
	sp.prefSet.Current.RangeRingRadius = 5
	ctx := newCtxWithClient()

	if got := sp.syncedRangeRingRadius(ctx); got != 5 {
		t.Errorf("syncedRangeRingRadius without TCWDisplay = %v, want 5", got)
	}

	ctx.Client.State.TCWDisplay = &sim.TCWDisplayState{ScopeView: sim.ScopeViewState{RangeRingRadius: 20}}
	if got := sp.syncedRangeRingRadius(ctx); got != 20 {
		t.Errorf("syncedRangeRingRadius with TCWDisplay = %v, want 20", got)
	}
}

func TestMergeLoadedPreferencesSkipsSyncedFields(t *testing.T) {
	existing := Preferences{}
	existing.Range = 20
	existing.UserCenter = math.Point2LL{1, 1}
	existing.RangeRingRadius = 5

	toLoad := Preferences{}
	toLoad.Range = 999
	toLoad.UserCenter = math.Point2LL{9, 9}
	toLoad.RangeRingRadius = 99
	// Unsynced field (Brightness lives on CommonPreferences/Preferences).
	toLoad.Brightness.DCB = 77

	result := mergeLoadedPreferences(existing, toLoad)
	if result.Range != 20 {
		t.Errorf("synced Range was clobbered: got %v, want 20", result.Range)
	}
	if result.UserCenter != (math.Point2LL{1, 1}) {
		t.Errorf("synced UserCenter was clobbered: got %+v, want {1,1}", result.UserCenter)
	}
	if result.RangeRingRadius != 5 {
		t.Errorf("synced RangeRingRadius was clobbered: got %v, want 5", result.RangeRingRadius)
	}
	if result.Brightness.DCB != 77 {
		t.Errorf("unsynced Brightness.DCB not applied: got %v, want 77", result.Brightness.DCB)
	}
}

func TestMirrorTCWDisplayIntoPrefs(t *testing.T) {
	sp := &STARSPane{prefSet: &PreferenceSet{Current: Preferences{}}}
	sp.prefSet.Current.Range = 1
	sp.prefSet.Current.UserCenter = math.Point2LL{}
	sp.prefSet.Current.RangeRingRadius = 1

	// Nil snapshot must be a no-op.
	sp.mirrorTCWDisplayIntoPrefs(nil)
	if sp.currentPrefs().Range != 1 {
		t.Errorf("nil mirror clobbered Range")
	}

	d := &sim.TCWDisplayState{ScopeView: sim.ScopeViewState{
		Range:           42,
		UserCenter:      math.Point2LL{-73.7, 40.6},
		RangeRingRadius: 7,
	}}
	sp.mirrorTCWDisplayIntoPrefs(d)
	ps := sp.currentPrefs()
	if ps.Range != 42 {
		t.Errorf("mirror Range = %v, want 42", ps.Range)
	}
	if ps.UserCenter != (math.Point2LL{-73.7, 40.6}) {
		t.Errorf("mirror UserCenter = %+v, want {-73.7,40.6}", ps.UserCenter)
	}
	if ps.RangeRingRadius != 7 {
		t.Errorf("mirror RangeRingRadius = %v, want 7", ps.RangeRingRadius)
	}
}
