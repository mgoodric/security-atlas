// authz.go — handler-level defense-in-depth control-read role guard.
//
// AC-5 requires every one of the four endpoints to 403 a role that lacks
// control-read access (the control-read role set is auditor + grc_engineer +
// control_owner per slice 025/035, plus admin who is a wildcard).
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production. This
// guard is its defense-in-depth twin — the same belt-and-suspenders posture
// slices 059/062 adopted with their handler-level cred.IsAdmin checks. It
// matters because the OPA engine is not wired in unit/integration test
// servers (api.New(api.Config{}) leaves authzEngine nil), so a handler-level
// check is the testable enforcement point.
//
// The role derivation mirrors internal/authz/input.go derivedRolesFor: a
// credstore.Credential's flags map to the canonical role. When slice-035's
// DB-backed user_roles becomes the role source of truth, this guard should
// re-derive from the resolved role set rather than the credential flags
// (decisions log D5, "Revisit once in use").
package controldetail

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// hasControlRead reports whether the credential carries an explicit
// control-read role signal. The control-read role set is:
//
//	IsAdmin            -> admin         (wildcard — control-read granted)
//	IsApprover         -> grc_engineer  (control-read granted)
//	len(OwnerRoles)>0  -> control_owner (control-read granted)
//
// This guard is deliberately STRICTER than authz.derivedRolesFor, which maps
// a bare tenant credential to grc_engineer. The control-detail read surface
// is the slice-041 operator/auditor UI, not a connector surface — a bare
// push credential (no flags) has no business reading it. Requiring an
// explicit role signal also makes AC-5 genuinely testable: the "role without
// control-read access" is exactly a credential carrying none of the three
// signals (the v1 representation of a viewer-only credential, which
// credstore does not issue first-class). When slice-035's DB-backed
// user_roles becomes the role source of truth, this should re-derive from
// the resolved role set — see decisions log D5.
func hasControlRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireControlRead is the guard the four handlers call FIRST — before
// resolving the tenant context — because "may this caller perform this
// action" is logically prior to "which tenant are they". It returns true
// when the request may proceed; on denial it writes a 403 and returns
// false. A missing credential is treated as a denial — the upstream
// bearer-auth middleware would normally have rejected it first, so this is
// purely defense-in-depth.
func requireControlRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasControlRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant control-read access")
		return false
	}
	return true
}
