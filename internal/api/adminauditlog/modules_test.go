package adminauditlog

import (
	"testing"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// #2173: every kind the Activity filter maps is a canonical kind, and its
// route is owned by an optional module, so a renamed route cannot silently
// make the kind core.
func TestKindModuleRoute_OptionalModules(t *testing.T) {
	t.Parallel()
	for kind, route := range kindModuleRoute {
		if !unifiedlog.IsCanonical(kind) {
			t.Errorf("kind %q is not canonical", kind)
		}
		if o, err := featureflag.Resolve(route); err != nil || o.Module.Core {
			t.Errorf("kind %q: route %s is not owned by an optional module", kind, route)
		}
	}
}

// #2477: a decision is placed by its recorded request path; only a row
// with no recorded path falls back to "/v1/"+target_type.
func TestDecisionRoute(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		e    unifiedlog.Entry
		want string
	}{
		{"me route", unifiedlog.Entry{TargetType: "me", PayloadJSON: []byte(`{"request_path":"/v1/me/acknowledgments"}`)}, "/v1/me/acknowledgments"},
		{"nested route", unifiedlog.Entry{TargetType: "controls", PayloadJSON: []byte(`{"request_path":"/v1/controls/c1/policies"}`)}, "/v1/controls/c1/policies"},
		{"path wins over type", unifiedlog.Entry{TargetType: "policies", PayloadJSON: []byte(`{"request_path":"/v1/controls/c1"}`)}, "/v1/controls/c1"},
		{"legacy empty path", unifiedlog.Entry{TargetType: "policies", PayloadJSON: []byte(`{"request_path":""}`)}, "/v1/policies"},
		{"legacy no payload", unifiedlog.Entry{TargetType: "policies"}, "/v1/policies"},
		{"unplaceable path", unifiedlog.Entry{TargetType: "policies", PayloadJSON: []byte(`{"request_path":"/not-v1/x"}`)}, "/v1/policies"},
		{"nothing to place", unifiedlog.Entry{}, ""},
	}
	for _, c := range cases {
		if got := decisionRoute(c.e); got != c.want {
			t.Errorf("%s: decisionRoute = %q; want %q", c.name, got, c.want)
		}
	}
}

// With nothing hidden, every row stays, in order.
func TestVisibleEntries_NothingHidden(t *testing.T) {
	t.Parallel()
	in := []unifiedlog.Entry{
		{Kind: unifiedlog.KindRisk},
		{Kind: unifiedlog.KindDecision, TargetType: "policies"},
		{Kind: unifiedlog.KindEvidence},
	}
	got := visibleEntries(append([]unifiedlog.Entry(nil), in...), moduleaccess.ModuleSet{})
	if len(got) != len(in) {
		t.Fatalf("visibleEntries kept %d of %d rows", len(got), len(in))
	}
	for i := range in {
		if got[i].Kind != in[i].Kind {
			t.Errorf("row %d kind = %q; want %q", i, got[i].Kind, in[i].Kind)
		}
	}
	if (&Handler{}).WithModules(nil).views != nil {
		t.Errorf("WithModules(nil) set views")
	}
}
