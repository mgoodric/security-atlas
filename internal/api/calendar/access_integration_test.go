//go:build integration

// #2130 review — risk reviews and action-plan due dates reach only callers
// who may read risks / action plans; the ICS feed (whose token names no
// user) never carries them; and policy acknowledgement expiries stay on the
// calendar after the upcoming rollup's retirement. #2232 gates vendor and
// exception events the same way, on vendors and exceptions reads.

package calendar_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fixedRoles []authz.Role

func (f fixedRoles) RolesFor(context.Context, string, string) ([]authz.Role, error) {
	return f, nil
}

// calendarAs serves GET /v1/calendar through the real handler and the
// real rego bundle, for a human user of tenant holding role.
func calendarAs(t *testing.T, tenant string, role authz.Role, query string) map[string]map[string]any {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), fixedRoles{role})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	h := calendar.New(calendar.NewStore(dbtest.NewAppPool(t)), nil).WithAuthz(engine)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				ID: "calendar-access-test", TenantID: tenant,
				UserID: "22222222-2222-2222-2222-222222222222", OwnerRoles: []string{"security_lead"},
			})
			ctx, err := tenancy.WithTenant(ctx, tenant)
			if err != nil {
				t.Errorf("WithTenant: %v", err)
				return
			}
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/calendar", h.ListEvents)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1/calendar"+query, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("%s GET /v1/calendar%s: status=%d body=%s", role, query, rec.Code, rec.Body.String())
	}
	var body struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	out := map[string]map[string]any{}
	for _, ev := range body.Events {
		out[ev["id"].(string)] = ev
	}
	return out
}

func countType(events map[string]map[string]any, typ string) int {
	n := 0
	for _, ev := range events {
		if ev["type"] == typ {
			n++
		}
	}
	return n
}

// TestCalendar_RiskAndPlanEventsFollowOPA: an auditor and a control owner
// see neither risk reviews nor action plans, asked for or not; a viewer
// sees risk reviews but no plans; a GRC engineer sees both. #2232: vendor
// events follow a vendors read (viewer, GRC engineer) and exception events
// an exceptions read (auditor, GRC engineer); the control owner sees
// neither. Every role still sees the ungated events.
func TestCalendar_RiskAndPlanEventsFollowOPA(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)
	window := "from=" + ymd(now.AddDate(0, 0, -7)) + "&to=" + ymd(now.AddDate(0, 0, 14))

	cases := []struct {
		role              authz.Role
		risks, plan       int
		vendor, exception bool
	}{
		{authz.RoleAuditor, 0, 0, false, true},
		{authz.RoleControlOwner, 0, 0, false, false},
		{authz.RoleViewer, 2, 0, true, false},
		{authz.RoleGRCEngineer, 2, 2, true, true},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			all := calendarAs(t, fx.A.ID, tc.role, "?"+window)
			if got := countType(all, "risk"); got != tc.risks {
				t.Errorf("risk events = %d, want %d", got, tc.risks)
			}
			if got := countType(all, "action_plan"); got != tc.plan {
				t.Errorf("action_plan events = %d, want %d", got, tc.plan)
			}
			if tc.exception {
				wantEvent(t, all, fx.A.ExpiringWaiver.String(), "exception", "active")
			} else if got := countType(all, "exception"); got != 0 {
				t.Errorf("exception events = %d, want none", got)
			}
			wantEvent(t, all, fx.A.OverduePolicy.String(), "policy", "overdue")

			asked := calendarAs(t, fx.A.ID, tc.role, "?types=risk,action_plan&"+window)
			if got := countType(asked, "risk") + countType(asked, "action_plan"); got != tc.risks+tc.plan {
				t.Errorf("asking for risk,action_plan: %d events, want %d", got, tc.risks+tc.plan)
			}
			// The fixture's vendor review fell due 10 days ago: a wider window.
			wide := "from=" + ymd(now.AddDate(0, 0, -30)) + "&to=" + ymd(now.AddDate(0, 0, 14))
			asked = calendarAs(t, fx.A.ID, tc.role, "?types=vendor,exception&"+wide)
			if tc.vendor {
				wantEvent(t, asked, fx.A.OverdueVendor.String(), "vendor", "overdue")
			}
			if got, want := countType(asked, "vendor") > 0, tc.vendor; got != want {
				t.Errorf("asking for vendor,exception: vendor events present=%v, want %v", got, want)
			}
			if got, want := countType(asked, "exception") > 0, tc.exception; got != want {
				t.Errorf("asking for vendor,exception: exception events present=%v, want %v", got, want)
			}
		})
	}
}

// TestCalendarICS_LeavesOutRiskAndPlanEvents: the ICS feed carries the
// ungated events and no risk review, action plan, vendor or exception,
// even when asked (#2130, #2232).
func TestCalendarICS_LeavesOutRiskAndPlanEvents(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)
	env := testServer(t, app, fx.A.ID)

	subReq, _ := http.NewRequest(http.MethodPost, env.server.URL+"/v1/calendar/subscription", nil)
	subReq.Header.Set("Authorization", "Bearer "+env.bearer)
	subResp, err := http.DefaultClient.Do(subReq)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	var sub map[string]any
	_ = json.NewDecoder(subResp.Body).Decode(&sub)
	_ = subResp.Body.Close()
	url, _ := sub["url"].(string)
	token := url[strings.Index(url, "?token=")+len("?token="):]

	window := "&from=" + ymd(now.AddDate(0, 0, -7)) + "&to=" + ymd(now.AddDate(0, 0, 14))
	for _, types := range []string{"", "&types=risk,action_plan,exception"} {
		resp, err := http.Get(env.server.URL + "/v1/calendar.ics?token=" + token + window + types)
		if err != nil {
			t.Fatalf("ICS GET: %v", err)
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("ICS%s status=%d body=%s", types, resp.StatusCode, body)
		}
		ics := string(body)
		// The acknowledgement lapsing in 10 days is in the feed without its
		// user: no user id reaches an ICS subscriber.
		if types == "" && !strings.Contains(ics, "acknowledgement lapses on") {
			t.Errorf("ICS lost the policy acknowledgement event")
		}
		for _, banned := range []string{
			"Risk review:", "Action plan due:", fx.A.OverdueRisk.String(), fx.A.OverduePlan.String(),
			fx.A.Owner.String(), fx.A.Other.String(),
			"Vendor review:", "Vendor renewal:", fx.A.OverdueVendor.String(),
			"Exception on", fx.A.ExpiringWaiver.String(),
		} {
			if strings.Contains(ics, banned) {
				t.Errorf("ICS%s carries %q", types, banned)
			}
		}
		if types == "" && !strings.Contains(ics, "Policy review:") {
			t.Errorf("ICS lost the ungated policy review event")
		}
	}
}

// ackEventID is the id of the grouped policy_ack event for policy whose
// acknowledgements lapse on lapse's UTC day (#2234).
func ackEventID(policy uuid.UUID, lapse time.Time) string {
	return policy.String() + ":" + ymd(lapse)
}

// TestCalendar_PolicyAckExpiries: the latest acknowledgement per policy
// version and user lapses 365 days on; it is due-soon inside 14 days,
// overdue once lapsed, and an older superseded ack is not counted. Acks
// are grouped per policy and lapse day (#2234), so an event names the
// policy and the count, never who acknowledged.
func TestCalendar_PolicyAckExpiries(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	day := 24 * time.Hour
	fx := attention.Seed(t, admin, now)
	dueSoon := ackEventID(fx.A.OverduePolicy, now.Add(10*day))
	overdue := ackEventID(fx.A.OverduePolicy, now.Add(-35*day))

	events := calendarEvents(t, testServer(t, app, fx.A.ID),
		"/v1/calendar?types=policy_ack&from="+ymd(now.AddDate(0, 0, -60))+"&to="+ymd(now.AddDate(0, 0, 14)))
	wantEvent(t, events, dueSoon, "policy_ack", "due-soon")
	wantEvent(t, events, overdue, "policy_ack", "overdue")
	if got := len(events); got != 2 {
		t.Errorf("%d policy_ack events, want 2 (the superseded ack is not counted): %v", got, events)
	}
	if ev := events[dueSoon]; ev != nil {
		want := "tenant A policy: overdue review: 1 acknowledgement lapses on " + ymd(now.Add(10*day))
		if ev["title"] != want {
			t.Errorf("title=%v, want %q", ev["title"], want)
		}
		// Who acknowledged is not exposed: the summary names the policy.
		if want := "tenant A policy: overdue review"; ev["summary"] != want {
			t.Errorf("summary=%v, want %q", ev["summary"], want)
		}
		if ev["related_entity_kind"] != "policy" || ev["related_entity_id"] != fx.A.OverduePolicy.String() {
			t.Errorf("related entity = %v %v, want the policy", ev["related_entity_kind"], ev["related_entity_id"])
		}
	}
	for id, ev := range events {
		for _, field := range []string{"title", "summary", "related_entity_id"} {
			v, _ := ev[field].(string)
			if strings.Contains(v, fx.A.Owner.String()) || strings.Contains(v, fx.A.Other.String()) {
				t.Errorf("policy_ack %s: %s carries a user id: %q", id, field, v)
			}
		}
	}
	if _, ok := events[ackEventID(fx.B.OverduePolicy, now.Add(10*day))]; ok {
		t.Error("tenant A sees tenant B's acknowledgement")
	}

	// The dashboard's "Coming up" window (today + 14 days) carries the
	// lapsing acknowledgement.
	comingUp := calendarEvents(t, testServer(t, app, fx.A.ID),
		"/v1/calendar?from="+ymd(now)+"&to="+ymd(now.AddDate(0, 0, 13)))
	wantEvent(t, comingUp, dueSoon, "policy_ack", "due-soon")
}

// TestCalendar_PolicyAckGroupedAndScoped (#2234): a campaign's acks that
// lapse on one day are one event carrying the count, and another day is
// another event. Acks of a superseded policy version, and acks by a
// disabled or SCIM-deactivated user, are left out.
func TestCalendar_PolicyAckGroupedAndScoped(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	day := 24 * time.Hour
	tenant := dbtest.SeedTenant(t, admin, "policy_acknowledgments", "policies", "users")
	exec := func(what, sql string, args ...any) {
		t.Helper()
		if _, err := admin.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("seed %s: %v", what, err)
		}
	}
	user := func(status string, active bool) uuid.UUID {
		id := uuid.New()
		exec("user", `
			INSERT INTO users (id, tenant_id, email, display_name, status, active)
			VALUES ($1, $2, $3, 'Ack campaign', $4, $5)
		`, id, tenant, "ack-"+id.String()[:8]+"@test.example", status, active)
		return id
	}
	policy := func(title, status string, supersededAt *time.Time) uuid.UUID {
		id := uuid.New()
		exec("policy "+title, `
			INSERT INTO policies (
				id, tenant_id, title, version, status, effective_date,
				body_md, owner_role, approver_role, created_by,
				published_at, published_by, superseded_at
			)
			VALUES ($1, $2, $3, '1.0.0', $4, now()::date,
			        '# body', 'security_lead', 'ciso', 'tester',
			        now(), 'tester', $5)
		`, id, tenant, title, status, supersededAt)
		return id
	}
	ack := func(p, u uuid.UUID, when time.Time) {
		id := uuid.New()
		exec("ack", `
			INSERT INTO policy_acknowledgments (
				id, tenant_id, policy_id, policy_version_id, user_id, acknowledged_at, ack_token
			)
			VALUES ($1, $2, $3, $3, $4, $5, $6)
		`, id, tenant, p, u, when, "campaign-"+id.String())
	}

	current := policy("Acceptable use", "published", nil)
	old := policy("Acceptable use (old)", "superseded", &now)
	campaign := now.Add(-355 * day)
	for i := 0; i < 3; i++ {
		ack(current, user("active", true), campaign)
	}
	ack(current, user("disabled", true), campaign)
	ack(current, user("active", false), campaign)
	ack(old, user("active", true), campaign)
	ack(current, user("active", true), campaign.Add(-2*day))

	events := calendarEvents(t, testServer(t, app, tenant),
		"/v1/calendar?types=policy_ack&from="+ymd(now)+"&to="+ymd(now.AddDate(0, 0, 13)))
	grouped := ackEventID(current, campaign.Add(365*day))
	wantEvent(t, events, grouped, "policy_ack", "due-soon")
	if want := "Acceptable use: 3 acknowledgements lapse on " + ymd(campaign.Add(365*day)); events[grouped]["title"] != want {
		t.Errorf("campaign title=%v, want %q", events[grouped]["title"], want)
	}
	single := ackEventID(current, campaign.Add(363*day))
	wantEvent(t, events, single, "policy_ack", "due-soon")
	if want := "Acceptable use: 1 acknowledgement lapses on " + ymd(campaign.Add(363*day)); events[single]["title"] != want {
		t.Errorf("single title=%v, want %q", events[single]["title"], want)
	}
	if got := len(events); got != 2 {
		t.Errorf("%d policy_ack events, want 2 (none for the superseded version): %v", got, events)
	}
}

// TestCalendar_OffboardingEventsFollowOPA (#2215): an open offboarding
// checklist is a calendar event of type `offboarding` linked to the
// checklist, the type filter can name it, and it reaches only roles that
// may read personnel security, since its title carries the departing
// person's work email.
func TestCalendar_OffboardingEventsFollowOPA(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	now := time.Now().UTC()
	tenant := dbtest.SeedTenant(t, admin, "personnel_security_checklists")
	checklist := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO personnel_security_checklists (
			id, tenant_id, workflow_kind, source, person_external_id,
			person_work_email, due_at, status
		)
		VALUES ($1, $2, 'offboarding', 'manual', 'emp-2215',
		        'departing-2215@test.example', $3, 'open')
	`, checklist, tenant, now.Add(3*24*time.Hour)); err != nil {
		t.Fatalf("seed offboarding checklist: %v", err)
	}
	window := "from=" + ymd(now.AddDate(0, 0, -7)) + "&to=" + ymd(now.AddDate(0, 0, 14))

	cases := []struct {
		role authz.Role
		want int
	}{
		{authz.RoleAuditor, 0},
		{authz.RoleControlOwner, 0},
		{authz.RoleViewer, 0},
		{authz.RoleGRCEngineer, 1},
		{authz.RoleAdmin, 1},
	}
	for _, tc := range cases {
		t.Run(string(tc.role), func(t *testing.T) {
			for _, q := range []string{"?" + window, "?types=offboarding&" + window} {
				events := calendarAs(t, tenant, tc.role, q)
				if got := countType(events, "offboarding"); got != tc.want {
					t.Fatalf("%s: offboarding events = %d, want %d", q, got, tc.want)
				}
				if tc.want == 0 {
					continue
				}
				ev := events[checklist.String()]
				if ev["related_entity_kind"] != "personnel_security_checklist" ||
					ev["related_entity_id"] != checklist.String() {
					t.Errorf("%s: offboarding event = %v", q, ev)
				}
			}
		})
	}
}
