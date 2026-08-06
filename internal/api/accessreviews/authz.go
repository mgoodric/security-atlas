// authz.go — handler-level defense-in-depth role guards for the OE-670
// access-review endpoints.
//
// Access-review campaigns carry entitlement PII (principal emails, user
// ids, per-system entitlements), so every route on this surface — reads
// AND writes — requires an explicit program role signal. The derivation
// is the one internal/api/actionplans (slice 384) and
// internal/api/personnelsecurity (OE-663) use: admin as a wildcard,
// grc_engineer (IsApprover), control_owner (OwnerRoles). A bare push
// credential (no flags) has no business on an entitlement-roster
// surface, which also makes the guard genuinely testable.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production
// (the "access-reviews" resource is enrolled in
// policies/authz/grc_engineer.rego's writable set). These guards are its
// defense-in-depth twins — the same belt-and-suspenders posture slices
// 059/062/064/066/067/384 and OE-663 adopted — and the testable
// enforcement point, because integration test servers leave the OPA
// engine nil.
//
// Read and write guards share one derivation today but stay distinct
// functions (the actionplans shape) so a future reviewer-only read role
// can diverge without touching call sites.
package accessreviews

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// hasProgramRead reports whether the credential carries an explicit
// program-read role signal. Identical derivation to
// internal/api/personnelsecurity.hasProgramRead — deliberately stricter
// than authz.derivedRolesFor, which maps a bare tenant credential to
// grc_engineer.
func hasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// hasProgramWrite reports whether the credential may mutate campaign
// state (create, attest, complete). Same derivation as hasProgramRead
// in v1: an attestation is additionally bound to the item's assigned
// reviewer at the handler layer, so the role guard only answers "may
// this credential touch the surface at all".
func hasProgramWrite(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireProgramRead guards the read endpoints. On denial it writes a
// 403 and returns false. A missing credential is a denial.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant access-review read access")
		return false
	}
	return true
}

// requireProgramWrite guards the mutating endpoints. On denial it
// writes a 403 and returns false. A missing credential is a denial.
func requireProgramWrite(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramWrite(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant access-review write access")
		return false
	}
	return true
}
