package calendar

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// #2173: every event type maps to a route the module registry places, and
// a viewer that sees every module keeps the filter as asked.
func TestEventTypeRoutes_CoverEveryType(t *testing.T) {
	t.Parallel()
	for _, typ := range strings.Split(validEventTypes, ",") {
		route, ok := eventTypeRoutes[typ]
		if !ok {
			t.Errorf("event type %q has no module route", typ)
			continue
		}
		if _, err := featureflag.Resolve(route); err != nil {
			t.Errorf("event type %q: route %s: %v", typ, route, err)
		}
	}
	var views *moduleaccess.Views
	viewer, err := views.For(httptest.NewRequest("GET", "/v1/calendar", nil))
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	filter, left, err := VisibleTypes(viewer, "policy,vendor")
	if err != nil || !left || filter != "policy,vendor" {
		t.Errorf("VisibleTypes = %q, %v, %v; want policy,vendor kept", filter, left, err)
	}
	if (&Handler{}).WithModules(nil).views != nil {
		t.Errorf("WithModules(nil) set views")
	}
}
