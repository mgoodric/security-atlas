// authz.go — handler-level defense-in-depth program-read role guard for
// the slice-067 risk read endpoints.
//
// Slice 067 (AC-6) requires the risk read endpoints it touches —
// GET /v1/risks (the list, now carrying the slice-067 ?theme=/?org_unit=
// filters) and GET /v1/risks/theme-heatmap (new) — to 403 a role that
// lacks risk/program-read access. The program-read role set is the same
// one slices 064 (control-detail) and 066 (dashboard) guard with: auditor
// + grc_engineer + control_owner, plus admin as a wildcard. Slice 056's
// hierarchical risk dashboard is the CISO/program-lead surface — the exact
// operator/auditor audience the dashboard slice serves — so reusing the
// identical derivation keeps the backend-for-frontend slices coherent.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production.
// This guard is its defense-in-depth twin — the same belt-and-suspenders
// posture slices 059/062/064/066 adopted. It matters because the OPA
// engine is not wired in unit/integration test servers (api.New(
// api.Config{}) leaves authzEngine nil), so a handler-level check is the
// testable enforcement point. When slice-035's DB-backed user_roles
// becomes the role source of truth, this guard should re-derive from the
// resolved role set rather than the credential flags (the same revisit
// slices 064/066 record in their decisions logs).
//
// #2556: the read gate is now authz.IsTenantRiskReader — admin,
// grc_engineer and viewer by live tenant role, the set OPA admits on
// `risks` reads. control_owner and auditor no longer pass the handler; OPA
// already refused them, so nothing they could reach changes.
//
// SCOPE: this guard is applied ONLY to the read endpoints slice 067
// touches. It is NOT retrofitted onto the slice-019/020/053 risk write
// endpoints (CreateRisk, LinkControl, DeleteRisk, Aggregate, theme
// assignment) — those keep their existing posture; retrofitting authz onto
// untouched endpoints is out of slice-067 scope and would risk other
// slices' tests.
package risks

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// hasProgramRead reports whether the credential may read the risk register:
// authz.IsTenantRiskReader, which counts the live tenant role authzmw
// resolved, never a session's role claim (#2556). It admits admin,
// grc_engineer and viewer, the roles OPA admits on `risks` reads, and
// refuses control_owner and auditor, which OPA refuses there too.
//
// Deliberately STRICTER than authz.derivedRolesFor, which maps a bare
// tenant credential to grc_engineer: a bare push credential (no flag, no
// label) has no business reading the register.
func hasProgramRead(c credstore.Credential) bool {
	return authz.IsTenantRiskReader(c)
}

// requireProgramRead is the guard the slice-067 read handlers call FIRST —
// before resolving the tenant context — because "may this caller perform
// this action" is logically prior to "which tenant are they". It returns
// true when the request may proceed; on denial it writes a 403 and
// returns false. A missing credential is treated as a denial — the
// upstream bearer-auth middleware would normally have rejected it first,
// so this is purely defense-in-depth.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-read access")
		return false
	}
	return true
}

// requireTaxonomyAdmin is the guard the #1810 category write handlers call
// first. The OPA middleware is the primary gate in production (writes under
// /v1/risks are the grc_engineer and admin `risks` resource); this is its
// defense-in-depth twin and the testable enforcement point.
func requireTaxonomyAdmin(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !authz.IsTenantApprover(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk taxonomy management")
		return false
	}
	return true
}
