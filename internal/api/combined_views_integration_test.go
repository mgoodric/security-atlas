//go:build integration

// #2173 — combined views show only modules the caller can see. Each
// request goes through the production router (JWT, tenancy, the module
// gate, the module access gate, OPA with the live user_roles resolver) on
// real Postgres under RLS, over the attention fixture's tenant, which has a
// policy review, policy acknowledgements, a vendor review, risk reviews and
// action plans on the calendar.
//
// Requires DATABASE_URL_APP (atlas_app, RLS) and DATABASE_URL (migrate role).
package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

type combinedViewsEnv struct {
	*moduleAccessEnv
	fx attention.Fixture
}

// newCombinedViewsEnv builds the production router over the attention
// fixture's tenant A. Its admin is an admin credential that flips switches
// and sets access.
func newCombinedViewsEnv(t *testing.T) *combinedViewsEnv {
	t.Helper()
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, migrate, time.Now().UTC())
	tenant := fx.A.ID
	t.Cleanup(func() {
		ctx := context.Background()
		for _, table := range []string{
			"member_module_access_audit_log", "member_module_access",
			"feature_flag_audit_log", "feature_flags",
			"decision_audit_log", "me_audit_log", "api_keys", "user_roles",
		} {
			_, _ = migrate.Exec(ctx, "DELETE FROM "+table+" WHERE tenant_id = $1", tenant)
		}
	})

	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	f := &moduleGateFixture{admin: srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	f.url = ts.URL
	return &combinedViewsEnv{
		moduleAccessEnv: &moduleAccessEnv{moduleGateFixture: f, srv: srv, tenant: tenant, migrate: migrate, app: app},
		fx:              fx,
	}
}

// everyOptionalBut is every optional module key except the ones named.
func everyOptionalBut(except ...string) []string {
	all := []string{"risk", "third_party_risk", "policies", "questionnaires", "personnel_security", "exceptions", "board_reporting"}
	out := []string{}
	for _, k := range all {
		skip := false
		for _, e := range except {
			skip = skip || k == e
		}
		if !skip {
			out = append(out, k)
		}
	}
	return out
}

func (e *combinedViewsEnv) window() string {
	now := e.fx.Now
	return "from=" + now.AddDate(0, 0, -45).Format("2006-01-02") + "&to=" + now.AddDate(0, 0, 14).Format("2006-01-02")
}

// calendarTypes returns the event types on the caller's calendar.
func (e *combinedViewsEnv) calendarTypes(t *testing.T, bearer string) map[string]int {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/calendar?"+e.window(), bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/calendar: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode calendar: %v", err)
	}
	out := map[string]int{}
	for _, ev := range body.Events {
		out[ev.Type]++
	}
	return out
}

// upcomingTypes returns the event types on the dashboard's Upcoming list:
// the calendar from today for 14 days, the window the web app asks for.
func (e *combinedViewsEnv) upcomingTypes(t *testing.T, bearer string) map[string]int {
	t.Helper()
	today := time.Date(e.fx.Now.Year(), e.fx.Now.Month(), e.fx.Now.Day(), 0, 0, 0, 0, time.UTC)
	q := "from=" + today.Format("2006-01-02") + "&to=" + today.AddDate(0, 0, 13).Format("2006-01-02")
	got := e.do(t, http.MethodGet, "/v1/calendar?"+q, bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/calendar (upcoming): status %d body=%s", got.status, got.body)
	}
	var body struct {
		Events []struct {
			Type string `json:"type"`
		} `json:"events"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode upcoming: %v", err)
	}
	out := map[string]int{}
	for _, ev := range body.Events {
		out[ev.Type]++
	}
	return out
}

// subscribe mints the caller's ICS feed URL.
func (e *combinedViewsEnv) subscribe(t *testing.T, bearer string) string {
	t.Helper()
	got := e.do(t, http.MethodPost, "/v1/calendar/subscription", bearer, map[string]any{})
	if got.status != http.StatusCreated {
		t.Fatalf("POST /v1/calendar/subscription: status %d body=%s", got.status, got.body)
	}
	var body struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode subscription: %v", err)
	}
	return body.URL
}

// icsTypes fetches an ICS feed and returns its event categories.
func (e *combinedViewsEnv) icsTypes(t *testing.T, feed string) map[string]int {
	t.Helper()
	resp, err := http.Get(e.url + feed + "&" + e.window())
	if err != nil {
		t.Fatalf("GET ics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET ics: status %d body=%s", resp.StatusCode, raw)
	}
	out := map[string]int{}
	for _, line := range strings.Split(string(raw), "\r\n") {
		if c, ok := strings.CutPrefix(line, "CATEGORIES:"); ok {
			out[c]++
		}
	}
	return out
}

// activityTargets returns the target types of the caller's Activity rows,
// read telemetry included so authorization decisions are listed.
func (e *combinedViewsEnv) activityTargets(t *testing.T, bearer string) map[string]int {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/activity/unified?include_reads=true&from="+e.fx.Now.Add(-time.Hour).Format(time.RFC3339)+"&to="+time.Now().UTC().Add(time.Hour).Format(time.RFC3339), bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/activity/unified: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Entries []struct {
			Kind       string `json:"kind"`
			TargetType string `json:"target_type"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode activity: %v body=%s", err, got.body)
	}
	out := map[string]int{}
	for _, en := range body.Entries {
		out[en.Kind+"/"+en.TargetType]++
	}
	return out
}

// searchTypes returns the result types a search for the fixture's policy
// title prefix returns.
func (e *combinedViewsEnv) searchTypes(t *testing.T, bearer string) map[string]int {
	t.Helper()
	q := strings.TrimSpace(strings.ReplaceAll(e.fx.A.PolicyTitlePrefix, ":", ""))
	got := e.do(t, http.MethodGet, "/v1/search?q="+strings.ReplaceAll(q, " ", "+"), bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/search: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Hits []struct {
			Type string `json:"type"`
		} `json:"hits"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode search: %v", err)
	}
	out := map[string]int{}
	for _, h := range body.Hits {
		out[h.Type]++
	}
	return out
}

// exportPanels returns the dashboard export's upcoming categories and its
// risks panel size.
func (e *combinedViewsEnv) exportPanels(t *testing.T, bearer string) (map[string]int, int) {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/dashboard/export?format=json", bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/dashboard/export: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Panels struct {
			Risks    []json.RawMessage `json:"risks"`
			Upcoming []struct {
				Category string `json:"category"`
			} `json:"upcoming"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	out := map[string]int{}
	for _, u := range body.Panels.Upcoming {
		out[u.Category]++
	}
	return out, len(body.Panels.Risks)
}

// With Third-party risk switched off, Upcoming, the Calendar and the ICS
// feed carry no vendor review; switched back on, the calendar does again.
func TestCombinedViews_ThirdPartyRiskOffHidesVendorReviews(t *testing.T) {
	e := newCombinedViewsEnv(t)
	_, engineer := e.member(t, "grc_engineer")
	feed := e.subscribe(t, e.admin)

	if got := e.calendarTypes(t, engineer)["vendor"]; got == 0 {
		t.Fatalf("switched on: calendar has no vendor review; the fixture should put one there")
	}

	e.setSwitch(t, "vendor.enabled", false)
	for name, types := range map[string]map[string]int{
		"calendar (grc_engineer)": e.calendarTypes(t, engineer),
		"calendar (admin)":        e.calendarTypes(t, e.admin),
		"upcoming (admin)":        e.upcomingTypes(t, e.admin),
		"ics (admin)":             e.icsTypes(t, feed),
	} {
		if types["vendor"] != 0 {
			t.Errorf("switched off: %s has %d vendor reviews; want none", name, types["vendor"])
		}
	}
	if up, _ := e.exportPanels(t, e.admin); up["vendor"] != 0 {
		t.Errorf("switched off: export upcoming has %d vendor reviews; want none", up["vendor"])
	}

	e.setSwitch(t, "vendor.enabled", true)
	if got := e.calendarTypes(t, engineer)["vendor"]; got == 0 {
		t.Errorf("switched back on: calendar has no vendor review")
	}
}

// A member without Policies access sees no policy items in Upcoming, the
// Calendar, their own ICS feed, Activity, search or the dashboard export.
// The ICS feed follows the subscriber's access at fetch time: narrowing
// after subscribing takes effect without re-subscribing.
func TestCombinedViews_NoPoliciesAccessHidesPolicyItems(t *testing.T) {
	e := newCombinedViewsEnv(t)
	member, engineer := e.member(t, "grc_engineer")
	feed := e.subscribe(t, engineer)
	// An authorization decision on policies, for Activity.
	if got := e.do(t, http.MethodGet, "/v1/policies", e.admin, nil); got.status != http.StatusOK {
		t.Fatalf("admin GET /v1/policies: status %d", got.status)
	}

	// The feed's token now belongs to the member, but it is still no
	// general bearer: the API authenticates JWTs only.
	token := feed[strings.Index(feed, "?token=")+len("?token="):]
	if got := e.do(t, http.MethodGet, "/v1/policies", token, nil); got.status != http.StatusUnauthorized {
		t.Fatalf("ICS token as a bearer: GET /v1/policies status %d; want 401", got.status)
	}

	// On "all", the member sees policy items everywhere.
	before := e.icsTypes(t, feed)
	if before["policy"]+before["policy_ack"] == 0 {
		t.Fatalf("all modules: ICS feed has no policy items; the fixture should put some there: %v", before)
	}
	if got := e.searchTypes(t, engineer)["policies"]; got == 0 {
		t.Fatalf("all modules: search finds no policies")
	}
	if got := e.activityTargets(t, engineer)["decision/policies"]; got == 0 {
		t.Fatalf("all modules: Activity has no decision on policies")
	}

	e.setAccess(t, member, everyOptionalBut("policies"))
	views := map[string]map[string]int{
		"calendar": e.calendarTypes(t, engineer),
		"upcoming": e.upcomingTypes(t, engineer),
		"ics":      e.icsTypes(t, feed),
	}
	up, _ := e.exportPanels(t, engineer)
	views["export upcoming"] = up
	for name, types := range views {
		if types["policy"]+types["policy_ack"] != 0 {
			t.Errorf("no Policies access: %s has policy items %v; want none", name, types)
		}
	}
	if got := e.searchTypes(t, engineer)["policies"]; got != 0 {
		t.Errorf("no Policies access: search returns %d policies; want none", got)
	}
	if got := e.activityTargets(t, engineer)["decision/policies"]; got != 0 {
		t.Errorf("no Policies access: Activity has %d decisions on policies; want none", got)
	}
	// Other modules are untouched by the narrowing.
	if got := e.calendarTypes(t, engineer)["vendor"]; got == 0 {
		t.Errorf("no Policies access: vendor reviews vanished too")
	}
}

// activityDecisionIDs returns the target ids of the caller's Activity
// decision rows.
func (e *combinedViewsEnv) activityDecisionIDs(t *testing.T, bearer string) map[string]bool {
	t.Helper()
	got := e.do(t, http.MethodGet, "/v1/activity/unified?include_reads=true&from="+e.fx.Now.Add(-time.Hour).Format(time.RFC3339)+"&to="+time.Now().UTC().Add(time.Hour).Format(time.RFC3339), bearer, nil)
	if got.status != http.StatusOK {
		t.Fatalf("GET /v1/activity/unified: status %d body=%s", got.status, got.body)
	}
	var body struct {
		Entries []struct {
			Kind     string `json:"kind"`
			TargetID string `json:"target_id"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(got.body), &body); err != nil {
		t.Fatalf("decode activity: %v body=%s", err, got.body)
	}
	out := map[string]bool{}
	for _, en := range body.Entries {
		if en.Kind == "decision" {
			out[en.TargetID] = true
		}
	}
	return out
}

// #2477: an authorization decision is placed by the request path it was
// made on, not by its resource type (the first path segment), so a
// decision on a nested or /v1/me route of a hidden module leaves Activity.
// A legacy row with no recorded path still falls back to its type.
func TestCombinedViews_ActivityDecisionsFollowRequestPath(t *testing.T) {
	e := newCombinedViewsEnv(t)
	member, engineer := e.member(t, "grc_engineer")
	rows := []struct{ resourceType, path string }{
		{"me", "/v1/me/acknowledgments"},
		{"controls", "/v1/controls/" + uuid.NewString() + "/policies"},
		{"policies", ""}, // legacy: no recorded path, placed by its type
		// A recorded path wins over the type: a core route stays shown.
		{"policies", "/v1/controls/" + uuid.NewString()},
	}
	ids := make([]string, len(rows))
	for i, row := range rows {
		ids[i] = uuid.NewString()
		if _, err := e.migrate.Exec(context.Background(),
			`INSERT INTO decision_audit_log
			   (tenant_id, user_id, action, resource_type, resource_id, result, request_path, request_method)
			 VALUES ($1, 'seed-2477', 'read', $2, $3, 'allow', $4, 'GET')`,
			e.tenant, row.resourceType, ids[i], row.path); err != nil {
			t.Fatalf("seed decision %d: %v", i, err)
		}
	}

	before := e.activityDecisionIDs(t, engineer)
	for i, id := range ids {
		if !before[id] {
			t.Fatalf("all modules: Activity is missing decision %d (%s %q)", i, rows[i].resourceType, rows[i].path)
		}
	}

	e.setAccess(t, member, everyOptionalBut("policies"))
	after := e.activityDecisionIDs(t, engineer)
	for i, want := range []bool{false, false, false, true} {
		if after[ids[i]] != want {
			t.Errorf("no Policies access: decision on %s %q shown=%v; want %v", rows[i].resourceType, rows[i].path, after[ids[i]], want)
		}
	}
}

// An admin sees every switched-on module's items in each combined view,
// whatever access is stored on their row.
func TestCombinedViews_AdminSeesEverySwitchedOnModule(t *testing.T) {
	e := newCombinedViewsEnv(t)
	if got := e.do(t, http.MethodGet, "/v1/policies", e.admin, nil); got.status != http.StatusOK {
		t.Fatalf("admin GET /v1/policies: status %d", got.status)
	}
	adminUser, admin := e.member(t, "admin")
	// A narrowing stored on an admin is kept and ignored.
	e.setAccess(t, adminUser, []string{})

	cal := e.calendarTypes(t, admin)
	for _, typ := range []string{"policy", "vendor", "risk", "action_plan", "exception", "control"} {
		if cal[typ] == 0 {
			t.Errorf("admin calendar has no %s events: %v", typ, cal)
		}
	}
	if up := e.upcomingTypes(t, admin); up["risk"] == 0 || up["action_plan"] == 0 {
		t.Errorf("admin upcoming lacks risk or action plan items: %v", up)
	}
	ics := e.icsTypes(t, e.subscribe(t, admin))
	if ics["policy"]+ics["policy_ack"] == 0 {
		t.Errorf("admin ICS feed has no policy items: %v", ics)
	}
	if got := e.searchTypes(t, admin)["policies"]; got == 0 {
		t.Errorf("admin search finds no policies")
	}
	if got := e.activityTargets(t, admin)["decision/policies"]; got == 0 {
		t.Errorf("admin Activity has no decision on policies")
	}
	up, risks := e.exportPanels(t, admin)
	if up["risk"] == 0 || risks == 0 {
		t.Errorf("admin export: upcoming %v, %d risks; want risk items in both", up, risks)
	}
}

// Switching Risk off empties the export's risks panel for everyone, and a
// member without Risk access gets it empty too.
func TestCombinedViews_RiskHiddenEmptiesExportRisks(t *testing.T) {
	e := newCombinedViewsEnv(t)
	member, engineer := e.member(t, "grc_engineer")
	if _, risks := e.exportPanels(t, engineer); risks == 0 {
		t.Fatalf("all modules: export has no risks; the fixture should put some there")
	}
	e.setAccess(t, member, everyOptionalBut("risk"))
	if up, risks := e.exportPanels(t, engineer); risks != 0 || up["risk"]+up["action_plan"] != 0 {
		t.Errorf("no Risk access: export upcoming %v, %d risks; want no risk items", up, risks)
	}
	e.setAccess(t, member, nil)
	e.setSwitch(t, "risk.enabled", false)
	if up, risks := e.exportPanels(t, e.admin); risks != 0 || up["risk"]+up["action_plan"] != 0 {
		t.Errorf("Risk switched off: admin export upcoming %v, %d risks; want no risk items", up, risks)
	}
}
