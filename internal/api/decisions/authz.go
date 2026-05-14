// authz.go — handler-level defense-in-depth program-read role guard for
// the slice-067 decisions read endpoint.
//
// Slice 067 (AC-6) requires GET /v1/decisions — now carrying the
// slice-067 ?constraints= / ?decision_maker= / ?revisit_by_from= /
// ?revisit_by_to= filters — to 403 a role that lacks risk/program-read
// access. The program-read role set is the same one slices 064 / 066 and
// the slice-067 risks + orgunits package guards use: auditor +
// grc_engineer + control_owner, plus admin as a wildcard.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production;
// this guard is its defense-in-depth twin and the testable enforcement
// point (the OPA engine is nil in api.New(api.Config{}) test servers).
//
// SCOPE: this guard is applied ONLY to GET /v1/decisions (the list, which
// slice 067 extends with richer filters). It is NOT retrofitted onto the
// slice-055 Decision Log write/read-one/linkage endpoints — retrofitting
// authz onto untouched endpoints is out of slice-067 scope.
package decisions

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
)

// hasProgramRead reports whether the credential carries an explicit
// program-read role signal (IsAdmin / IsApprover / a non-empty
// OwnerRoles). Deliberately stricter than authz.derivedRolesFor — a bare
// push credential has no business reading the slice-056 decision timeline.
func hasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireProgramRead is the guard ListDecisions calls FIRST — before
// resolving the tenant context. It returns true when the request may
// proceed; on denial it writes a 403 and returns false. A missing
// credential is treated as a denial (defense-in-depth — bearer-auth would
// normally have rejected it upstream).
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		writeError(w, http.StatusForbidden, "role does not grant risk/program-read access")
		return false
	}
	return true
}
