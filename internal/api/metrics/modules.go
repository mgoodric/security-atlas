package metrics

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/mgoodric/security-atlas/internal/api/httperr"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// WithModules wires the module views (#2173): a metric that measures a
// module the caller may not see, because it is switched off or outside
// their module access, is left out of the catalog and the cascade, and
// reads as not found. With none every metric is shown.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// categoryModuleRoute names a route of the module a whole catalog
// category measures; metricModuleRoute overrides it per metric. Both are
// read through the module registry, so the registry decides which module
// owns each route. A metric in neither map measures core work (controls,
// evidence, audits, the programme) and is always shown.
var categoryModuleRoute = map[string]string{
	"risk":   "/v1/risks",
	"vendor": "/v1/vendors",
}

var metricModuleRoute = map[string]string{
	// Catalogued under risk, but measures exception expiries.
	"exception_expiration_runway": "/v1/exceptions",
	"policy_attestation_rate":     "/v1/policies",
	"hire_onboarding_completion":  "/v1/personnel-security",
	"offboarding_completion":      "/v1/personnel-security",
}

// seesMetric reports whether the metric id in category may be shown.
func seesMetric(set moduleaccess.ModuleSet, id, category string) bool {
	if route, ok := metricModuleRoute[id]; ok {
		return set.Sees(route)
	}
	if route, ok := categoryModuleRoute[category]; ok {
		return set.Sees(route)
	}
	return true
}

// hideMetric answers a per-metric sub-route (#2477: observations, inputs,
// target) for a metric the caller may not see, as GetCatalog does: it
// writes 404 and returns true. It also returns true, after writing a 500,
// when the answer cannot be had, so the caller never serves the metric's
// values on an error. A metric with no catalog row is placed by its id
// alone; the sub-route's own lookup answers whether it exists.
func (h *Handler) hideMetric(w http.ResponseWriter, r *http.Request, id string) bool {
	set, err := h.moduleSet(r)
	if err != nil {
		httperr.WriteInternal(w, r, "metrics module access", err)
		return true
	}
	category := ""
	row, err := dbx.New(h.pool).GetMetricCatalog(r.Context(), id)
	switch {
	case err == nil:
		category = row.Category
	case !errors.Is(err, pgx.ErrNoRows):
		httperr.WriteInternal(w, r, "metrics catalog lookup", err)
		return true
	}
	if !seesMetric(set, id, category) {
		httpresp.WriteError(w, http.StatusNotFound, "metric not found")
		return true
	}
	return false
}

// moduleSet resolves the caller's visible modules once per request.
func (h *Handler) moduleSet(r *http.Request) (moduleaccess.ModuleSet, error) {
	viewer, err := h.views.For(r)
	if err != nil {
		return moduleaccess.ModuleSet{}, err
	}
	return viewer.Modules()
}
