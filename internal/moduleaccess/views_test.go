package moduleaccess

import (
	"context"
	"errors"
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

// #2555: only a Views over both stores counts as wired; the HTTP server
// refuses to start on any other.
func TestViews_Wired(t *testing.T) {
	t.Parallel()
	store := NewStore(nil)
	flags := featureflag.NewStore(nil)
	cases := map[string]struct {
		v    *Views
		want bool
	}{
		"nil":         {nil, false},
		"no access":   {NewViews(nil, flags), false},
		"no flags":    {NewViews(store, nil), false},
		"both stores": {NewViews(store, flags), true},
	}
	for name, tc := range cases {
		if got := tc.v.Wired(); got != tc.want {
			t.Errorf("%s: Wired() = %v; want %v", name, got, tc.want)
		}
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

// flagsDown fails every lookup as the flag store does when its DB read
// fails.
type flagsDown struct{}

func (flagsDown) Get(context.Context, string) (featureflag.Flag, error) {
	return featureflag.Flag{}, featureflag.ErrStoreUnavailable
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

// #2590: a path the registry places under two tied owners is an error
// from Sees, as it is from RouteOn and the gate, never "show"; a path no
// module owns is still shown.
func TestViewer_SeesTiedOwnersIsAnError(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/activity", nil)
	v := Viewer{
		r: r, flags: flagsOff{}, access: Access{Exempt: true},
		resolve: func(p string) (featureflag.Owner, error) {
			if p == "/v1/tied" {
				return featureflag.Owner{}, featureflag.ErrAmbiguousModule
			}
			return featureflag.Resolve(p)
		},
	}
	if ok, err := v.Sees("/v1/tied"); !errors.Is(err, featureflag.ErrAmbiguousModule) || ok {
		t.Errorf("Sees(tied) = %v, %v; want false, ErrAmbiguousModule", ok, err)
	}
	if ok, err := v.Sees("/v1/no-such-route"); err != nil || !ok {
		t.Errorf("Sees(unowned) = %v, %v; want true, nil", ok, err)
	}
}

// #2590: Modules reads the switches strictly, as Sees (RouteOn) does: with
// the flag store down it fails rather than falling back to the seed
// default, which shows Risk.
func TestViewer_ModulesFailsClosedOnStoreDown(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/activity", nil)
	v := Viewer{r: r, flags: flagsDown{}, access: Access{Exempt: true}}
	if _, err := v.Modules(); !errors.Is(err, featureflag.ErrStoreUnavailable) {
		t.Fatalf("Modules err = %v; want ErrStoreUnavailable", err)
	}
	if _, err := (&Views{flags: flagsDown{}}).Switches(r); !errors.Is(err, featureflag.ErrStoreUnavailable) {
		t.Fatalf("Switches err = %v; want ErrStoreUnavailable", err)
	}
}

// #2590: Switches answers the tenant's switches alone. A switched-off
// module's routes are hidden; access plays no part, and a nil Views
// reports every module on.
func TestViews_Switches(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest("GET", "/v1/admin/audit-log/unified", nil)
	set, err := (&Views{flags: flagsOff{"risk.enabled": true}}).Switches(r)
	if err != nil {
		t.Fatalf("Switches: %v", err)
	}
	if set.Sees("/v1/risks") || !set.Sees("/v1/vendors") || !set.Sees("/v1/controls") {
		t.Errorf("Switches: risks %v, vendors %v, controls %v; want false, true, true",
			set.Sees("/v1/risks"), set.Sees("/v1/vendors"), set.Sees("/v1/controls"))
	}
	var nilViews *Views
	if set, err := nilViews.Switches(r); err != nil || !set.Sees("/v1/risks") {
		t.Errorf("nil Switches err = %v; want every module on", err)
	}
}
