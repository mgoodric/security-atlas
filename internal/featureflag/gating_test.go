package featureflag

import "testing"

// Slice 660 — GatingKeys are the capability flags that gate BOTH a nav
// entry and a route/handler. They must default OFF in Seed (pending GA)
// and must each resolve to a real Seed entry.

func TestGatingKeysAllSeededAndDefaultOff(t *testing.T) {
	t.Parallel()
	if len(GatingKeys) == 0 {
		t.Fatal("GatingKeys is empty; slice 660 expects oscal.export + board.reporting")
	}
	for _, key := range GatingKeys {
		def, ok := DefaultByKey(key)
		if !ok {
			t.Errorf("GatingKey %q has no Seed entry", key)
			continue
		}
		if def.Enabled {
			t.Errorf("GatingKey %q defaults ON; slice 660 expects OFF pending GA", key)
		}
	}
}

func TestGatingKeysContainExpectedModules(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"oscal.export": false, "board.reporting": false}
	for k := range want {
		if !IsGatingKey(k) {
			t.Errorf("expected %q to be a gating key", k)
		}
	}
}

func TestIsGatingKeyRejectsNonGating(t *testing.T) {
	t.Parallel()
	for _, key := range []string{"risk.enabled", "policy.enabled", "", "oscal", "board"} {
		if IsGatingKey(key) {
			t.Errorf("IsGatingKey(%q) = true; want false", key)
		}
	}
}
