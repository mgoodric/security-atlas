package adminauditlog

import (
	"encoding/json"

	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/featureflag"
	"github.com/mgoodric/security-atlas/internal/moduleaccess"
)

// WithModules wires the module views (#2173) that leave rows from a
// module the caller may not see out of GET /v1/activity/unified. The
// admin forensic surface GET /v1/admin/audit-log/unified is not filtered:
// it marks rows from switched-off modules instead (#2590). With none
// every module is shown.
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
	// Vendor owner changes (#2450) belong to the vendors module.
	unifiedlog.KindVendor: "/v1/vendors",
}

// visibleEntries drops the rows whose module the caller may not see: a
// kind owned by a hidden module, or an authorization decision on a hidden
// module's route. It must run before the #2375 payload gate, which can
// clear the payload that carries a decision's request path.
func visibleEntries(entries []unifiedlog.Entry, set moduleaccess.ModuleSet) []unifiedlog.Entry {
	out := entries[:0]
	for _, e := range entries {
		if entrySeen(e, set) {
			out = append(out, e)
		}
	}
	return out
}

// entrySeen reports whether set shows the module that owns e: the module
// its kind belongs to, or, for an authorization decision, the module that
// owns the route it was made on. Core rows are always shown.
func entrySeen(e unifiedlog.Entry, set moduleaccess.ModuleSet) bool {
	if route, ok := kindModuleRoute[e.Kind]; ok && !set.Sees(route) {
		return false
	}
	if e.Kind == unifiedlog.KindDecision {
		if route := decisionRoute(e); route != "" && !set.Sees(route) {
			return false
		}
	}
	return true
}

// maxActivityScans caps the aggregator reads one Activity page makes while
// it skips rows from hidden modules. A page that reaches the cap comes back
// short, with a cursor at the last row read, so the next page resumes past
// the hidden run rather than repeating it.
const maxActivityScans = 10

// activityPage reads one Activity page of up to pageSize rows the caller
// may see (#2590). Rows from hidden modules are skipped before the page is
// cut, so a page is full whenever more visible rows exist, and the next
// cursor is set only when they do: the page size and the cursor describe
// what the caller sees, not the rows read. params.Limit must be pageSize+1.
func activityPage(
	params unifiedlog.QueryParams, pageSize int, set moduleaccess.ModuleSet,
	query func(unifiedlog.QueryParams) ([]unifiedlog.Entry, error),
) ([]unifiedlog.Entry, *unifiedlog.Cursor, error) {
	out := make([]unifiedlog.Entry, 0, pageSize)
	for range maxActivityScans {
		got, err := query(params)
		if err != nil {
			return nil, nil, err
		}
		for _, e := range got {
			if !entrySeen(e, set) {
				continue
			}
			if len(out) == pageSize {
				return out, cursorAt(out[pageSize-1]), nil
			}
			out = append(out, e)
		}
		if len(got) < params.Limit {
			return out, nil, nil
		}
		params.Cursor = cursorAt(got[len(got)-1])
	}
	return out, params.Cursor, nil
}

func cursorAt(e unifiedlog.Entry) *unifiedlog.Cursor {
	return &unifiedlog.Cursor{OccurredAt: e.OccurredAt, Kind: e.Kind, RowID: e.RowID}
}

// markSwitchedOff reports, per row, whether its module is switched off
// for the tenant (#2590). The admin audit log marks those rows rather than
// hiding them: it is the forensic record, which ADR 0025 decision 2 keeps
// whole, and an auditor reviewing the period must still see what happened
// in a module before it was switched off.
func markSwitchedOff(entries []unifiedlog.Entry, switches moduleaccess.ModuleSet) []bool {
	out := make([]bool, len(entries))
	for i, e := range entries {
		out[i] = !entrySeen(e, switches)
	}
	return out
}

// decisionRoute is the route an authorization decision was made on: the
// request path the decision log recorded (#2477), so a decision on a
// nested or /v1/me route (/v1/controls/{id}/policies,
// /v1/me/acknowledgments) belongs to the module that owns that route.
// A legacy row with no recorded path, or a path the module registry
// cannot place, falls back to its target_type, the OPA resource type,
// which names the module's list route.
func decisionRoute(e unifiedlog.Entry) string {
	var p struct {
		RequestPath string `json:"request_path"`
	}
	if len(e.PayloadJSON) > 0 && json.Unmarshal(e.PayloadJSON, &p) == nil && p.RequestPath != "" {
		if _, err := featureflag.Resolve(p.RequestPath); err == nil {
			return p.RequestPath
		}
	}
	if e.TargetType == "" {
		return ""
	}
	return "/v1/" + e.TargetType
}
