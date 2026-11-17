package moduleaccess

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/featureflag"
)

// A nil Views (a server with no database) sees every module, and its
// ModuleSet hides nothing.
func TestViews_NilSeesEverything(t *testing.T) {
	t.Parallel()
	var v *Views
	viewer, err := v.For(httptest.NewRequest("GET", "/v1/calendar", nil))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	for _, p := range []string{"/v1/policies", "/v1/vendors", "/v1/controls", "/v1/no-such-route"} {
		if ok, err := viewer.Sees(p); err != nil || !ok {
			t.Errorf("Sees(%q) = %v, %v; want true", p, ok, err)
		}
	}
	set, err := viewer.Modules()
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	if !set.Sees("/v1/policies") {
		t.Errorf("zero ModuleSet hides policies")
	}
}

// ModuleSet answers per module through the registry: a hidden optional
// module's routes are hidden, nested routes follow their owner, and core
// routes and unplaced paths are always shown.
func TestModuleSet_SeesFollowsRegistry(t *testing.T) {
	t.Parallel()
	set := ModuleSet{hidden: map[string]bool{"policies": true, "risk": true}}
	cases := map[string]bool{
		"/v1/policies":                false,
		"/v1/me/acknowledgments":      false,
		"/v1/controls/x/policies":     false,
		"/v1/action-plans":            false,
		"/v1/controls/x/action-plans": false,
		"/v1/vendors":                 true,
		"/v1/controls":                true,
		"/v1/evidence":                true,
		"/v1/no-such-route":           true,
	}
	for path, want := range cases {
		if got := set.Sees(path); got != want {
			t.Errorf("Sees(%q) = %v, want %v", path, got, want)
		}
	}
}

// A viewer narrowed to an explicit set hides optional modules outside it
// without reading a switch (Has is checked first).
func TestViewer_AccessHidesBeforeSwitch(t *testing.T) {
	t.Parallel()
	v := Viewer{access: Access{Modules: []string{"policies"}}}
	if ok, err := v.Sees("/v1/vendors"); err != nil || ok {
		t.Errorf("Sees(/v1/vendors) = %v, %v; want false, nil", ok, err)
	}
	hidden, err := v.Hidden(map[string]string{"vendor": "/v1/vendors", "risk": "/v1/risks"})
	if err != nil {
		t.Fatalf("Hidden: %v", err)
	}
	if !hidden["vendor"] || !hidden["risk"] {
		t.Errorf("Hidden = %v; want vendor and risk", hidden)
	}
}

// flagsOff is a featureflag.Looker with every switch on except the keys
// it lists.
type flagsOff map[string]bool

func (f flagsOff) Get(_ context.Context, key string) (featureflag.Flag, error) {
	return featureflag.Flag{Key: key, Enabled: !f[key]}, nil
}

// flagsErr fails every lookup as an unknown key, the error Enabled
// surfaces rather than falling back to the seed default.
type flagsErr struct{}

func (flagsErr) Get(context.Context, string) (featureflag.Flag, error) {
	return featureflag.Flag{}, featureflag.ErrNotFound
}

// Modules honours a feature's own switch, as Sees does (#2477): a feature
// switched off hides the routes it owns, in an optional module and in a
// core one, while the rest of its module stays visible.
func TestViewer_ModulesHonoursFeatureSwitches(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/activity", nil)
	v := Viewer{r: r, flags: flagsOff{"vendor.claims": true, "audit.oscal_export": true}, access: Access{Exempt: true}}
	set, err := v.Modules()
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	cases := map[string]bool{
		"/v1/oscal/component-definitions":  false,
		"/v1/vendors":                      true,
		"/v1/audit-periods/x/oscal-export": false,
		"/v1/audit-periods":                true,
		"/v1/policies":                     true,
		"/v1/controls":                     true,
	}
	for path, want := range cases {
		if got := set.Sees(path); got != want {
			t.Errorf("ModuleSet.Sees(%q) = %v, want %v", path, got, want)
		}
		// The batched answer agrees with the per-request one.
		if got, err := v.Sees(path); err != nil || got != want {
			t.Errorf("Viewer.Sees(%q) = %v, %v; want %v", path, got, err, want)
		}
	}
}

// A module switched off hides its features' routes too.
func TestViewer_ModulesModuleOffHidesFeatures(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/activity", nil)
	v := Viewer{r: r, flags: flagsOff{"vendor.enabled": true}, access: Access{Exempt: true}}
	set, err := v.Modules()
	if err != nil {
		t.Fatalf("Modules: %v", err)
	}
	if set.Sees("/v1/oscal/component-definitions") || set.Sees("/v1/vendors") {
		t.Errorf("vendor module off but its routes are shown")
	}
}

// A switch that cannot be read fails the whole answer rather than
// showing what it gates.
func TestViewer_ModulesFailsClosedOnFlagError(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/activity", nil)
	v := Viewer{r: r, flags: flagsErr{}, access: Access{Exempt: true}}
	if _, err := v.Modules(); err == nil {
		t.Fatal("Modules: want error, got nil")
	}
}
