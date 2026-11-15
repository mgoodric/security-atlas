package featureflag

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModulesFlagKeysAreSeeded(t *testing.T) {
	t.Parallel()
	for _, m := range Modules {
		if _, ok := DefaultByKey(m.FlagKey); !ok {
			t.Errorf("module %q switch %q is not in Seed", m.Key, m.FlagKey)
		}
	}
}

func TestModuleDefaults(t *testing.T) {
	t.Parallel()
	want := map[string]bool{"questionnaires": true, "board_reporting": false, "vendor_claims": false}
	for _, m := range Modules {
		def, _ := DefaultByKey(m.FlagKey)
		if def.Enabled != want[m.Key] {
			t.Errorf("module %q defaults to %v; want %v", m.Key, def.Enabled, want[m.Key])
		}
	}
}

func TestModuleForPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		path string
		want string // "" = core
	}{
		{"/v1/questionnaires", "questionnaires"},
		{"/v1/questionnaires/abc/answers/q1/ai-suggest", "questionnaires"},
		{"/v1/questionnaires-x", ""},
		{"/v1/board-packs/abc.md", "board_reporting"},
		{"/v1/board-briefs", "board_reporting"},
		{"/v1/board/narrative/generate", "board_reporting"},
		{"/v1/boards", ""},
		{"/v1/oscal/component-claims/abc:accept", "vendor_claims"},
		{"/v1/oscal/component-definitions/abc", "vendor_claims"},
		{"/v1/audit-periods/abc/oscal-export", ""},
		{"/v1/controls", ""},
	}
	for _, c := range cases {
		m, ok := ModuleForPath(c.path)
		got := ""
		if ok {
			got = m.Key
		}
		if got != c.want {
			t.Errorf("ModuleForPath(%q) = %q; want %q", c.path, got, c.want)
		}
	}
}

func TestModuleGate(t *testing.T) {
	t.Parallel()
	looker := &fakeLooker{answers: map[string]Flag{
		"questionnaires.enabled": {Key: "questionnaires.enabled", Enabled: false},
		"board.reporting":        {Key: "board.reporting", Enabled: true},
		"oscal.export":           {Key: "oscal.export", Enabled: true},
	}}
	h := ModuleGate(looker)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	cases := []struct {
		path   string
		status int
		marker string
	}{
		{"/v1/questionnaires/abc", http.StatusNotFound, "questionnaires.enabled"},
		{"/v1/board-packs", http.StatusTeapot, ""},
		{"/v1/controls", http.StatusTeapot, ""},
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("%s: status %d; want %d", c.path, rec.Code, c.status)
		}
		if got := rec.Header().Get("X-Feature-Disabled"); got != c.marker {
			t.Errorf("%s: marker %q; want %q", c.path, got, c.marker)
		}
	}
}
