package adminauditlog

import (
	"encoding/json"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// WithModules wires the module views (#2173) that leave rows from a
// module the caller may not see out of GET /v1/activity/unified. The
// admin forensic surfaces (/v1/admin/audit-log/...) are not filtered.
// With none every module is shown.
func (h *Handler) WithModules(v *moduleaccess.Views) *Handler {
	h.views = v
	return h
}

// kindModuleRoute names, for each unified-log kind owned by an optional
// module, a route of that module, so the module registry decides which
// module a row belongs to. Kinds absent here are core.
var kindModuleRoute = map[unifiedlog.Kind]string{
	unifiedlog.KindException:       "/v1/exceptions",
	unifiedlog.KindAggregationRule: "/v1/aggregation-rules",
	// Taxonomy rows are risk categories.
	unifiedlog.KindTaxonomy: "/v1/risks",
	unifiedlog.KindRisk:     "/v1/risks",
}

// visibleEntries drops the rows whose module the caller may not see: a
// kind owned by a hidden module, or an authorization decision on a hidden
// module's route. It runs after the page and cursor are cut, so paging is
// unchanged and a filtered page can come back short. It must run before
// the #2375 payload gate, which can clear the payload that carries a
// decision's request path.
func visibleEntries(entries []unifiedlog.Entry, set moduleaccess.ModuleSet) []unifiedlog.Entry {
	out := entries[:0]
	for _, e := range entries {
		if route, ok := kindModuleRoute[e.Kind]; ok && !set.Sees(route) {
			continue
		}
		if e.Kind == unifiedlog.KindDecision {
			if route := decisionRoute(e); route != "" && !set.Sees(route) {
				continue
			}
		}
		out = append(out, e)
	}
	return out
}

// decisionRoute is the route an authorization decision was made on: the
// request path the decision log recorded (#2477), so a decision on a
// nested or /v1/me route (/v1/controls/{id}/policies,
// /v1/me/acknowledgments) belongs to the module that owns that route.
// Only a legacy row with no recorded path falls back to its target_type,
// the OPA resource type, which names the module's list route.
func decisionRoute(e unifiedlog.Entry) string {
	var p struct {
		RequestPath string `json:"request_path"`
	}
	if len(e.PayloadJSON) > 0 && json.Unmarshal(e.PayloadJSON, &p) == nil && p.RequestPath != "" {
		return p.RequestPath
	}
	if e.TargetType == "" {
		return ""
	}
	return "/v1/" + e.TargetType
}
