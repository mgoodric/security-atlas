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
// #2556: the gate is now authz.IsTenantRiskReader — admin, grc_engineer
// and viewer by live tenant role. viewer.rego admits viewer to `decisions`
// reads in the same change, so the two layers admit the same set.
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
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// hasProgramRead reports whether the credential may read the decision log:
// authz.IsTenantRiskReader, which counts the live tenant role authzmw
// resolved, never a session's role claim (#2556). It admits admin,
// grc_engineer and viewer, the roles OPA admits on `decisions` reads.
// Deliberately stricter than authz.derivedRolesFor — a bare push
// credential has no business reading the slice-056 decision timeline.
func hasProgramRead(c credstore.Credential) bool {
	return authz.IsTenantRiskReader(c)
}

// requireProgramRead is the guard ListDecisions calls FIRST — before
// resolving the tenant context. It returns true when the request may
// proceed; on denial it writes a 403 and returns false. A missing
// credential is treated as a denial (defense-in-depth — bearer-auth would
// normally have rejected it upstream).
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant risk/program-read access")
		return false
	}
	return true
}
