package moduleaccess

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// #2175: SwitchedOn answers for a module key or a feature key, with no
// request: a feature needs its module and its own switch on.
func TestViews_SwitchedOnModulesAndFeatures(t *testing.T) {
	t.Parallel()
	tenant := uuid.New()
	v := &Views{flags: flagsOff{"vendor.enabled": true, "decisions.log": true}}
	cases := map[string]bool{
		"third_party_risk":   false, // module off
		"vendor_claims":      false, // feature of a module that is off
		"risk":               true,
		"decision_log":       false, // feature switched off, module on
		"risk_hierarchy":     true,
		"policies":           true,
		"controls":           true, // core
		"no_such_module_key": true,
	}
	for key, want := range cases {
		got, err := v.SwitchedOn(context.Background(), tenant, key)
		if err != nil || got != want {
			t.Errorf("SwitchedOn(%q) = %v, %v; want %v", key, got, err, want)
		}
	}
}

// A switch that cannot be read is an error, never a yes.
func TestViews_SwitchedOnFlagError(t *testing.T) {
	t.Parallel()
	v := &Views{flags: flagsErr{}}
	if on, err := v.SwitchedOn(context.Background(), uuid.New(), "risk"); err == nil || on {
		t.Fatalf("SwitchedOn = %v, %v; want false, error", on, err)
	}
}

// A switched-off module reaches no one, before any access read: Reaches
// answers false without touching the (absent) access store.
func TestViews_ReachesModuleOff(t *testing.T) {
	t.Parallel()
	v := &Views{flags: flagsOff{"policy.enabled": true}}
	ok, err := v.Reaches(context.Background(), uuid.New(), "u1", "policy_acknowledgments")
	if err != nil || ok {
		t.Fatalf("Reaches = %v, %v; want false, nil", ok, err)
	}
}

// A nil Views (no database) reaches everyone.
func TestViews_NilReachesEveryone(t *testing.T) {
	t.Parallel()
	var v *Views
	for _, key := range []string{"risk", "decision_log", "third_party_risk"} {
		if ok, err := v.Reaches(context.Background(), uuid.New(), "u1", key); err != nil || !ok {
			t.Errorf("nil Reaches(%q) = %v, %v; want true", key, ok, err)
		}
	}
}

// lookup finds module keys and feature keys, and a feature reports its
// module.
func TestLookup(t *testing.T) {
	t.Parallel()
	m, f, ok := lookup("decision_log")
	if !ok || m.Key != "risk" || f == nil || f.FlagKey != "decisions.log" {
		t.Errorf("lookup(decision_log) = %q, %v, %v", m.Key, f, ok)
	}
	m, f, ok = lookup("personnel_security")
	if !ok || m.Key != "personnel_security" || f != nil {
		t.Errorf("lookup(personnel_security) = %q, %v, %v", m.Key, f, ok)
	}
	if _, _, ok := lookup("nope"); ok {
		t.Error("lookup(nope) found a module")
	}
}
