// authz.go — handler-level defense-in-depth program-read role guard for
// the OE-663 personnel-security endpoints.
//
// Personnel checklists carry person PII (external id, work email,
// display name), so every route on this surface — reads AND writes —
// requires an explicit program-read role signal. The role derivation is
// the same one internal/api/risks/authz.go (slice 067) uses: admin as a
// wildcard, grc_engineer (IsApprover), control_owner (OwnerRoles). A
// bare push credential (no flags) has no business on a person-roster
// surface, which also makes the guard genuinely testable.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production
// (the "personnel-security" resource is enrolled in
// policies/authz/grc_engineer.rego's writable set). This guard is its
// defense-in-depth twin — the same belt-and-suspenders posture slices
// 059/062/064/066/067 adopted — and the testable enforcement point,
// because integration test servers leave the OPA engine nil.
package personnelsecurity

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// hasProgramRead reports whether the credential may read or change
// personnel-security checklists: a tenant approver by its live roles
// (authz.IsTenantApprover), as OPA reads it for admin and grc_engineer only.
// #2507 moved it off `IsAdmin || IsApprover || len(OwnerRoles) > 0`, which
// counted a session's role claim (#2381) and admitted roles OPA refuses
// here.
func hasProgramRead(c credstore.Credential) bool {
	return authz.IsTenantApprover(c)
}

// requireProgramRead is the guard every OE-663 handler calls FIRST —
// before resolving the tenant context — because "may this caller
// perform this action" is logically prior to "which tenant are they".
// It returns true when the request may proceed; on denial it writes a
// 403 and returns false.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant personnel-security access")
		return false
	}
	return true
}
