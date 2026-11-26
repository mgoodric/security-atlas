//go:build integration

// #2234 — the export's upcoming panel narrows its calendar read with the
// same OPA type filter as GET /v1/calendar, so for every role the export
// and the calendar's "Coming up" window list the same events.

package dashboardexport_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/api/dashboard"
	"github.com/mgoodric/security-atlas/internal/api/dashboardexport"
	"github.com/mgoodric/security-atlas/internal/api/tenancymw"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/risk"
)

type fixedRoles []authz.Role

func (f fixedRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return f, nil
}

// seedEveryCalendarType seeds one event of each gated type (risk review,
// action plan, vendor review, exception, offboarding) and of the ungated
// policy review and policy acknowledgement, all due inside the next 14 days.
func seedEveryCalendarType(t *testing.T, tenant string) {
	t.Helper()
	admin := dbtest.NewMigratePool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := 24 * time.Hour
	userID, policyID, controlID := uuid.New(), uuid.New(), uuid.New()
	for _, q := range []struct {
		what string
		sql  string
		args []any
	}{
		{"user", `INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, 'Export access', 'active')`,
			[]any{userID, tenant, "export-access-" + userID.String()[:8] + "@test.example"}},
		{"policy", `INSERT INTO policies (id, tenant_id, title, version, status, effective_date, body_md, owner_role, approver_role, created_by, published_at, published_by, next_review_at)
			VALUES ($1, $2, 'Export access policy', '1.0.0', 'published', now()::date, '# body', 'security_lead', 'ciso', 'tester', now(), 'tester', $3)`,
			[]any{policyID, tenant, now.Add(4 * day)}},
		{"ack", `INSERT INTO policy_acknowledgments (id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token)
			VALUES ($1, $2, $3, $3, $4, $5, $6)`,
			[]any{uuid.New(), tenant, policyID, userID, now.Add(-355 * day), "export-access-" + userID.String()}},
		{"risk", `INSERT INTO risks (id, tenant_id, title, category_id, treatment, treatment_owner, inherent_score, residual_score, review_due_at)
			VALUES ($1, $2, 'Export access risk', risk_category_id($2, 'operational'), 'mitigate'::risk_treatment, 'owner',
			        '{"likelihood": 3, "impact": 4}'::jsonb, '{"likelihood": 2, "impact": 3}'::jsonb, $3)`,
			[]any{uuid.New(), tenant, now.Add(5 * day)}},
		{"action plan", `INSERT INTO action_plans (id, tenant_id, title, owner_id, due_date, status) VALUES ($1, $2, 'Export access plan', $3, $4::date, 'draft')`,
			[]any{uuid.New(), tenant, userID, now.Add(6 * day).Format("2006-01-02")}},
		{"vendor", `INSERT INTO vendors (id, tenant_id, name, criticality, review_cadence, last_review_date) VALUES ($1, $2, 'Export access vendor', 'high', 'monthly', $3)`,
			[]any{uuid.New(), tenant, now.Add(-25 * day)}},
		{"control", `INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id, evidence_queries, applicability_expr, lifecycle_state)
			VALUES ($1, $2, 'Export access control', 'AAA', 'automated', $3, '[]'::jsonb, 'true', 'active')`,
			[]any{controlID, tenant, "test-bundle-2234-" + controlID.String()}},
		{"exception", `INSERT INTO exceptions (id, tenant_id, control_id, scope_cell_predicate, justification, requested_by, requested_at, expires_at, activated_at, status)
			VALUES ($1, $2, $3, '{}'::jsonb, 'export access', 'tester', now(), $4, now(), 'active')`,
			[]any{uuid.New(), tenant, controlID, now.Add(8 * day)}},
		{"offboarding", `INSERT INTO personnel_security_checklists (id, tenant_id, workflow_kind, source, person_external_id, person_work_email, due_at, status)
			VALUES ($1, $2, 'offboarding', 'manual', 'emp-2234', 'departing-2234@test.example', $3, 'open')`,
			[]any{uuid.New(), tenant, now.Add(3 * day)}},
	} {
		if _, err := admin.Exec(ctx, q.sql, q.args...); err != nil {
			t.Fatalf("seed %s: %v", q.what, err)
		}
	}
}

var upcomingAccessTables = []string{
	"personnel_security_checklists", "exceptions", "controls", "vendors",
	"action_plans", "risks", "policy_acknowledgments", "policies", "users",
	"me_audit_log",
}

// as serves path through a router whose caller is cred, with the tenant
// lifted onto the context as in production.
func as(t *testing.T, cred credstore.Credential, path, query string, h http.HandlerFunc) []byte {
	t.Helper()
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(authctx.WithCredential(req.Context(), cred)))
		})
	})
	r.Use(tenancymw.Middleware)
	r.Get(path, h)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path+"?"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.Bytes()
}

// comingUpQuery is the dashboard's "Coming up" window on the calendar.
func comingUpQuery() string {
	today := time.Now().UTC()
	return "from=" + today.Format("2006-01-02") + "&to=" + today.AddDate(0, 0, 13).Format("2006-01-02")
}

// calendarComingUp lists the calendar's "Coming up" events for cred as
// sorted "type related_entity_id" keys.
func calendarComingUp(t *testing.T, app *pgxpool.Pool, engine *authz.Engine, cred credstore.Credential) []string {
	t.Helper()
	h := calendar.New(calendar.NewStore(app), nil).WithAuthz(engine)
	var body struct {
		Events []struct {
			Type            string `json:"type"`
			RelatedEntityID string `json:"related_entity_id"`
		} `json:"events"`
	}
	if err := json.Unmarshal(as(t, cred, "/v1/calendar", comingUpQuery(), h.ListEvents), &body); err != nil {
		t.Fatalf("decode calendar: %v", err)
	}
	out := make([]string, 0, len(body.Events))
	for _, ev := range body.Events {
		out = append(out, ev.Type+" "+ev.RelatedEntityID)
	}
	sort.Strings(out)
	return out
}

func upcomingKeys(rows []dashboardexport.UpcomingPanelRow) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Category+" "+row.ResourceID)
	}
	sort.Strings(out)
	return out
}

func liveSource(app *pgxpool.Pool) *dashboardexport.LivePanelSource {
	return dashboardexport.NewLivePanelSource(
		dashboard.NewStore(app), risk.NewStore(app), freshness.NewStore(app), drift.NewStore(app),
		calendar.NewStore(app),
	)
}

func newEngine(t *testing.T, role authz.Role) *authz.Engine {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), fixedRoles{role})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return engine
}

func sameKeys(t *testing.T, calendarKeys, exportKeys []string) {
	t.Helper()
	if len(calendarKeys) == 0 {
		t.Fatal("the calendar lists nothing: the fixture did not land in the window")
	}
	if strings.Join(calendarKeys, "\n") != strings.Join(exportKeys, "\n") {
		t.Fatalf("calendar and export differ\ncalendar: %v\nexport:   %v", calendarKeys, exportKeys)
	}
}

// TestUpcomingPanel_MatchesCalendarPerRole: for each role, the upcoming
// panel read with UpcomingTypesFor (what the export handler passes) lists
// exactly the calendar's "Coming up" events for the same caller. The
// caller is a person with an owner role and no admin or approver flag, so
// the role under test is what OPA sees: a control owner's panel carries no
// gated event, an admin's carries every type.
func TestUpcomingPanel_MatchesCalendarPerRole(t *testing.T) {
	tenant := dbtest.SeedTenant(t, dbtest.NewMigratePool(t), upcomingAccessTables...)
	seedEveryCalendarType(t, tenant)
	app := dbtest.NewAppPool(t)
	cred := credstore.Credential{
		ID: "export-access-test", TenantID: tenant,
		UserID: "33333333-3333-3333-3333-333333333333", OwnerRoles: []string{"security_lead"},
	}

	for _, role := range []authz.Role{
		authz.RoleAdmin, authz.RoleGRCEngineer, authz.RoleViewer,
		authz.RoleAuditor, authz.RoleControlOwner,
	} {
		t.Run(string(role), func(t *testing.T) {
			engine := newEngine(t, role)
			fromCalendar := calendarComingUp(t, app, engine, cred)

			var upcoming dashboardexport.UpcomingTypes
			as(t, cred, "/probe", "", func(w http.ResponseWriter, r *http.Request) {
				upcoming = dashboardexport.UpcomingTypesFor(r, engine)
			})
			snap, err := liveSource(app).Snapshot(dbtest.WithTenantCtx(t, tenant), upcoming)
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			fromExport := upcomingKeys(snap.Panels.Upcoming)
			sameKeys(t, fromCalendar, fromExport)

			types := map[string]bool{}
			for _, row := range snap.Panels.Upcoming {
				types[row.Category] = true
			}
			switch role {
			case authz.RoleAdmin:
				for _, typ := range []string{"policy", "policy_ack", "risk", "action_plan", "vendor", "exception", "offboarding"} {
					if !types[typ] {
						t.Errorf("admin export lacks %s events: %v", typ, fromExport)
					}
				}
			case authz.RoleControlOwner:
				for _, typ := range []string{"risk", "action_plan", "vendor", "exception", "offboarding"} {
					if types[typ] {
						t.Errorf("control owner export carries %s events: %v", typ, fromExport)
					}
				}
			}
		})
	}
}

// TestExportHandler_UpcomingMatchesCalendar: through the export handler
// itself (OPA engine wired, approver-flagged caller), the JSON export's
// upcoming panel equals the calendar's "Coming up" for the same caller.
func TestExportHandler_UpcomingMatchesCalendar(t *testing.T) {
	tenant := dbtest.SeedTenant(t, dbtest.NewMigratePool(t), upcomingAccessTables...)
	seedEveryCalendarType(t, tenant)
	app := dbtest.NewAppPool(t)
	cred := credstore.Credential{
		ID: "export-access-test", TenantID: tenant,
		UserID: "33333333-3333-3333-3333-333333333333", IsApprover: true,
	}
	engine := newEngine(t, authz.RoleGRCEngineer)

	h := dashboardexport.NewHandler(app, liveSource(app)).WithAuthz(engine)
	var body struct {
		Panels struct {
			Upcoming []dashboardexport.UpcomingPanelRow `json:"upcoming"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(as(t, cred, "/v1/dashboard/export", "format=json", h.ExportDashboard), &body); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	sameKeys(t, calendarComingUp(t, app, engine, cred), upcomingKeys(body.Panels.Upcoming))
}
