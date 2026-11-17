// authz.go — handler-level defense-in-depth program-read role guard.
//
// AC-5 requires every one of the four slice-066 endpoints to 403 a role
// that lacks dashboard/program-read access. The program-read role set is
// the same as the slice-064 control-read set — auditor + grc_engineer +
// control_owner, plus admin who is a wildcard — because the program
// dashboard (slice 040) is the operator/auditor home screen, the exact
// same audience as the control-detail view slice 064 guards. Reusing the
// identical derivation keeps the two backend-for-frontend slices coherent:
// a credential that can read a control detail can read the program
// dashboard that links to it.
//
// The slice-035 OPA middleware is the PRIMARY authz gate in production.
// This guard is its defense-in-depth twin — the same belt-and-suspenders
// posture slices 059/062/064 adopted. It matters because the OPA engine is
// not wired in unit/integration test servers (api.New(api.Config{}) leaves
// authzEngine nil), so a handler-level check is the testable enforcement
// point. When slice-035's DB-backed user_roles becomes the role source of
// truth, this guard should re-derive from the resolved role set rather
// than the credential flags (the same revisit slice 064's decisions log
// D5 records).
package dashboard

import (
	"net/http"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/httpresp"
)

// hasProgramRead reports whether the credential carries an explicit
// program-read role signal:
//
//	IsAdmin            -> admin         (wildcard — program-read granted)
//	IsApprover         -> grc_engineer  (program-read granted)
//	len(OwnerRoles)>0  -> control_owner (program-read granted)
//
// Deliberately STRICTER than authz.derivedRolesFor, which maps a bare
// tenant credential to grc_engineer. The program dashboard is the
// slice-040 operator/auditor UI, not a connector surface — a bare push
// credential (no flags) has no business reading it. Requiring an explicit
// role signal also makes AC-5 genuinely testable: the "role without
// program-read access" is exactly a credential carrying none of the three
// signals.
func hasProgramRead(c credstore.Credential) bool {
	return c.IsAdmin || c.IsApprover || len(c.OwnerRoles) > 0
}

// requireProgramRead is the guard the dashboard handlers call FIRST —
// before resolving the tenant context — because "may this caller perform
// this action" is logically prior to "which tenant are they". It returns
// true when the request may proceed; on denial it writes a 403 and returns
// false. A missing credential is treated as a denial — the upstream
// bearer-auth middleware would normally have rejected it first, so this is
// purely defense-in-depth.
func requireProgramRead(w http.ResponseWriter, r *http.Request) bool {
	cred, ok := authctx.CredentialFromContext(r.Context())
	if !ok || !hasProgramRead(cred) {
		httpresp.WriteError(w, http.StatusForbidden, "role does not grant dashboard/program-read access")
		return false
	}
	return true
}
