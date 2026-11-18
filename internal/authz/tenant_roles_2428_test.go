package authz_test

// #2428: a tenant admin or grc_engineer signed in with a JWT got
// IsAdmin = IsApprover = false (jwtmw sets both from the super_admin claim
// only), so every handler gate written on those flags refused them while
// OPA admitted them. Handler gates now read authz.IsTenantAdmin /
// authz.IsTenantApprover, which see the live user_roles authzmw resolved
// for the decision (credstore.Credential.TenantRoles).
//
// This file holds the route table both #2428 tiers share and the no-DB
// tests: the two helpers, authzmw stamping the live roles, and the
// agreement check that the handler gate on every switched route admits
// exactly the roles OPA admits there.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/authzmw"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// gate2428 is the predicate a switched handler gate calls.
type gate2428 struct {
	name string
	fn   func(credstore.Credential) bool
}

var (
	tenantAdminGate    = gate2428{"IsTenantAdmin", authz.IsTenantAdmin}
	tenantApproverGate = gate2428{"IsTenantApprover", authz.IsTenantApprover}
	riskReaderGate     = gate2428{"IsTenantRiskReader", authz.IsTenantRiskReader}
	anyRoleGate        = gate2428{"HasAnyTenantRole", authz.HasAnyTenantRole}
)

// route2428 is one switched route and the gate its handler calls.
type route2428 struct {
	method, path string
	gate         gate2428
}

const id2428 = "6f1c2a52-3b0e-4b8e-9a51-1d6a3f0c2428"

// routes2428 lists every route whose handler gate #2428 moved onto a
// helper. Approve-shaped gates are IsTenantApprover where OPA admits
// grc_engineer, and IsTenantAdmin on the resources OPA admits for admin
// only (oscal, board, questionnaires, mcp, action-plans). The /v1/admin/*
// and install-wide gates stay on cred.IsAdmin (#2216 D2) and are not here.
var routes2428 = []route2428{
	// Approve gates, admin or grc_engineer.
	{http.MethodPatch, "/v1/framework-scopes/" + id2428 + "/approve", tenantApproverGate},
	{http.MethodPatch, "/v1/exceptions/" + id2428 + "/approve", tenantApproverGate},
	{http.MethodPatch, "/v1/exceptions/" + id2428 + "/deny", tenantApproverGate},
	{http.MethodPatch, "/v1/exceptions/" + id2428 + "/activate", tenantApproverGate},
	{http.MethodPatch, "/v1/exceptions/" + id2428 + "/revoke", tenantApproverGate},
	{http.MethodPatch, "/v1/policies/" + id2428 + "/approve", tenantApproverGate},
	{http.MethodPost, "/v1/policies/" + id2428 + "/publish", tenantApproverGate},
	{http.MethodPatch, "/v1/policies/" + id2428 + "/review-date", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/approve", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/reject", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/" + id2428 + "/acceptance/revoke", tenantApproverGate},
	{http.MethodPost, "/v1/controls/checklist:generate", tenantApproverGate},
	{http.MethodPost, "/v1/controls/checklist/sections/" + id2428 + ":approve", tenantApproverGate},
	{http.MethodGet, "/v1/dashboard/export", tenantApproverGate},
	{http.MethodGet, "/v1/audit-periods/" + id2428 + "/control-state", tenantApproverGate},
	{http.MethodGet, "/v1/audit-periods/" + id2428 + "/controls/" + id2428 + "/evidence-summary", tenantApproverGate},
	{http.MethodPost, "/v1/walkthroughs", tenantApproverGate},
	{http.MethodPost, "/v1/walkthroughs/" + id2428 + "/attachments", tenantApproverGate},
	{http.MethodPost, "/v1/walkthroughs/" + id2428 + ":finalize", tenantApproverGate},
	// #2447: the taxonomy, posture-target and overlay gates, which read the
	// JWT role claim until then.
	{http.MethodPost, "/v1/risks/categories", tenantApproverGate},
	{http.MethodPatch, "/v1/risks/categories/c1", tenantApproverGate},
	{http.MethodDelete, "/v1/risks/categories/c1", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/archive", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/unarchive", tenantApproverGate},
	{http.MethodPost, "/v1/risks/categories/c1/set-default", tenantApproverGate},
	{http.MethodPost, "/v1/themes", tenantApproverGate},
	{http.MethodPatch, "/v1/themes/t1", tenantApproverGate},
	{http.MethodDelete, "/v1/themes/t1", tenantApproverGate},
	{http.MethodPut, "/v1/framework-scopes/posture-targets/" + id2428, tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays", tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays/" + id2428 + "/retire", tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays/import", tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id2428 + "/approve", tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id2428 + "/reject", tenantApproverGate},
	{http.MethodPost, "/v1/mapping-overlays/remap-proposals/" + id2428 + "/keep-legacy", tenantApproverGate},
	// #2540: the CSF assessment edit gate.
	{http.MethodPut, "/v1/csf/tier", tenantApproverGate},
	{http.MethodPut, "/v1/csf/profiles/target", tenantApproverGate},
	{http.MethodPut, "/v1/csf/profiles/target/selections", tenantApproverGate},
	{http.MethodDelete, "/v1/csf/profiles/target/selections/" + id2428, tenantApproverGate},

	// Approve gates on resources OPA admits for admin only.
	{http.MethodPost, "/v1/oscal/component-claims/" + id2428 + ":accept", tenantAdminGate},
	{http.MethodPost, "/v1/oscal/component-claims/" + id2428 + ":reject", tenantAdminGate},
	{http.MethodPost, "/v1/oscal/component-claims/" + id2428 + ":needs-info", tenantAdminGate},
	{http.MethodPatch, "/v1/oscal/component-claims/" + id2428 + "/scf-anchor", tenantAdminGate},
	{http.MethodPost, "/v1/board/narrative/generate", tenantAdminGate},
	{http.MethodPost, "/v1/board/narrative/generate-all", tenantAdminGate},
	{http.MethodPost, "/v1/board/narrative/approve", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/answers/q1/ai-suggest", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/answers/q1/ai-approve", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/answers/q1/ai-reject", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/answer-runs", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/answer-runs/r1/cancel", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/questions/q1/scf-mapping/ai-suggest", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/questions/q1/scf-mapping/p1/approve", tenantAdminGate},
	{http.MethodPost, "/v1/questionnaires/" + id2428 + "/questions/q1/scf-mapping/p1/reject", tenantAdminGate},
	{http.MethodPost, "/v1/mcp/write-proposals/" + id2428 + "/confirm", tenantAdminGate},
	{http.MethodPost, "/v1/mcp/write-proposals/" + id2428 + "/reject", tenantAdminGate},
	{http.MethodPost, "/v1/action-plans/" + id2428 + "/verify", tenantAdminGate},
	// #2172: a member's module access, set by a tenant admin.
	{http.MethodPut, "/v1/admin/users/" + id2428 + "/module-access", tenantAdminGate},
	// #2503: the tenant's own users, read and re-roled by a tenant admin.
	{http.MethodGet, "/v1/admin/users/" + id2428, tenantAdminGate},
	{http.MethodPatch, "/v1/admin/users/" + id2428 + "/roles", tenantAdminGate},

	// Tenant-admin gates.
	{http.MethodPost, "/v1/audit-periods", tenantAdminGate},
	{http.MethodPost, "/v1/audit-periods/" + id2428 + "/freeze", tenantAdminGate},
	{http.MethodPost, "/v1/audit-periods/" + id2428 + "/populations/" + id2428, tenantAdminGate},
	{http.MethodPost, "/v1/metrics/" + id2428 + "/inputs", tenantAdminGate},
	{http.MethodPut, "/v1/metrics/" + id2428 + "/target", tenantAdminGate},
	{http.MethodPost, "/v1/controls:upload-bundle", tenantAdminGate},
	// #2358: stock controls the boot seed holds back behind a reviewed
	// re-anchor.
	{http.MethodGet, "/v1/admin/controls/held-back", tenantAdminGate},
	// #2110: Evaluate now. The role gate is tenant admin; the handler also
	// admits the control's assigned owner (owner_user_id), whatever their
	// role, which this role-only table cannot express.
	{http.MethodPost, "/v1/controls/" + id2428 + "/evaluate", tenantAdminGate},
	{http.MethodDelete, "/v1/risks/" + id2428, tenantAdminGate},
	// #2134: the Everyone view of the needs-attention list. for=me stays
	// open to every role, so OPA admits the path for all of them.
	{http.MethodGet, "/v1/needs-attention?for=all", tenantApproverGate},

	// #2556: the risk-module read gates, admin, grc_engineer and viewer.
	// The other risk-module reads have no handler role gate; OPA alone
	// decides them (risk_reads_2556_test.go).
	{http.MethodGet, "/v1/risks", riskReaderGate},
	{http.MethodGet, "/v1/risks/theme-heatmap", riskReaderGate},
	{http.MethodGet, "/v1/risks/export", riskReaderGate},
	{http.MethodGet, "/v1/decisions", riskReaderGate},
	{http.MethodGet, "/v1/org_units", riskReaderGate},

	// #2507: the last `len(OwnerRoles) > 0` read gates, on live roles.
	// Action plans: reads admin and grc_engineer, writes admin only; a
	// risk's or control's plan list follows that record's read gate.
	{http.MethodGet, "/v1/action-plans", tenantApproverGate},
	{http.MethodGet, "/v1/action-plans/" + id2428, tenantApproverGate},
	{http.MethodPost, "/v1/action-plans", tenantAdminGate},
	{http.MethodPatch, "/v1/action-plans/" + id2428, tenantAdminGate},
	{http.MethodDelete, "/v1/action-plans/" + id2428, tenantAdminGate},
	{http.MethodPost, "/v1/action-plans/" + id2428 + "/risks/" + id2428, tenantAdminGate},
	{http.MethodDelete, "/v1/action-plans/" + id2428 + "/risks/" + id2428, tenantAdminGate},
	{http.MethodPost, "/v1/action-plans/" + id2428 + "/controls/" + id2428, tenantAdminGate},
	{http.MethodDelete, "/v1/action-plans/" + id2428 + "/controls/" + id2428, tenantAdminGate},
	{http.MethodGet, "/v1/risks/" + id2428 + "/action-plans", riskReaderGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/action-plans", anyRoleGate},
	// The /v1/admin/* exports: admin and grc_engineer read.
	{http.MethodGet, "/v1/admin/samples/export", tenantApproverGate},
	{http.MethodGet, "/v1/admin/evidence/export", tenantApproverGate},
	{http.MethodGet, "/v1/admin/exceptions/export", tenantApproverGate},
	{http.MethodGet, "/v1/admin/policies/export", tenantApproverGate},
	// OSCAL, personnel security and the evidence-summary portfolio:
	// admin and grc_engineer.
	{http.MethodGet, "/v1/oscal/component-definitions", tenantApproverGate},
	{http.MethodGet, "/v1/oscal/component-definitions/" + id2428, tenantApproverGate},
	{http.MethodGet, "/v1/oscal/imported-profiles/" + id2428 + "/provenance", tenantApproverGate},
	{http.MethodGet, "/v1/personnel-security/checklists", tenantApproverGate},
	{http.MethodGet, "/v1/personnel-security/checklists/" + id2428, tenantApproverGate},
	{http.MethodPost, "/v1/personnel-security/checklists", tenantApproverGate},
	{http.MethodPost, "/v1/personnel-security/checklist-items/" + id2428 + "/complete", tenantApproverGate},
	{http.MethodGet, "/v1/evidence-summary/portfolio", tenantApproverGate},
	// Control reads, the activity feed and framework posture: every role.
	// controldetail's gate also guards GET /v1/evidence and
	// /v1/evidence/{id}, where OPA is the narrower layer
	// (read_gates_2507_test.go); those are not rows here.
	{http.MethodGet, "/v1/controls/checklist/" + id2428, anyRoleGate},
	{http.MethodGet, "/v1/controls/checklist/" + id2428 + "/export.md", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/policies", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/risks", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/history", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/anchor-history", anyRoleGate},
	{http.MethodGet, "/v1/controls/export", anyRoleGate},
	{http.MethodGet, "/v1/controls/history/export", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/evidence-summary", anyRoleGate},
	{http.MethodGet, "/v1/controls/" + id2428 + "/gap-explanation", anyRoleGate},
	{http.MethodGet, "/v1/activity", anyRoleGate},
	{http.MethodGet, "/v1/frameworks/posture", anyRoleGate},
}

// tenantRoles2428 are the tenant roles the matrices run, each alone.
var tenantRoles2428 = []authz.Role{
	authz.RoleAdmin, authz.RoleGRCEngineer, authz.RoleControlOwner,
	authz.RoleAuditor, authz.RoleViewer,
}

// wantAdmit2428 is the intended matrix: what each gate admits for a
// signed-in person holding exactly one tenant role.
func wantAdmit2428(g gate2428, role authz.Role) bool {
	switch g.name {
	case tenantAdminGate.name:
		return role == authz.RoleAdmin
	case riskReaderGate.name:
		return role == authz.RoleAdmin || role == authz.RoleGRCEngineer || role == authz.RoleViewer
	case anyRoleGate.name:
		return true
	default:
		return role == authz.RoleAdmin || role == authz.RoleGRCEngineer
	}
}

func TestTenantGates2428_Helpers(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name            string
		cred            credstore.Credential
		admin, approver bool
	}{
		{"admin flag", credstore.Credential{ID: "key_a", IsAdmin: true}, true, true},
		{"approver flag", credstore.Credential{ID: "key_p", IsApprover: true}, false, true},
		{"live admin", credstore.Credential{ID: "jwt:1", TenantRoles: []string{"admin"}}, true, true},
		{"live grc_engineer", credstore.Credential{ID: "jwt:2", TenantRoles: []string{"grc_engineer"}}, false, true},
		{"live control_owner", credstore.Credential{ID: "jwt:3", TenantRoles: []string{"control_owner"}}, false, false},
		{"live auditor", credstore.Credential{ID: "jwt:4", TenantRoles: []string{"auditor"}}, false, false},
		{"live viewer", credstore.Credential{ID: "jwt:5", TenantRoles: []string{"viewer"}}, false, false},
		// A JWT's role claim outlives a revoke (#2381); only live roles count.
		{"JWT admin claim, no live role", credstore.Credential{ID: "jwt:6", OwnerRoles: []string{"admin"}}, false, false},
		{"JWT grc_engineer claim, no live role", credstore.Credential{ID: "jwt:7", OwnerRoles: []string{"grc_engineer"}}, false, false},
		// An API key's owner_roles label keeps counting, as before #2428.
		{"API key admin label", credstore.Credential{ID: "key_8", OwnerRoles: []string{"admin"}}, true, true},
		{"API key grc_engineer label", credstore.Credential{ID: "key_9", OwnerRoles: []string{"grc_engineer"}}, false, true},
		{"bare credential", credstore.Credential{ID: "key_0"}, false, false},
		// #2541: a key bound to a user counts its owner's live roles, not its
		// label, which is a copy taken when the key was minted.
		{"bound key admin label, owner's admin revoked", credstore.Credential{ID: "key_b1", UserID: "u1", OwnerRoles: []string{"admin"}, TenantRoles: []string{"viewer"}}, false, false},
		{"bound key grc_engineer label, owner's role revoked", credstore.Credential{ID: "key_b2", UserID: "u2", OwnerRoles: []string{"grc_engineer"}}, false, false},
		{"bound key grc_engineer label, owner holds it live", credstore.Credential{ID: "key_b3", UserID: "u3", OwnerRoles: []string{"grc_engineer"}, TenantRoles: []string{"grc_engineer"}}, false, true},
		{"unbound key admin label", credstore.Credential{ID: "key_u1", UserID: "key_u1", OwnerRoles: []string{"admin"}}, true, true},
	} {
		if got := authz.IsTenantAdmin(c.cred); got != c.admin {
			t.Errorf("%s: IsTenantAdmin = %v, want %v", c.name, got, c.admin)
		}
		if got := authz.IsTenantApprover(c.cred); got != c.approver {
			t.Errorf("%s: IsTenantApprover = %v, want %v", c.name, got, c.approver)
		}
	}
}

// handlerNarrower2428 lists the route/role pairs where OPA admits a role
// the handler gate refuses. Each predates #2428, which touches only admin
// and grc_engineer: auditor reads of the dashboard export and the audit
// workspace, and control_owner walkthrough writes. The handler stays the
// narrower layer there, so nothing widens; the reverse (a handler
// admitting what OPA refuses) is never allowed.
var handlerNarrower2428 = map[string]bool{
	"/v1/dashboard/export auditor":                                                      true,
	"/v1/audit-periods/" + id2428 + "/control-state auditor":                            true,
	"/v1/audit-periods/" + id2428 + "/controls/" + id2428 + "/evidence-summary auditor": true,
	"/v1/walkthroughs control_owner":                                                    true,
	"/v1/walkthroughs/" + id2428 + "/attachments control_owner":                         true,
	"/v1/walkthroughs/" + id2428 + ":finalize control_owner":                            true,
	// #2110: OPA admits grc_engineer (controls write) and control_owner
	// (the evaluate path rule); the handler admits neither by role, only
	// the control's assigned owner (Matt's call: owner or admin).
	"/v1/controls/" + id2428 + "/evaluate grc_engineer":  true,
	"/v1/controls/" + id2428 + "/evaluate control_owner": true,
	// #2134: OPA admits every role to GET /v1/needs-attention, whose for=me
	// view is anyone's; the view is a query parameter OPA does not see, so
	// the handler alone refuses for=all to the roles below.
	"/v1/needs-attention?for=all control_owner": true,
	"/v1/needs-attention?for=all auditor":       true,
	"/v1/needs-attention?for=all viewer":        true,
	// #2503: grc_engineer.rego's blanket read admits GET /v1/admin/users;
	// the handler has always refused it (cred.IsAdmin before, IsTenantAdmin
	// now). grc_engineer's OPA rules are unchanged by #2503.
	"/v1/admin/users/" + id2428 + " grc_engineer": true,
}

// TestTenantGates2428_HandlerAgreesWithOPA runs the real bundle for every
// switched route and tenant role, and asserts the route's handler gate
// admits a credential holding that live role exactly when OPA does (bar
// the pre-existing handlerNarrower2428 pairs), and that the handler
// matches the intended matrix.
func TestTenantGates2428_HandlerAgreesWithOPA(t *testing.T) {
	t.Parallel()
	e := engine(t)
	for _, rt := range routes2428 {
		for _, role := range tenantRoles2428 {
			r := httptest.NewRequest(rt.method, rt.path, nil)
			r = r.WithContext(authctx.WithCredential(r.Context(), credstore.Credential{
				ID: "jwt:2428", TenantID: "00000000-0000-4000-8000-000000002428", UserID: "user:2428",
			}))
			in := authz.BuildInput(r, nil)
			in.User.Roles = []authz.Role{role}
			d, err := e.Decide(context.Background(), in)
			if err != nil {
				t.Fatalf("%s %s as %s: Decide: %v", rt.method, rt.path, role, err)
			}
			handler := rt.gate.fn(credstore.Credential{ID: "jwt:2428", TenantRoles: []string{string(role)}})
			narrower := handlerNarrower2428[rt.path+" "+string(role)]
			switch {
			case narrower && (handler || !d.Allow):
				t.Errorf("%s %s as %s: listed as handler-narrower, but handler=%v OPA=%v; update the list", rt.method, rt.path, role, handler, d.Allow)
			case !narrower && handler != d.Allow:
				t.Errorf("%s %s as %s: handler %s=%v, OPA allow=%v (%s/%s)", rt.method, rt.path, role, rt.gate.name, handler, d.Allow, in.Resource.Type, in.Action)
			}
			if want := wantAdmit2428(rt.gate, role); handler != want {
				t.Errorf("%s %s as %s: handler %s=%v, want %v", rt.method, rt.path, role, rt.gate.name, handler, want)
			}
		}
	}
}

// staticResolver returns fixed live roles, standing in for user_roles.
type staticResolver []authz.Role

func (s staticResolver) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return s, nil
}

// TestTenantGates2428_MiddlewareStampsLiveRoles: authzmw puts the roles the
// resolver returned on the credential the handler reads, replaces any value
// the credential arrived with, and leaves out the roles derived from the
// credential itself.
func TestTenantGates2428_MiddlewareStampsLiveRoles(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		live staticResolver
		in   credstore.Credential
		want []string
	}{
		{"live admin", staticResolver{authz.RoleAdmin}, credstore.Credential{ID: "jwt:a"}, []string{"admin"}},
		{"live grc_engineer", staticResolver{authz.RoleGRCEngineer}, credstore.Credential{ID: "jwt:g"}, []string{"grc_engineer"}},
		// Claim says admin, user_roles has viewer: the claim is not a live
		// role. (With no live role at all the session is denied, #2496.)
		{"claim admin, live viewer", staticResolver{authz.RoleViewer}, credstore.Credential{ID: "jwt:r", OwnerRoles: []string{"admin"}}, []string{"viewer"}},
		// A value set before authzmw never survives it.
		{"forged TenantRoles", staticResolver{authz.RoleViewer}, credstore.Credential{ID: "jwt:f", OwnerRoles: []string{"control_owner"}, TenantRoles: []string{"admin"}}, []string{"viewer"}},
		// The admin flag derives admin for OPA but is not a live role.
		{"admin flag", nil, credstore.Credential{ID: "key_x", IsAdmin: true}, nil},
	} {
		e, err := authz.NewEngine(context.Background(), c.live)
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		cred := c.in
		cred.TenantID = "00000000-0000-4000-8000-000000002428"
		cred.UserID = "user:00000000-0000-4000-8000-000000002428"
		var got credstore.Credential
		reached := false
		h := authzmw.Middleware(e, nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			reached = true
			got, _ = authctx.CredentialFromContext(r.Context())
		}))
		// A read OPA admits for every principal here, so the handler runs.
		r := httptest.NewRequest(http.MethodGet, "/v1/controls", nil)
		r = r.WithContext(authctx.WithCredential(r.Context(), cred))
		h.ServeHTTP(httptest.NewRecorder(), r)
		if !reached {
			t.Fatalf("%s: handler not reached", c.name)
		}
		if !slices.Equal(got.TenantRoles, c.want) {
			t.Errorf("%s: TenantRoles = %v, want %v", c.name, got.TenantRoles, c.want)
		}
	}
}
