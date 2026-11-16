//go:build integration

// Integration tests for the #2375 payload gate: a unified-log row whose
// source resource the caller cannot read keeps its envelope but carries
// a null payload_json, on the activity feed, the unified list and the
// export alike.
package adminauditlog_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/adminauditlog"
	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/authz"
)

// fixedRoles resolves every user to one role, so the OPA decision the
// gate makes is the role's real policy decision.
type fixedRoles struct{ role authz.Role }

func (f fixedRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return []authz.Role{f.role}, nil
}

// gatedTables is every audit-log table seedUnifiedRow can fill, so each
// kind the gate maps is seeded at least once.
var gatedTables = []string{
	"decision_audit_log",
	"evidence_audit_log",
	"exception_audit_log",
	"sample_audit_log",
	"audit_period_audit_log",
	"feature_flag_audit_log",
	"me_audit_log",
	"walkthrough_audit_log",
	"aggregation_rule_audit_log",
	"taxonomy_audit_log",
	"fw_to_scf_overlay_edge_audit_log",
}

// keepsPayload is, per role, the kinds whose source resource the role's
// OPA policy lets it read (the #2375 role x kind table). A kind absent
// here must come back with a null payload. me rows are judged apart:
// only the caller's own keep their payload.
var keepsPayload = map[authz.Role]map[string]bool{
	authz.RoleAuditor: {
		"exception": true, "sample": true, "audit_period": true, "walkthrough": true,
	},
	authz.RoleControlOwner: {
		"evidence": true, "walkthrough": true,
	},
	authz.RoleViewer: {
		"taxonomy": true,
	},
	authz.RoleGRCEngineer: {
		"decision": true, "evidence": true, "exception": true, "sample": true,
		"audit_period": true, "feature_flag": true, "walkthrough": true,
		"aggregation_rule": true, "taxonomy": true, "mapping_overlay": true,
	},
}

// keeps reports whether a caller holding role sees a kind's payload. The
// test credential has the shape jwtmw synthesizes for a human (the JWT's
// tenant roles in OwnerRoles), which authz.BuildInput derives to
// control_owner; the engine then adds role. So the caller reads what
// either role reads, exactly as the authz middleware decides its routes.
func keeps(role authz.Role, kind string) bool {
	return keepsPayload[role][kind] || keepsPayload[authz.RoleControlOwner][kind]
}

// newGateRouter mounts the three unified-log routes behind a credential
// for a non-admin user holding role, with a real OPA engine.
func newGateRouter(t *testing.T, tenantID uuid.UUID, userID string, role authz.Role) http.Handler {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), fixedRoles{role: role})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	h := adminauditlog.New(appPool).WithAuthz(engine)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID:         "jwt:test-gate",
				TenantID:   tenantID.String(),
				UserID:     userID,
				OwnerRoles: []string{string(role)},
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get("/v1/activity/unified", h.ActivityList)
	r.Get("/v1/admin/audit-log/unified", h.UnifiedList)
	r.Get("/v1/admin/audit-log/export", h.ExportUnified)
	return r
}

// seedRole grants role to userID in user_roles, which the unified and
// export routes' defense-in-depth gate reads.
func seedRole(t *testing.T, tenantID uuid.UUID, userID string, role authz.Role) {
	t.Helper()
	ctx := context.Background()
	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("seed role begin: %v", err)
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenantID.String()); err != nil {
		t.Fatalf("seed role set_config: %v", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-seeder')`,
		tenantID, userID, string(role)); err != nil {
		t.Fatalf("seed role insert: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("seed role commit: %v", err)
	}
	t.Cleanup(func() {
		_, _ = appPool.Exec(context.Background(),
			`DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenantID, userID)
	})
}

// gateRow is the part of a returned row the assertions need.
type gateRow struct {
	kind    string
	actor   string
	payload bool
}

// seedGateTenant seeds one row per gated table, plus a me row of the
// caller's own and one of another user.
func seedGateTenant(t *testing.T, tenant, caller uuid.UUID) {
	t.Helper()
	cleanupUnifiedTables(t, tenant)
	for _, tbl := range gatedTables {
		seedUnifiedRow(t, tenant, tbl)
	}
	seedMeRowForActor(t, tenant, caller, "profile.update")
}

// assertGated checks every row against the role's table, and that every
// kind the caller is shown did come back (so a missing row cannot pass).
// wantOtherMe says whether another user's me row is in the caller's view.
func assertGated(t *testing.T, role authz.Role, caller string, rows []gateRow, wantKinds []string, wantOtherMe bool) {
	t.Helper()
	seen := map[string]bool{}
	ownMe, otherMe := false, false
	for _, row := range rows {
		seen[row.kind] = true
		want := keeps(role, row.kind)
		if row.kind == "me" {
			want = row.actor == caller
			if want {
				ownMe = true
			} else {
				otherMe = true
			}
		}
		if row.payload != want {
			t.Errorf("%s: kind %s (actor %s) payload present = %v; want %v",
				role, row.kind, row.actor, row.payload, want)
		}
	}
	for _, k := range wantKinds {
		if !seen[k] {
			t.Errorf("%s: no %s row returned; the gate must null payloads, not hide rows", role, k)
		}
	}
	if !ownMe || otherMe != wantOtherMe {
		t.Errorf("%s: own me row seen = %v, another user's me row seen = %v; want true, %v",
			role, ownMe, otherMe, wantOtherMe)
	}
}

// allGatedKinds is the kind of every row seedGateTenant writes.
var allGatedKinds = []string{
	"decision", "evidence", "exception", "sample", "audit_period", "feature_flag",
	"me", "walkthrough", "aggregation_rule", "taxonomy", "mapping_overlay",
}

func gateWindow() (string, string) {
	from := time.Now().Add(-1 * time.Hour).UTC().Format(time.RFC3339)
	to := time.Now().Add(1 * time.Hour).UTC().Format(time.RFC3339)
	return from, to
}

func listRows(t *testing.T, r http.Handler, url string) []gateRow {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status = %d; body = %s", url, rec.Code, rec.Body.String())
	}
	var resp adminauditlog.UnifiedListResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := make([]gateRow, 0, len(resp.Entries))
	for _, e := range resp.Entries {
		out = append(out, gateRow{
			kind:    e.Kind,
			actor:   e.ActorID,
			payload: len(e.PayloadJSON) > 0 && string(e.PayloadJSON) != "null",
		})
	}
	return out
}

// TestPayloadGate_Activity covers /v1/activity/unified for each non-admin
// role: a row whose source the role cannot read comes back with a null
// payload. viewer and control_owner never see feature_flag rows (slice
// 270), so the kind is not required of them.
func TestPayloadGate_Activity(t *testing.T) {
	for _, role := range []authz.Role{authz.RoleAuditor, authz.RoleControlOwner, authz.RoleViewer, authz.RoleGRCEngineer} {
		t.Run(string(role), func(t *testing.T) {
			tenant, caller := uuid.New(), uuid.New()
			seedGateTenant(t, tenant, caller)
			privileged := role == authz.RoleAuditor || role == authz.RoleGRCEngineer
			if privileged {
				seedRole(t, tenant, caller.String(), role)
			}
			from, to := gateWindow()
			r := newGateRouter(t, tenant, caller.String(), role)
			rows := listRows(t, r, fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to))

			want := allGatedKinds
			if !privileged {
				want = nil
				for _, k := range allGatedKinds {
					if k != "feature_flag" {
						want = append(want, k)
					}
				}
			}
			// A non-privileged caller sees only its own me rows (slice 270).
			assertGated(t, role, caller.String(), rows, want, privileged)
		})
	}
}

// TestPayloadGate_UnifiedList covers /v1/admin/audit-log/unified for the
// privileged non-admin roles.
func TestPayloadGate_UnifiedList(t *testing.T) {
	for _, role := range []authz.Role{authz.RoleAuditor, authz.RoleGRCEngineer} {
		t.Run(string(role), func(t *testing.T) {
			tenant, caller := uuid.New(), uuid.New()
			seedGateTenant(t, tenant, caller)
			seedRole(t, tenant, caller.String(), role)
			from, to := gateWindow()
			r := newGateRouter(t, tenant, caller.String(), role)
			rows := listRows(t, r, fmt.Sprintf("/v1/admin/audit-log/unified?from=%s&to=%s", from, to))
			assertGated(t, role, caller.String(), rows, allGatedKinds, true)
		})
	}
}

// TestPayloadGate_Export covers /v1/admin/audit-log/export (JSON) for the
// privileged non-admin roles: a withheld payload is JSON null.
func TestPayloadGate_Export(t *testing.T) {
	for _, role := range []authz.Role{authz.RoleAuditor, authz.RoleGRCEngineer} {
		t.Run(string(role), func(t *testing.T) {
			tenant, caller := uuid.New(), uuid.New()
			seedGateTenant(t, tenant, caller)
			seedRole(t, tenant, caller.String(), role)
			from, to := gateWindow()
			r := newGateRouter(t, tenant, caller.String(), role)
			url := fmt.Sprintf("/v1/admin/audit-log/export?format=json&from=%s&to=%s", from, to)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, url, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; body = %s", rec.Code, rec.Body.String())
			}
			var parsed []map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
				t.Fatalf("decode export: %v; body = %s", err, rec.Body.String())
			}
			rows := make([]gateRow, 0, len(parsed))
			for _, p := range parsed {
				payload, _ := p["payload_json"].(string)
				kind, _ := p["kind"].(string)
				actor, _ := p["actor_id"].(string)
				rows = append(rows, gateRow{kind: kind, actor: actor, payload: payload != ""})
			}
			assertGated(t, role, caller.String(), rows, allGatedKinds, true)
		})
	}
}

// TestPayloadGate_AdminKeepsEveryPayload pins the other side: a caller
// holding the admin role (not the credential flag, so the gate's OPA admin
// probe is what decides) keeps every payload it is shown. The activity feed
// classifies a role-only admin as non-privileged, so feature_flag and other
// users' me rows are not in its view: ten rows.
func TestPayloadGate_AdminKeepsEveryPayload(t *testing.T) {
	tenant, caller := uuid.New(), uuid.New()
	seedGateTenant(t, tenant, caller)
	from, to := gateWindow()
	r := newGateRouter(t, tenant, caller.String(), authz.RoleAdmin)
	rows := listRows(t, r, fmt.Sprintf("/v1/activity/unified?from=%s&to=%s", from, to))
	if len(rows) < len(allGatedKinds)-1 {
		t.Fatalf("admin saw %d rows; want at least %d", len(rows), len(allGatedKinds)-1)
	}
	for _, row := range rows {
		if !row.payload {
			t.Errorf("admin: kind %s (actor %s) payload withheld", row.kind, row.actor)
		}
	}
}
