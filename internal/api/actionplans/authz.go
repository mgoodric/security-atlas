// authz.go — handler-level defense-in-depth role guards for the slice-384
// ActionPlan endpoints.
//
// P0-384-9: NO new authz role is introduced. Reads require the same
// risk/program-read signal the slice-067 decisions list guard uses
// (IsAdmin / IsApprover / a non-empty OwnerRoles set — auditor +
// grc_engineer + control_owner, plus admin as a wildcard). Writes require
// the same program-capable signal: an ActionPlan is a risk-register
// mutation, gated by the slice-056 risk_register:write role in production
// via the slice-035 OPA middleware.
//
// The OPA middleware is the PRIMARY authz gate in production; these guards
// are its defense-in-depth twins and the testable enforcement point (the
// OPA engine is nil in api.New(api.Config{}) test servers, so without these
// guards a test server would not enforce role separation at all).
package actionplans

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// hasProgramRead reports whether the credential carries a program-read
// signal. Mirrors the slice-067 decisions guard exactly — a bare push
// credential has no business reading the risk-register timeline.
func hasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// hasProgramWrite reports whether the credential may mutate risk-register
// records. v1 surfaces the slice-056 risk_register:write role through the
// same credential flags; admin / approver / a control-owner role can author
// a remediation commitment. (The OPA middleware enforces the precise
// risk_register:write predicate in production.)
func hasProgramWrite(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireProgramRead guards the read endpoints. On denial it writes 403 and
// returns false. A missing credential is a denial.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-read access")
		return false
	}
	return true
}

// requireProgramWrite guards the mutating endpoints. On denial it writes 403
// and returns false. A missing credential is a denial.
func requireProgramWrite(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-write access")
		return false
	}
	return true
}
