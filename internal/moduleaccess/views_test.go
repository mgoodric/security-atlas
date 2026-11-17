package moduleaccess

import (
	"net/http/httptest"
	"testing"
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
