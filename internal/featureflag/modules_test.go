package featureflag

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModulesFlagKeysAreSeeded(t *testing.T) {
	t.Parallel()
	for _, m := range Modules {
		if m.Core {
			if m.FlagKey != "" {
				t.Errorf("core module %q has a switch %q; core modules are always on", m.Key, m.FlagKey)
			}
		} else if _, ok := DefaultByKey(m.FlagKey); !ok {
			t.Errorf("module %q switch %q is not in Seed", m.Key, m.FlagKey)
		}
		for _, f := range m.Features {
			if _, ok := DefaultByKey(f.FlagKey); !ok {
				t.Errorf("feature %s/%s switch %q is not in Seed", m.Key, f.Key, f.FlagKey)
			}
		}
	}
}

// Every Seed flag belongs to a module, so Admin › Features can group it.
func TestEverySeedFlagHasAModule(t *testing.T) {
	t.Parallel()
	for _, d := range Seed {
		if _, _, ok := ModuleForFlag(d.Key); !ok {
			t.Errorf("seed flag %q belongs to no module", d.Key)
		}
	}
}

// Existing tenants keep today's behaviour: every optional module is on by
// default except Board reporting, and Vendor claims stays off.
func TestModuleDefaults(t *testing.T) {
	t.Parallel()
	want := map[string]bool{
		"risk": true, "third_party_risk": true, "policies": true,
		"questionnaires": true, "personnel_security": true, "exceptions": true,
		"board_reporting": false,
	}
	got := map[string]bool{}
	for _, m := range OptionalModules() {
		def, _ := DefaultByKey(m.FlagKey)
		got[m.Key] = def.Enabled
	}
	if len(got) != len(want) {
		t.Fatalf("optional modules = %v; want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("module %q defaults to %v; want %v", k, got[k], v)
		}
	}
	for key, on := range map[string]bool{"vendor.claims": false, "audit.oscal_export": true} {
		if def, _ := DefaultByKey(key); def.Enabled != on {
			t.Errorf("%s defaults to %v; want %v", key, def.Enabled, on)
		}
	}
}

func TestResolve(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path    string
		module  string
		feature string
	}{
		{"/v1/questionnaires", "questionnaires", ""},
		{"/v1/questionnaires/abc/answers/q1/ai-suggest", "questionnaires", ""},
		{"/v1/board-packs/abc.md", "board_reporting", ""},
		{"/v1/board/narrative/generate", "board_reporting", ""},
		{"/v1/oscal/component-claims/abc:accept", "third_party_risk", "vendor_claims"},
		{"/v1/oscal/component-definitions/abc", "third_party_risk", "vendor_claims"},
		{"/v1/oscal/imported-profiles/abc/provenance", "frameworks", ""},
		{"/v1/audit-periods/abc/oscal-export", "audit_periods", "oscal_export"},
		{"/v1/audit-periods/abc/oscal-export:download", "audit_periods", "oscal_export"},
		{"/v1/audit-periods/abc/freeze", "audit_periods", ""},
		{"/v1/controls", "controls", ""},
		{"/v1/controls:bulk-assign-owner", "controls", ""},
		{"/v1/controls/abc/action-plans", "risk", ""},
		{"/v1/controls/abc/risks", "risk", ""},
		{"/v1/controls/abc/policies", "policies", ""},
		{"/v1/controls/abc/state", "controls", ""},
		{"/v1/controls/abc/effective-scope", "scopes", ""},
		{"/v1/org_units/abc", "risk", ""},
		{"/v1/decisions/abc/links/risk/xyz", "risk", ""},
		{"/v1/me", "settings_admin", ""},
		{"/v1/people", "settings_admin", ""},
		{"/v1/approvals", "dashboard", ""},
		{"/v1/needs-attention", "dashboard", ""},
		{"/v1/me/acknowledgments", "policies", ""},
		{"/v1/admin/vendors/export", "third_party_risk", ""},
		{"/v1/admin/features/risk.enabled", "settings_admin", ""},
		{"/v1/personnel-security/checklists", "personnel_security", ""},
		{"/v1/exceptions/abc/approve", "exceptions", ""},
		{"/v1/calendar.ics", "dashboard", ""},
		{"/health", "settings_admin", ""},
	}
	for _, c := range cases {
		o, err := Resolve(c.path)
		if err != nil {
			t.Errorf("Resolve(%q): %v", c.path, err)
			continue
		}
		feat := ""
		if o.Feature != nil {
			feat = o.Feature.Key
		}
		if o.Module.Key != c.module || feat != c.feature {
			t.Errorf("Resolve(%q) = %s/%s; want %s/%s", c.path, o.Module.Key, feat, c.module, c.feature)
		}
	}
}

func TestResolveRefusesUnknownAndNearMisses(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/v1/questionnaires-x", "/v1/boards", "/v1/nope", "/"} {
		if o, err := Resolve(p); !errors.Is(err, ErrNoModule) {
			t.Errorf("Resolve(%q) = %s, %v; want ErrNoModule", p, o.Module.Key, err)
		}
	}
}

func TestModuleGate(t *testing.T) {
	t.Parallel()
	looker := &fakeLooker{answers: map[string]Flag{
		"questionnaires.enabled": {Key: "questionnaires.enabled", Enabled: false},
		"board.reporting":        {Key: "board.reporting", Enabled: true},
		"risk.enabled":           {Key: "risk.enabled", Enabled: false},
		"vendor.enabled":         {Key: "vendor.enabled", Enabled: true},
		"vendor.claims":          {Key: "vendor.claims", Enabled: false},
		"audit.oscal_export":     {Key: "audit.oscal_export", Enabled: true},
	}}
	h := ModuleGate(looker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	cases := []struct {
		target string
		status int
		marker string
	}{
		{"/v1/questionnaires/abc", http.StatusNotFound, "questionnaires.enabled"},
		{"/v1/board-packs", http.StatusTeapot, ""},
		{"/v1/controls", http.StatusTeapot, ""},
		// A nested route is gated by the module whose data it returns.
		{"/v1/controls/abc/risks", http.StatusNotFound, "risk.enabled"},
		// An encoded "/" in the id: chi routes on the raw path, where the
		// nested risks route still matches, so the gate must refuse too.
		{"/v1/controls/a%2Fb/risks", http.StatusNotFound, "risk.enabled"},
		// A feature switched off inside a module that is on.
		{"/v1/oscal/component-definitions", http.StatusNotFound, "vendor.claims"},
		{"/v1/vendors", http.StatusTeapot, ""},
		{"/v1/audit-periods/abc/oscal-export", http.StatusTeapot, ""},
		// A path the registry cannot place falls through to chi's 404.
		{"/v1/nope", http.StatusTeapot, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.target, nil))
		if rec.Code != c.status {
			t.Errorf("%s: status %d; want %d", c.target, rec.Code, c.status)
		}
		if got := rec.Header().Get("X-Feature-Disabled"); got != c.marker {
			t.Errorf("%s: marker %q; want %q", c.target, got, c.marker)
		}
	}
}

// A feature whose module is off names the module switch, and the
// feature's own switch is never read or changed.
func TestModuleGateModuleOffWinsOverFeature(t *testing.T) {
	t.Parallel()
	looker := &fakeLooker{answers: map[string]Flag{
		"vendor.enabled": {Key: "vendor.enabled", Enabled: false},
		"vendor.claims":  {Key: "vendor.claims", Enabled: true},
	}}
	h := ModuleGate(looker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/oscal/component-definitions", nil))
	if rec.Code != http.StatusNotFound || rec.Header().Get("X-Feature-Disabled") != "vendor.enabled" {
		t.Fatalf("status %d marker %q; want 404 vendor.enabled", rec.Code, rec.Header().Get("X-Feature-Disabled"))
	}
	if n := looker.calls.Load(); n != 1 {
		t.Fatalf("looker called %d times; want 1 (the module switch only)", n)
	}
}

// A flag-store error answers 503 for an optional module, whatever its Seed
// default: the gate does not guess the tenant's switch (#2339). A core
// route reads no switch and is served.
func TestModuleGateFailsClosedOnStoreError(t *testing.T) {
	looker := &fakeLooker{err: errors.New("db down")}
	h := ModuleGate(looker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	before := ModuleStateErrors()
	for target, want := range map[string]int{
		"/v1/risks":       http.StatusServiceUnavailable, // default on
		"/v1/board-packs": http.StatusServiceUnavailable, // default off
		"/v1/controls":    http.StatusTeapot,             // core
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil).WithContext(context.Background()))
		if rec.Code != want {
			t.Errorf("%s: status %d; want %d", target, rec.Code, want)
		}
		if want != http.StatusServiceUnavailable {
			continue
		}
		if got := rec.Header().Get("X-Feature-Disabled"); got != "" {
			t.Errorf("%s: marker %q on a 503; a read failure is not a switch-off", target, got)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body["error"] != "module_state_unavailable" {
			t.Errorf("%s: body %q; want error module_state_unavailable", target, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Errorf("%s: no Retry-After on a 503", target)
		}
	}
	if got := ModuleStateErrors() - before; got != 2 {
		t.Errorf("ModuleStateErrors grew by %d; want 2", got)
	}
}

// The recorder sees every request refused as switched off, with the module
// and the switch, and nothing else.
func TestModuleGateRecordsGatedAttempts(t *testing.T) {
	t.Parallel()
	looker := &fakeLooker{answers: map[string]Flag{
		"questionnaires.enabled": {Key: "questionnaires.enabled", Enabled: false},
		"vendor.enabled":         {Key: "vendor.enabled", Enabled: true},
		"vendor.claims":          {Key: "vendor.claims", Enabled: false},
		"risk.enabled":           {Key: "risk.enabled", Enabled: true},
	}}
	var got []GatedAttempt
	h := ModuleGate(looker, WithAttemptRecorder(func(_ *http.Request, a GatedAttempt) {
		got = append(got, a)
	}))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	for _, target := range []string{"/v1/questionnaires", "/v1/oscal/component-definitions", "/v1/risks", "/v1/controls"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, target, nil))
	}
	want := []GatedAttempt{
		{Module: "questionnaires", FlagKey: "questionnaires.enabled"},
		{Module: "third_party_risk", FlagKey: "vendor.claims"},
	}
	if len(got) != len(want) {
		t.Fatalf("recorded %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("attempt %d = %+v; want %+v", i, got[i], want[i])
		}
	}
}
