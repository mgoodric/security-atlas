//go:build integration

// #2118 — an admin assigns and removes the auditors of an audit period over
// HTTP, through the production middleware chain (JWT, tenancy, OPA authz with
// the auditor attrs resolver). The tests pin:
//
//   - assign and remove each write one audit_period_audit_log row;
//   - a non-admin (grc_engineer, auditor, viewer) is refused;
//   - only an active auditor of the caller's tenant can be assigned;
//   - the grant adds that one period and nothing else, and a removal takes
//     the period away on the auditor's next request.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/audit/auditor"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

// seedPeriodUser inserts an active user in tenant holding roles, removing
// it when the test ends.
func seedPeriodUser(t *testing.T, admin *pgxpool.Pool, tenant, status string, roles ...string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	if _, err := admin.Exec(ctx,
		`INSERT INTO users (id, tenant_id, email, display_name, status) VALUES ($1, $2, $3, $4, $5)`,
		id, tenant, "user-2118-"+id.String()+"@example.invalid", "Auditor 2118", status); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	for _, role := range roles {
		if _, err := admin.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2118')`,
			tenant, id.String(), role); err != nil {
			t.Fatalf("seed user_role: %v", err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, id.String())
		_, _ = admin.Exec(ctx, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

type periodAuditorsCall struct {
	t      *testing.T
	url    string
	bearer string
}

func (c periodAuditorsCall) do(method, path string, body any) (int, map[string]any) {
	c.t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			c.t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, c.url+path, rdr)
	if err != nil {
		c.t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.bearer)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	out := map[string]any{}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// periodLog returns the actions logged for the period, oldest first.
func periodLog(t *testing.T, admin *pgxpool.Pool, periodID uuid.UUID) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(),
		`SELECT action || ':' || actor || ':' || (detail->>'user_id')
		   FROM audit_period_audit_log
		  WHERE audit_period_id = $1 AND action LIKE 'auditor_%'
		  ORDER BY occurred_at, id`, periodID)
	if err != nil {
		t.Fatalf("read period log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatalf("scan period log: %v", err)
		}
		out = append(out, s)
	}
	return out
}

func mePeriodIDs(t *testing.T, c periodAuditorsCall) []string {
	t.Helper()
	status, body := c.do(http.MethodGet, "/v1/me/audit-periods", nil)
	if status != http.StatusOK {
		t.Fatalf("GET /v1/me/audit-periods: %d %v", status, body)
	}
	var ids []string
	items, _ := body["audit_periods"].([]any)
	for _, it := range items {
		m, _ := it.(map[string]any)
		id, _ := m["audit_period_id"].(string)
		ids = append(ids, id)
	}
	return ids
}

func TestPeriodAuditors_AdminAssignsAndRemoves(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := dbtest.SeedTenant(t, admin)
	otherTenant := dbtest.SeedTenant(t, admin)
	tid := uuid.MustParse(tenant)

	period := seedNotePeriod(t, admin, tenant)
	sibling := seedNotePeriod(t, admin, tenant)
	foreign := seedNotePeriod(t, admin, otherTenant)
	t.Cleanup(func() {
		// Runs before seedNotePeriod's cleanup (LIFO). The log is
		// append-only under RLS; the migrate role bypasses it.
		for _, tn := range []string{tenant, otherTenant} {
			_, _ = admin.Exec(context.Background(), `DELETE FROM audit_period_audit_log WHERE tenant_id = $1`, tn)
		}
	})

	auditorUser := seedPeriodUser(t, admin, tenant, "active", "auditor")
	viewerUser := seedPeriodUser(t, admin, tenant, "active", "viewer")
	disabledAuditor := seedPeriodUser(t, admin, tenant, "disabled", "auditor")
	foreignAuditor := seedPeriodUser(t, admin, otherTenant, "active", "auditor")
	adminUser := uuid.New()

	claimsFor := func(user uuid.UUID, role string) (string, []authz.Role) {
		return "user:" + user.String(), []authz.Role{authz.Role(role)}
	}
	adminSub, adminRoles := claimsFor(adminUser, "admin")
	auditorSub, auditorRoles := claimsFor(auditorUser, "auditor")
	grcUser := uuid.New()
	grcSub, grcRoles := claimsFor(grcUser, "grc_engineer")
	viewerSub, viewerRoles := claimsFor(viewerUser, "viewer")

	engine, err := authz.NewEngine(context.Background(), noteSubjectRoles{
		adminSub:   adminRoles,
		auditorSub: auditorRoles,
		grcSub:     grcRoles,
		viewerSub:  viewerRoles,
	})
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	engine = engine.WithAttrsResolver(auditor.NewDBAttrsResolver(app))

	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	bearer := func(sub, role string) string {
		c := testjwt.OwnerFor(tid, []string{role})
		c.Subject = sub
		return srv.IssueTestJWT(t, c)
	}
	// Mint before building the handler: the first IssueTestJWT wires the
	// signer, and the JWT middleware mounts only when it is set.
	adminBearer := bearer(adminSub, "admin")
	auditorBearer := bearer(auditorSub, "auditor")
	grcBearer := bearer(grcSub, "grc_engineer")
	viewerBearer := bearer(viewerSub, "viewer")
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	asAdmin := periodAuditorsCall{t, ts.URL, adminBearer}
	asAuditor := periodAuditorsCall{t, ts.URL, auditorBearer}
	asGRC := periodAuditorsCall{t, ts.URL, grcBearer}
	asViewer := periodAuditorsCall{t, ts.URL, viewerBearer}

	base := "/v1/audit-periods/" + period.String() + "/auditors"
	assign := map[string]string{"user_id": auditorUser.String()}

	// Before any grant the auditor reaches no period.
	if got := mePeriodIDs(t, asAuditor); len(got) != 0 {
		t.Fatalf("auditor periods before assign = %v; want none", got)
	}

	t.Run("non-admins are refused", func(t *testing.T) {
		for name, c := range map[string]periodAuditorsCall{"grc_engineer": asGRC, "auditor": asAuditor, "viewer": asViewer} {
			if s, b := c.do(http.MethodPost, base, assign); s != http.StatusForbidden {
				t.Errorf("%s POST: %d %v; want 403", name, s, b)
			}
			if s, b := c.do(http.MethodGet, base, nil); s != http.StatusForbidden {
				t.Errorf("%s GET: %d %v; want 403", name, s, b)
			}
			if s, b := c.do(http.MethodDelete, base+"/"+auditorUser.String(), nil); s != http.StatusForbidden {
				t.Errorf("%s DELETE: %d %v; want 403", name, s, b)
			}
		}
		if got := periodLog(t, admin, period); len(got) != 0 {
			t.Fatalf("refused calls logged %v", got)
		}
	})

	t.Run("only an active auditor of the tenant can be assigned", func(t *testing.T) {
		for name, user := range map[string]uuid.UUID{
			"viewer":                    viewerUser,
			"disabled auditor":          disabledAuditor,
			"auditor of another tenant": foreignAuditor,
			"unknown user":              uuid.New(),
		} {
			if s, b := asAdmin.do(http.MethodPost, base, map[string]string{"user_id": user.String()}); s != http.StatusBadRequest {
				t.Errorf("%s: %d %v; want 400", name, s, b)
			}
		}
		if s, b := asAdmin.do(http.MethodPost, "/v1/audit-periods/"+foreign.String()+"/auditors", assign); s != http.StatusNotFound {
			t.Errorf("period of another tenant: %d %v; want 404", s, b)
		}
		if got := periodLog(t, admin, period); len(got) != 0 {
			t.Fatalf("rejected assigns logged %v", got)
		}
	})

	candidateIDs := func() map[string]bool {
		t.Helper()
		s, b := asAdmin.do(http.MethodGet, base, nil)
		if s != http.StatusOK {
			t.Fatalf("list: %d %v", s, b)
		}
		out := map[string]bool{}
		cs, _ := b["candidates"].([]any)
		for _, c := range cs {
			m, _ := c.(map[string]any)
			id, _ := m["user_id"].(string)
			out[id] = true
		}
		return out
	}
	t.Run("the picker offers only the tenant's active auditors", func(t *testing.T) {
		got := candidateIDs()
		if !got[auditorUser.String()] {
			t.Fatalf("candidates %v miss the tenant auditor", got)
		}
		for name, user := range map[string]uuid.UUID{
			"viewer": viewerUser, "disabled auditor": disabledAuditor, "auditor of another tenant": foreignAuditor,
		} {
			if got[user.String()] {
				t.Errorf("candidates offer the %s", name)
			}
		}
	})

	// Assign: 201, then 200 on a repeat, with one log row.
	if s, b := asAdmin.do(http.MethodPost, base, assign); s != http.StatusCreated {
		t.Fatalf("assign: %d %v; want 201", s, b)
	}
	if s, b := asAdmin.do(http.MethodPost, base, assign); s != http.StatusOK {
		t.Fatalf("repeat assign: %d %v; want 200", s, b)
	}
	logged := periodLog(t, admin, period)
	if len(logged) != 1 || logged[0] != "auditor_assigned:"+adminSub+":"+auditorUser.String() {
		t.Fatalf("log after assign = %v; want one auditor_assigned by %s", logged, adminSub)
	}

	s, list := asAdmin.do(http.MethodGet, base, nil)
	if s != http.StatusOK || list["count"] != float64(1) {
		t.Fatalf("list: %d %v; want one auditor", s, list)
	}
	first, _ := list["auditors"].([]any)[0].(map[string]any)
	if first["user_id"] != auditorUser.String() || first["display_name"] != "Auditor 2118" {
		t.Fatalf("listed auditor = %v", first)
	}
	if candidateIDs()[auditorUser.String()] {
		t.Fatalf("an assigned auditor is still offered as a candidate")
	}

	t.Run("the grant adds this period and nothing beyond it", func(t *testing.T) {
		if got := mePeriodIDs(t, asAuditor); len(got) != 1 || got[0] != period.String() {
			t.Fatalf("auditor periods = %v; want only %s", got, period)
		}
		if s, b := postAuditNote(t, ts.URL, asAuditor.bearer, period); s != http.StatusCreated {
			t.Fatalf("note in assigned period: %d %s; want 201", s, b)
		}
		if s, b := postAuditNote(t, ts.URL, asAuditor.bearer, sibling); s != http.StatusForbidden {
			t.Fatalf("note in a sibling period: %d %s; want 403", s, b)
		}
		// The assignment is not an admin grant: the auditor still cannot
		// read or change the period's auditors.
		if s, b := asAuditor.do(http.MethodGet, base, nil); s != http.StatusForbidden {
			t.Fatalf("auditor lists auditors: %d %v; want 403", s, b)
		}
	})

	// Remove: 204, then 404 on a repeat, with one more log row; the auditor
	// loses the period on the next request.
	if s, b := asAdmin.do(http.MethodDelete, base+"/"+auditorUser.String(), nil); s != http.StatusNoContent {
		t.Fatalf("remove: %d %v; want 204", s, b)
	}
	if s, b := asAdmin.do(http.MethodDelete, base+"/"+auditorUser.String(), nil); s != http.StatusNotFound {
		t.Fatalf("repeat remove: %d %v; want 404", s, b)
	}
	logged = periodLog(t, admin, period)
	if len(logged) != 2 || logged[1] != "auditor_removed:"+adminSub+":"+auditorUser.String() {
		t.Fatalf("log after remove = %v; want auditor_assigned then auditor_removed", logged)
	}
	if got := mePeriodIDs(t, asAuditor); len(got) != 0 {
		t.Fatalf("auditor periods after remove = %v; want none", got)
	}
	if s, b := postAuditNote(t, ts.URL, asAuditor.bearer, period); s != http.StatusForbidden {
		t.Fatalf("note after removal: %d %s; want 403", s, b)
	}
}
