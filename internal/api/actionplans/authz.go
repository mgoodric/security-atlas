// authz.go — handler-level defense-in-depth role guards for the slice-384
// ActionPlan endpoints.
//
// P0-384-9: NO new authz role is introduced. The OPA middleware is the
// PRIMARY authz gate in production; these guards are its defense-in-depth
// twins. #2507 moved them off the `IsAdmin || IsApprover || len(OwnerRoles)
// > 0` signal, which counted a session's role claim, onto the live-role
// helpers in internal/authz, each matched to what OPA admits on the route:
//
//   - /v1/action-plans reads (type action-plans): admin and grc_engineer,
//     authz.IsTenantApprover.
//   - /v1/action-plans writes: admin only (action-plans is not in
//     grc_engineer's writable set), authz.IsTenantAdmin.
//   - GET /v1/risks/{id}/action-plans (type risks): the risk readers,
//     authz.IsTenantRiskReader.
//   - GET /v1/controls/{id}/action-plans (type controls): any tenant role,
//     authz.HasAnyTenantRole.
package actionplans

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// requireProgramRead guards the /v1/action-plans read endpoints. On denial
// it writes 403 and returns false. A missing credential is a denial.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	return admit(w, ok && authz.IsTenantApprover(cred), "role does not grant risk/program-read access")
}

// requireProgramWrite guards the mutating endpoints. On denial it writes 403
// and returns false. A missing credential is a denial.
func requireProgramWrite(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	return admit(w, ok && authz.IsTenantAdmin(cred), "role does not grant risk/program-write access")
}

// admit writes 403 with msg when allowed is false, and returns allowed.
func admit(w http.ResponseWriter, allowed bool, msg string) bool {
	if !allowed {
		httpresp.WriteError(w, http.StatusForbidden, msg)
	}
	return allowed
}
