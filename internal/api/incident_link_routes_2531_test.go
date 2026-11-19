package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// incidentWriteRoutes is every write route under /v1/incidents, each with
// how it stays clear of hidden-module links (#2501). Only Create accepts
// risk_ids or vendor_ids, and it refuses links into a module the caller
// cannot use (incidents.refuseHiddenLinks).
var incidentWriteRoutes = map[string]string{
	"POST /v1/incidents":                  "Create: refuseHiddenLinks before the store",
	"PATCH /v1/incidents/{id}/transition": "no links: to_state and summary only",
	"POST /v1/incidents/{id}/close":       "evidence links only: no risk or vendor ids",
}

// TestIncidentRoutes_LinkWritesRefuseHiddenModules (#2531) fails when a
// write route under /v1/incidents is added or removed. A new one (an
// incident update, a link or unlink route) that writes risk or vendor
// links must call refuseHiddenLinks before the store, as Create does, or a
// caller narrowed out of Risk or Third-party risk can link into it and
// probe which ids exist. Review it, then list it in incidentWriteRoutes
// saying how it is guarded.
func TestIncidentRoutes_LinkWritesRefuseHiddenModules(t *testing.T) {
	t.Setenv("ATLAS_TEST_MODE", "1")
	got := map[string]bool{}
	if err := chi.Walk(routeWalkServer(t).buildRouter(), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions &&
			strings.HasPrefix(route, "/v1/incidents") {
			got[method+" "+route] = true
		}
		return nil
	}); err != nil {
		t.Fatalf("chi.Walk: %v", err)
	}
	for route := range got {
		if _, ok := incidentWriteRoutes[route]; !ok {
			t.Errorf("%s is a new incident write route: if it writes risk or vendor links it must call refuseHiddenLinks before the store (#2501); then list it in incidentWriteRoutes", route)
		}
	}
	for route := range incidentWriteRoutes {
		if !got[route] {
			t.Errorf("%s is listed in incidentWriteRoutes but not registered; remove it", route)
		}
	}
}
