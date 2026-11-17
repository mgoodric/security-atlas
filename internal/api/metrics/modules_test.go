package metrics

import (
	"net/http/httptest"
	"testing"

	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// #2173: a metric follows its per-metric module, else its category's, else
// it is core. With no views wired every metric is shown.
func TestSeesMetric_ModuleMapping(t *testing.T) {
	t.Parallel()
	h := New(nil)
	set, err := h.moduleSet(httptest.NewRequest("GET", "/v1/metrics", nil))
	if err != nil {
		t.Fatalf("moduleSet: %v", err)
	}
	for _, m := range []struct{ id, category string }{
		{"vendor_reviews_due", "vendor"},
		{"top_risks_residual", "risk"},
		{"policy_attestation_rate", "people"},
		{"control_pass_rate", "posture"},
	} {
		if !seesMetric(set, m.id, m.category) {
			t.Errorf("no views: %s hidden; want shown", m.id)
		}
	}
	if (&Handler{}).WithModules(nil).views != nil {
		t.Errorf("WithModules(nil) set views")
	}
}

// Every route the mapping names is one the module registry places under
// an optional module, so a renamed route cannot silently make a metric
// core.
func TestMetricModuleRoutes_AreOptionalModules(t *testing.T) {
	t.Parallel()
	all := moduleaccess.ModuleSet{}
	for name, routes := range map[string]map[string]string{"category": categoryModuleRoute, "metric": metricModuleRoute} {
		for key, route := range routes {
			if !all.Sees(route) {
				t.Errorf("%s %s: zero ModuleSet hides %s", name, key, route)
			}
			if o, err := featureflag.Resolve(route); err != nil || o.Module.Core {
				t.Errorf("%s %s: route %s is not owned by an optional module", name, key, route)
			}
		}
	}
}
