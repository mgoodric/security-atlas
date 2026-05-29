// authz.go — handler-level defense-in-depth program-read role guard for
// the slice-067 org_units read endpoint.
//
// Slice 067 (AC-6) requires GET /v1/org_units — now honoring the
// slice-067 ?include_risk_counts=true query param — to 403 a role that
// lacks risk/program-read access. The program-read role set is the same
// one slices 064 / 066 / and the slice-067 risks package guard with:
// auditor + grc_engineer + control_owner, plus admin as a wildcard.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production;
// this guard is its defense-in-depth twin and the testable enforcement
// point (the OPA engine is nil in api.New(api.Config{}) test servers).
//
// SCOPE: this guard is applied ONLY to GET /v1/org_units (the list, which
// slice 067 extends). It is NOT retrofitted onto the slice-053 org_unit
// write/read-one endpoints (Create, Get, Patch, Delete) — retrofitting
// authz onto untouched endpoints is out of slice-067 scope.
package orgunits

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// hasProgramRead reports whether the credential carries an explicit
// program-read role signal (IsAdmin / IsApprover / a non-empty
// OwnerRoles). Deliberately stricter than authz.derivedRolesFor — a bare
// push credential has no business reading the slice-056 org tree.
func hasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireProgramRead is the guard List calls FIRST — before resolving the
// tenant context. It returns true when the request may proceed; on denial
// it writes a 403 and returns false. A missing credential is treated as a
// denial (defense-in-depth — bearer-auth would normally have rejected it
// upstream).
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-read access")
		return false
	}
	return true
}
