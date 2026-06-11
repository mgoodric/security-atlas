//go:build integration

// Slice 094 — integration tests for the compliance calendar.
//
// Real Postgres + the assembled platform router so the tests exercise the
// full request path: tenancy middleware, RLS, the sqlc query layer.
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/calendar/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage maps to slice 094's AC-17 / AC-17a / AC-18 / AC-19:
//
//	ISC-94-1  RLS: tenant A's exception does NOT appear in tenant B's calendar
//	ISC-94-2  RLS: tenant A's control-cadence event does NOT appear in tenant B
//	ISC-94-3  cadence math: control with last_evaluated_at = now() - 88d &
//	          freshness_class=quarterly emits due-soon at last+120d
//	ISC-94-4  cadence math: control with last_evaluated_at NULL emits status=overdue
//	ISC-94-5  truncation: 501-event seed yields truncated=true + next_from
//	ISC-94-6  ICS feed: response validates as RFC 5545 (envelope + VEVENTs)
//	ISC-94-7  ICS auth: missing token -> 401; non-calendar-scope token -> 403

package calendar_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	t.Cleanup(func() { pool.Close() })
	return pool
}

// freshTenant returns a new tenant id and registers a cleanup that
// removes every row this slice's tests can create under it.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM control_evaluations WHERE tenant_id = $1`,
			`DELETE FROM exceptions WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
			`DELETE FROM policies WHERE tenant_id = $1`,
			`DELETE FROM vendors WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedFrameworkVersion seeds the minimum catalog rows for an audit_period FK.
func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	fwID := uuid.New()
	fvID := uuid.New()
	slug := "test-fw-" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, $2, $3, 'test-issuer')
	`, fwID, "Test framework", slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version, status)
		VALUES ($1, NULL, $2, '1.0', 'current')
	`, fvID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM audit_periods WHERE framework_version_id = $1`, fvID)
		_, _ = admin.Exec(context.Background(), `DELETE FROM frameworks WHERE id = $1`, fwID)
	})
	return fvID
}

func seedAuditPeriod(t *testing.T, admin *pgxpool.Pool, tenant string, fvID uuid.UUID, end time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO audit_periods (
			id, tenant_id, name, framework_version_id,
			period_start, period_end, status, created_by
		)
		VALUES ($1, $2, 'test audit', $3, $4, $5, 'open', 'tester')
	`, uuid.New(), tenant, fvID, end.Add(-90*24*time.Hour), end); err != nil {
		t.Fatalf("seed audit_period: %v", err)
	}
}

func seedControlWithCadence(t *testing.T, admin *pgxpool.Pool, tenant string, freshnessClass string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, freshness_class,
			lifecycle_state
		)
		VALUES ($1, $2, 'test cadence control', 'AAA', 'manual_periodic',
		        $3, '[]'::jsonb, 'true', $4, 'active')
	`, ctrlID, tenant, "test-bundle-094-"+ctrlID.String(), freshnessClass); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func seedException(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, expiresAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO exceptions (
			id, tenant_id, control_id, scope_cell_predicate,
			justification, requested_by, requested_at, expires_at, status
		)
		VALUES ($1, $2, $3, '{}'::jsonb,
		        'test exception', 'tester', now(), $4, 'active')
	`, uuid.New(), tenant, ctrlID, expiresAt); err != nil {
		t.Fatalf("seed exception: %v", err)
	}
}

// seedVendor inserts a vendor with a last_review_date and review_cadence so
// the calendar's vendor-review branch (slice 675) projects a next-review
// event at last_review_date + cadence. Returns the vendor id.
func seedVendor(t *testing.T, admin *pgxpool.Pool, tenant string, name string, lastReview time.Time, cadence string) uuid.UUID {
	t.Helper()
	vendorID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO vendors (
			id, tenant_id, name, criticality, review_cadence, last_review_date
		)
		VALUES ($1, $2, $3, 'high', $4, $5)
	`, vendorID, tenant, name, cadence, lastReview); err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	return vendorID
}

func seedEvaluation(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, evaluatedAt time.Time) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO control_evaluations (
			id, tenant_id, control_id, eval_run_id, evaluated_at,
			result, freshness_status, evidence_count_in_window, trigger
		)
		VALUES ($1, $2, $3, $4, $5, 'pass', 'fresh', 1, 'manual')
	`, uuid.New(), tenant, ctrlID, uuid.New(), evaluatedAt); err != nil {
		t.Fatalf("seed control_evaluation: %v", err)
	}
}

type testEnv struct {
	server *httptest.Server
	bearer string
	// calendarToken is a credstore-issued opaque bearer used by
	// `GET /v1/calendar.ics?token=...`. The calendar.ics handler
	// authenticates via `h.creds.Authenticate` (slice 091); slice
	// 197 left that path on credstore intentionally because calendar
	// clients (Google / Apple / Outlook) cannot exchange JWTs. The
	// scope-mismatch integration test asserts a wrong-scope credstore
	// token returns 403.
	calendarToken string
	tenant        string
}

func testServer(t *testing.T, app *pgxpool.Pool, tenant string) testEnv {
	t.Helper()
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	// Slice 197: JWT bearer via slice 190 path (owner roles) for the
	// Authorization-header gated routes (/v1/calendar, /v1/calendar/subscription).
	bearer := srv.IssueTestJWT(t, testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"}))
	// Slice 091 path: a separate credstore opaque bearer drives the
	// `?token=` URL parameter on /v1/calendar.ics. The owner credential
	// has no calendar scope — the scope-mismatch test relies on that.
	_, calendarToken, err := srv.IssueBootstrapOwnerCredential(tenant, []string{"control_owner"})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential (calendar token): %v", err)
	}
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests returned nil — DB pool not attached")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)
	return testEnv{server: ts, bearer: bearer, calendarToken: calendarToken, tenant: tenant}
}

func get(t *testing.T, env testEnv, path string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, env.server.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+env.bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	_ = resp.Body.Close()
	return resp, body
}

func decodeJSON(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode JSON: %v (body=%q)", err, string(body))
	}
	return m
}

// ----- ISC-94-1 + 94-2: RLS isolation across tenants -----

func TestCalendar_RLSIsolatesExceptionsAcrossTenants(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A has one control + one exception expiring in 10 days.
	now := time.Now().UTC()
	ctrlA := seedControlWithCadence(t, admin, tenantA, "quarterly")
	seedException(t, admin, tenantA, ctrlA, now.Add(10*24*time.Hour))

	// Tenant B has no events.
	envB := testServer(t, app, tenantB)
	resp, body := get(t, envB, "/v1/calendar?types=exception")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) != 0 {
		t.Errorf("tenant B sees %d exceptions; expected 0 (cross-tenant leak)", len(events))
	}

	// Tenant A sees its own.
	envA := testServer(t, app, tenantA)
	respA, bodyA := get(t, envA, "/v1/calendar?types=exception")
	if respA.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET (A): status=%d body=%s", respA.StatusCode, string(bodyA))
	}
	gotA := decodeJSON(t, bodyA)
	eventsA := gotA["events"].([]any)
	if len(eventsA) != 1 {
		t.Errorf("tenant A sees %d exceptions; expected 1", len(eventsA))
	}
}

func TestCalendar_RLSIsolatesControlCadenceAcrossTenants(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	// Tenant A: control with cadence and a recent evaluation that puts
	// next_due_at firmly inside the default 90-day window.
	now := time.Now().UTC()
	ctrlA := seedControlWithCadence(t, admin, tenantA, "quarterly")     // 120d cadence
	seedEvaluation(t, admin, tenantA, ctrlA, now.Add(-30*24*time.Hour)) // due in 90d

	envB := testServer(t, app, tenantB)
	resp, body := get(t, envB, "/v1/calendar?types=control")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) != 0 {
		t.Errorf("tenant B sees %d control events; expected 0 (cross-tenant leak)", len(events))
	}
}

// ----- ISC-94-3 + 94-4: cadence math -----

func TestCalendar_ControlCadenceMathDueSoon(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	now := time.Now().UTC()

	ctrl := seedControlWithCadence(t, admin, tenant, "quarterly") // 120d cadence
	// last_evaluated_at = 110 days ago -> next_due_at = 10 days from now -> due-soon
	seedEvaluation(t, admin, tenant, ctrl, now.Add(-110*24*time.Hour))

	env := testServer(t, app, tenant)
	resp, body := get(t, env, "/v1/calendar?types=control")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("expected at least one control event, got 0; body=%s", string(body))
	}
	first := events[0].(map[string]any)
	if first["status"] != "due-soon" {
		t.Errorf("status=%v want=due-soon", first["status"])
	}
	if first["type"] != "control" {
		t.Errorf("type=%v want=control", first["type"])
	}
	if first["cadence"] != "quarterly" {
		t.Errorf("cadence=%v want=quarterly", first["cadence"])
	}
}

func TestCalendar_ControlCadenceMathOverdueWhenNeverEvaluated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	// Control with cadence but NO evaluations -> status=overdue at now().
	seedControlWithCadence(t, admin, tenant, "quarterly")

	env := testServer(t, app, tenant)
	resp, body := get(t, env, "/v1/calendar?types=control")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("expected an overdue control event for never-evaluated control; got 0; body=%s", string(body))
	}
	first := events[0].(map[string]any)
	if first["status"] != "overdue" {
		t.Errorf("status=%v want=overdue (never-evaluated control)", first["status"])
	}
}

// ----- Slice 675: calendar agenda sources vendor reviews (AC-1 / AC-5) -----

// TestCalendar_SurfacesVendorReviews is the slice-675 regression guard: a
// vendor with a last_review_date + cadence whose next review falls in the
// default window appears in the agenda with type=vendor. Before slice 675
// the calendar's UNION had no vendor branch, so this returned 0 events.
func TestCalendar_SurfacesVendorReviews(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	now := time.Now().UTC()

	// last_review_date 30 days ago + quarterly cadence => next review in ~62
	// days, inside the default 90-day forward window.
	seedVendor(t, admin, tenant, "Acme Cloud", now.Add(-30*24*time.Hour), "quarterly")

	env := testServer(t, app, tenant)
	resp, body := get(t, env, "/v1/calendar?types=vendor")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("expected 1 vendor-review event, got %d; body=%s", len(events), string(body))
	}
	first := events[0].(map[string]any)
	if first["type"] != "vendor" {
		t.Errorf("type=%v want=vendor", first["type"])
	}
	if !strings.Contains(first["title"].(string), "Acme Cloud") {
		t.Errorf("title=%v want to contain vendor name", first["title"])
	}
	if first["related_entity_kind"] != "vendor" {
		t.Errorf("related_entity_kind=%v want=vendor", first["related_entity_kind"])
	}
}

// TestCalendar_AgendaSourcesAllDashboardTypes is the AC-5 acceptance test: a
// tenant with an audit period + a vendor review + an exception shows ALL
// three in the agenda (the demo-audit bug was: exceptions only).
func TestCalendar_AgendaSourcesAllDashboardTypes(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin)
	now := time.Now().UTC()

	// One of each: audit period closing in 30d, vendor review due in ~62d,
	// exception expiring in 10d. All inside the default 90-day window.
	seedAuditPeriod(t, admin, tenant, fvID, now.Add(30*24*time.Hour))
	seedVendor(t, admin, tenant, "Globex SaaS", now.Add(-30*24*time.Hour), "quarterly")
	ctrl := seedControlWithCadence(t, admin, tenant, "annual")
	seedException(t, admin, tenant, ctrl, now.Add(10*24*time.Hour))

	env := testServer(t, app, tenant)
	// No types filter => all sources.
	resp, body := get(t, env, "/v1/calendar?types=audit,vendor,exception")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)

	seen := map[string]bool{}
	for _, e := range events {
		ev := e.(map[string]any)
		seen[ev["type"].(string)] = true
	}
	for _, want := range []string{"audit", "vendor", "exception"} {
		if !seen[want] {
			t.Errorf("agenda missing event type %q; types seen=%v body=%s", want, seen, string(body))
		}
	}
}

// ----- ISC-94-5: truncation flag fires at the 500-event threshold -----

func TestCalendar_TruncationFiresAt500Events(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	now := time.Now().UTC()

	// Seed 501 controls with cadence that ALL fall in the default 90-day
	// window. Use freshness_class=monthly (90d cadence) and
	// last_evaluated_at = 1 to 501 hours ago so each falls at a unique
	// time on the calendar.
	for i := 0; i < 501; i++ {
		ctrl := seedControlWithCadence(t, admin, tenant, "monthly")
		// last_evaluated_at i hours ago -> due_at = 90d - i hours from now
		seedEvaluation(t, admin, tenant, ctrl, now.Add(-time.Duration(i+1)*time.Hour))
	}

	env := testServer(t, app, tenant)
	// Generous window to capture all 501.
	resp, body := get(t, env, "/v1/calendar?from="+now.Add(24*time.Hour).Format("2006-01-02")+"&to="+now.Add(120*24*time.Hour).Format("2006-01-02"))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	got := decodeJSON(t, body)
	events := got["events"].([]any)
	if len(events) != 500 {
		t.Errorf("events length=%d want=500 (truncated)", len(events))
	}
	if got["truncated"] != true {
		t.Errorf("truncated=%v want=true", got["truncated"])
	}
	if _, ok := got["next_from"]; !ok {
		t.Error("expected next_from cursor in truncated response")
	}
}

// ----- ISC-94-6: ICS feed validates as RFC 5545 -----

func TestCalendarICS_FeedShapeValidates(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))

	tenant := freshTenant(t, admin)
	fvID := seedFrameworkVersion(t, admin)
	now := time.Now().UTC()
	seedAuditPeriod(t, admin, tenant, fvID, now.Add(30*24*time.Hour))

	env := testServer(t, app, tenant)

	// First mint a calendar token via POST /v1/calendar/subscription.
	subReq, _ := http.NewRequest(http.MethodPost, env.server.URL+"/v1/calendar/subscription", nil)
	subReq.Header.Set("Authorization", "Bearer "+env.bearer)
	subResp, err := http.DefaultClient.Do(subReq)
	if err != nil {
		t.Fatalf("subscribe POST: %v", err)
	}
	if subResp.StatusCode != http.StatusCreated {
		t.Fatalf("subscribe POST status=%d", subResp.StatusCode)
	}
	var subBody map[string]any
	_ = json.NewDecoder(subResp.Body).Decode(&subBody)
	_ = subResp.Body.Close()

	urlStr, ok := subBody["url"].(string)
	if !ok || urlStr == "" {
		t.Fatalf("subscribe response missing url; got=%+v", subBody)
	}
	// Extract just the token.
	if !strings.Contains(urlStr, "?token=") {
		t.Fatalf("unexpected url shape: %q", urlStr)
	}
	token := urlStr[strings.Index(urlStr, "?token=")+len("?token="):]

	icsURL := fmt.Sprintf("%s/v1/calendar.ics?token=%s", env.server.URL, token)
	icsResp, err := http.Get(icsURL)
	if err != nil {
		t.Fatalf("ICS GET: %v", err)
	}
	if icsResp.StatusCode != http.StatusOK {
		t.Fatalf("ICS status=%d", icsResp.StatusCode)
	}
	if ct := icsResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/calendar") {
		t.Errorf("Content-Type=%q want text/calendar", ct)
	}
	if cc := icsResp.Header.Get("Cache-Control"); !strings.Contains(cc, "private") || !strings.Contains(cc, "max-age=300") {
		t.Errorf("Cache-Control=%q want private+max-age=300", cc)
	}

	body := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := icsResp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	_ = icsResp.Body.Close()

	bodyStr := string(body)
	// Minimal RFC 5545 envelope checks.
	for _, want := range []string{
		"BEGIN:VCALENDAR\r\n",
		"VERSION:2.0\r\n",
		"PRODID:",
		"BEGIN:VEVENT\r\n",
		"UID:audit-",
		"DTSTART:",
		"DTSTAMP:",
		"END:VEVENT\r\n",
		"END:VCALENDAR\r\n",
	} {
		if !strings.Contains(bodyStr, want) {
			t.Errorf("ICS feed missing required token %q in body:\n%s", want, bodyStr)
		}
	}
	// No blank lines mid-feed (would break parsers).
	if strings.Contains(bodyStr, "\r\n\r\n") {
		t.Errorf("ICS feed contains blank line:\n%s", bodyStr)
	}
}

// ----- ISC-94-7: ICS auth rejects missing token + non-calendar-scope tokens -----

func TestCalendarICS_RejectsMissingToken(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	env := testServer(t, app, tenant)
	resp, err := http.Get(env.server.URL + "/v1/calendar.ics")
	if err != nil {
		t.Fatalf("ICS GET: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status=%d want=401 (no token)", resp.StatusCode)
	}
}

func TestCalendarICS_RejectsNonCalendarScopeToken(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)

	env := testServer(t, app, tenant)
	// env.calendarToken is an owner credstore bearer — NOT scoped for
	// calendar. (Slice 197: the JWT-bearer env.bearer cannot be used
	// here because /v1/calendar.ics authenticates via credstore lookup.)
	resp, err := http.Get(env.server.URL + "/v1/calendar.ics?token=" + env.calendarToken)
	if err != nil {
		t.Fatalf("ICS GET: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("status=%d want=403 (token wrong scope)", resp.StatusCode)
	}
}
