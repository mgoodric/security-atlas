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
