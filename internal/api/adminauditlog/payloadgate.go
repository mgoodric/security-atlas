package adminauditlog

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/audit/unifiedlog"
	"github.com/mgoodric/security-atlas/internal/auth/jwtmw"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// kindSourceResource names, for each unified-log kind, the OPA resource
// type whose read decides whether a caller may see that kind's payload
// (#2375). A caller who could not read the row's source resource gets the
// row's envelope (time, actor, kind, target, action) with a null payload,
// so the trail of who did what stays complete while the content stays
// behind the source's own read rule.
//
// Kinds absent from this map (and `me`, handled by payloadGate.keep) keep
// no payload for a non-admin caller: a new kind fails closed until it is
// mapped here.
var kindSourceResource = map[unifiedlog.Kind]string{
	// decision_audit_log is read at /v1/admin/audit-log.
	unifiedlog.KindDecision:  "admin",
	unifiedlog.KindEvidence:  "evidence",
	unifiedlog.KindException: "exceptions",
	// Samples are read through their audit period; the per-period
	// samples rule needs attrs a kind-wide decision does not have.
	unifiedlog.KindSample:          "audit-periods",
	unifiedlog.KindAuditPeriod:     "audit-periods",
	unifiedlog.KindAggregationRule: "aggregation-rules",
	// Feature flags are read at /v1/admin/features.
	unifiedlog.KindFeatureFlag:    "admin",
	unifiedlog.KindWalkthrough:    "walkthroughs",
	unifiedlog.KindFrameworkScope: "framework-scopes",
	// Taxonomy rows are risk categories, read at /v1/risks/categories.
	unifiedlog.KindTaxonomy:       "risks",
	unifiedlog.KindMappingOverlay: "mapping-overlays",
	// Control import rows are read with the controls they create.
	unifiedlog.KindControlImport: "controls",
}

// payloadGate decides, per row, whether the caller sees payload_json. It
// evaluates the caller's OPA read once per source resource and caches it,
// so a page costs at most one decision per distinct resource.
type payloadGate struct {
	r       *http.Request
	engine  *authz.Engine
	admin   bool
	self    string
	allowed map[string]bool
}

// newPayloadGate builds the gate for one request. self is the caller's
// identity as it appears in a me row's actor_id. An admin keeps every
// payload. Without an engine a non-admin keeps none: the gate fails
// closed rather than trusting the route-level admit.
func (h *Handler) newPayloadGate(r *http.Request, self string) *payloadGate {
	// A JWT caller's id carries a "user:" prefix that me_audit_log's
	// user_id does not.
	g := &payloadGate{r: r, engine: h.engine, self: jwtmw.SubjectUserID(self), allowed: map[string]bool{}}
	cred, _ := authctx.CredentialFromContext(r.Context())
	g.admin = cred.IsAdmin || g.decide("write", "admin")
	return g
}

// keep reports whether e's payload may be shown to the caller.
func (g *payloadGate) keep(e unifiedlog.Entry) bool {
	if g.admin {
		return true
	}
	if e.Kind == unifiedlog.KindMe {
		// A me row is another user's own settings and activity; only
		// that user (or an admin, above) sees its before/after.
		return g.self != "" && e.ActorID == g.self
	}
	resource, ok := kindSourceResource[e.Kind]
	if !ok {
		return false
	}
	allow, seen := g.allowed[resource]
	if !seen {
		allow = g.decide("read", resource)
		g.allowed[resource] = allow
	}
	return allow
}

// apply nulls the payload of every entry the caller may not see.
func (g *payloadGate) apply(entries []unifiedlog.Entry) {
	for i := range entries {
		if !g.keep(entries[i]) {
			entries[i].PayloadJSON = nil
		}
	}
}

// decide runs one OPA decision for the caller against a resource type.
// A nil engine or an engine error denies.
func (g *payloadGate) decide(action, resource string) bool {
	if g.engine == nil {
		return false
	}
	in := authz.BuildInput(g.r, nil)
	in.Action = action
	in.Resource.Type = resource
	in.Resource.ID = ""
	d, err := g.engine.Decide(g.r.Context(), in)
	return err == nil && d.Allow
}
