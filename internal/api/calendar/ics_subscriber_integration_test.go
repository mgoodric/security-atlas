//go:build integration

// #2331 — the ICS feed shows the gated event types its subscriber may read,
// asked of OPA with their live user_roles at every fetch (ADR 0026). A
// demoted, disabled or removed subscriber's feed narrows at the next read,
// a token that names no subscriber shows no gated type, and the token is
// no bearer for any other route.

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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
	"github.com/mgoodric/security-atlas/internal/authz"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

// authzServer is testServer with the real OPA engine attached, resolving
// roles from user_roles as production does.
func authzServer(t *testing.T, app, admin *pgxpool.Pool, fx attention.Fixture) testEnv {
	t.Helper()
	engine, err := authz.NewEngine(context.Background(), authz.NewDBRolesResolver(app))
	if err != nil {
		t.Fatalf("authz.NewEngine: %v", err)
	}
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	srv.AttachAuthz(engine, authz.NewAuditWriter(app))
	// The first test JWT attaches the validator, which the handler must
	// see when it is built.
	_ = srv.IssueTestJWT(t, testjwt.ViewerFor(uuid.MustParse(fx.A.ID)))
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)
	t.Cleanup(func() {
		for _, tenant := range []string{fx.A.ID, fx.B.ID} {
			for _, table := range []string{"decision_audit_log", "api_keys"} {
				_, _ = admin.Exec(context.Background(), "DELETE FROM "+table+" WHERE tenant_id = $1", tenant)
			}
		}
	})
	return testEnv{server: ts, srv: srv}
}

// gatedSeen is which of the fixture's gated events an ICS body carries.
type gatedSeen struct{ risk, plan, vendor, exception bool }

func gatedIn(ics string, at attention.Tenant) gatedSeen {
	return gatedSeen{
		risk:      strings.Contains(ics, "Risk review:"),
		plan:      strings.Contains(ics, "Action plan due:"),
		vendor:    strings.Contains(ics, at.OverdueVendor.String()),
		exception: strings.Contains(ics, at.ExpiringWaiver.String()),
	}
}

// setRole replaces user's roles in tenant with role ("" removes them all).
func setRole(t *testing.T, admin *pgxpool.Pool, tenant string, user uuid.UUID, role string) {
	t.Helper()
	ctx := context.Background()
	if _, err := admin.Exec(ctx, `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String()); err != nil {
		t.Fatalf("clear user_roles: %v", err)
	}
	if role != "" {
		if _, err := admin.Exec(ctx,
			`INSERT INTO user_roles (tenant_id, user_id, role, granted_by) VALUES ($1, $2, $3, 'test-2331')`,
			tenant, user.String(), role); err != nil {
			t.Fatalf("seed user_role %s: %v", role, err)
		}
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM user_roles WHERE tenant_id = $1 AND user_id = $2`, tenant, user.String())
	})
}

// subscribeAs mints an ICS token through POST /v1/calendar/subscription,
// signed in as user of tenant.
func subscribeAs(t *testing.T, env testEnv, tenant string, user uuid.UUID) string {
	t.Helper()
	claims := testjwt.OwnerFor(uuid.MustParse(tenant), []string{"control_owner"})
	claims.Subject = "user:" + user.String()
	req, _ := http.NewRequest(http.MethodPost, env.server.URL+"/v1/calendar/subscription", nil)
	req.Header.Set("Authorization", "Bearer "+env.srv.IssueTestJWT(t, claims))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("subscribe status=%d body=%s", resp.StatusCode, b)
	}
	var sub map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&sub)
	url, _ := sub["url"].(string)
	return url[strings.Index(url, "?token=")+len("?token="):]
}

// fetchICS reads the feed for token over a window wide enough to hold
// every fixture event (the vendor review fell due 10 days ago).
func fetchICS(t *testing.T, env testEnv, token, types string) string {
	t.Helper()
	now := time.Now().UTC()
	url := env.server.URL + "/v1/calendar.ics?token=" + token +
		"&from=" + ymd(now.AddDate(0, 0, -30)) + "&to=" + ymd(now.AddDate(0, 0, 14)) + types
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("ICS GET: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ICS%s status=%d body=%s", types, resp.StatusCode, body)
	}
	return string(body)
}

// wantPolicyReview checks a member's feed still carries the ungated policy
// review. (A subscriber with no live role sees core modules only, #2173, so
// a removed or disabled one does not get it.)
func wantPolicyReview(t *testing.T, ics string) {
	t.Helper()
	if !strings.Contains(ics, "Policy review:") {
		t.Errorf("ICS lost the ungated policy review event")
	}
}

// TestCalendarICS_GatedTypesFollowSubscriberRole: each role's feed carries
// exactly the gated events OPA lets that role read, the same set the JSON
// calendar shows it (TestCalendar_RiskAndPlanEventsFollowOPA), asked for or
// not.
func TestCalendarICS_GatedTypesFollowSubscriberRole(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, admin, time.Now().UTC())
	env := authzServer(t, app, admin, fx)

	cases := []struct {
		role string
		want gatedSeen
	}{
		{"admin", gatedSeen{true, true, true, true}},
		{"grc_engineer", gatedSeen{true, true, true, true}},
		{"viewer", gatedSeen{risk: true, vendor: true}},
		{"auditor", gatedSeen{exception: true}},
		{"control_owner", gatedSeen{}},
	}
	for _, tc := range cases {
		t.Run(tc.role, func(t *testing.T) {
			setRole(t, admin, fx.A.ID, fx.A.Owner, tc.role)
			token := subscribeAs(t, env, fx.A.ID, fx.A.Owner)
			for _, types := range []string{"", "&types=risk,action_plan,vendor,exception"} {
				ics := fetchICS(t, env, token, types)
				if got := gatedIn(ics, fx.A); got != tc.want {
					t.Errorf("ICS%s gated = %+v, want %+v", types, got, tc.want)
				}
				if types == "" {
					wantPolicyReview(t, ics)
				}
			}
		})
	}
}

// TestCalendarICS_RoleChangeTakesEffectAtNextRead: the feed is decided at
// fetch time, so promoting the subscriber widens it and demoting them
// narrows it on the very next read, with the same token. Removing their
// roles, or disabling them, leaves no gated type.
func TestCalendarICS_RoleChangeTakesEffectAtNextRead(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, admin, time.Now().UTC())
	env := authzServer(t, app, admin, fx)

	setRole(t, admin, fx.A.ID, fx.A.Owner, "grc_engineer")
	token := subscribeAs(t, env, fx.A.ID, fx.A.Owner)
	if got := gatedIn(fetchICS(t, env, token, ""), fx.A); got != (gatedSeen{true, true, true, true}) {
		t.Fatalf("grc_engineer feed gated = %+v, want all", got)
	}

	setRole(t, admin, fx.A.ID, fx.A.Owner, "control_owner")
	if got := gatedIn(fetchICS(t, env, token, ""), fx.A); got != (gatedSeen{}) {
		t.Errorf("after demotion to control_owner: gated = %+v, want none", got)
	}

	setRole(t, admin, fx.A.ID, fx.A.Owner, "viewer")
	if got := gatedIn(fetchICS(t, env, token, ""), fx.A); got != (gatedSeen{risk: true, vendor: true}) {
		t.Errorf("after change to viewer: gated = %+v, want risk+vendor", got)
	}

	setRole(t, admin, fx.A.ID, fx.A.Owner, "")
	if got := gatedIn(fetchICS(t, env, token, ""), fx.A); got != (gatedSeen{}) {
		t.Errorf("after removal from the tenant: gated = %+v, want none", got)
	}

	// A disabled user keeps their user_roles row; the feed still shows no
	// gated type. (Production also refuses the token outright: the
	// apikeystore user gate, #1915, which this test server does not wire.)
	setRole(t, admin, fx.A.ID, fx.A.Owner, "admin")
	if _, err := admin.Exec(context.Background(),
		`UPDATE users SET status = 'disabled' WHERE id = $1`, fx.A.Owner); err != nil {
		t.Fatalf("disable user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `UPDATE users SET status = 'active' WHERE id = $1`, fx.A.Owner)
	})
	if got := gatedIn(fetchICS(t, env, token, ""), fx.A); got != (gatedSeen{}) {
		t.Errorf("disabled admin subscriber: gated = %+v, want none", got)
	}
}

// TestCalendarICS_LegacyTokenShowsNoGatedTypes: a token minted before
// #2173 recorded its subscriber names no user, so its feed shows no gated
// type, whatever the person who made it holds, until they subscribe again.
func TestCalendarICS_LegacyTokenShowsNoGatedTypes(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, admin, time.Now().UTC())
	env := authzServer(t, app, admin, fx)

	setRole(t, admin, fx.A.ID, fx.A.Owner, "admin")
	token := subscribeAs(t, env, fx.A.ID, fx.A.Owner)
	// Before #2173 the calendar token was a plain Issue: its UserID was its
	// own key id, which names no person.
	if err := env.srv.RebindBearerUserIDForTests(token, "key_legacy_2331"); err != nil {
		t.Fatalf("rebind: %v", err)
	}
	ics := fetchICS(t, env, token, "&types=risk,action_plan,vendor,exception,policy")
	if got := gatedIn(ics, fx.A); got != (gatedSeen{}) {
		t.Errorf("legacy token gated = %+v, want none", got)
	}
}

// TestCalendarICS_SubscriberSeesOnlyTheirTenant: an admin of tenant B
// subscribed to B's feed sees B's gated events and none of A's.
func TestCalendarICS_SubscriberSeesOnlyTheirTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, admin, time.Now().UTC())
	env := authzServer(t, app, admin, fx)

	setRole(t, admin, fx.B.ID, fx.B.Owner, "admin")
	// The same person id holding admin in A must not widen B's feed into A.
	setRole(t, admin, fx.A.ID, fx.A.Owner, "admin")
	token := subscribeAs(t, env, fx.B.ID, fx.B.Owner)
	ics := fetchICS(t, env, token, "")
	if got := gatedIn(ics, fx.B); got != (gatedSeen{true, true, true, true}) {
		t.Errorf("tenant B admin gated = %+v, want all of B's", got)
	}
	for _, id := range []uuid.UUID{fx.A.OverdueRisk, fx.A.OverduePlan, fx.A.OverdueVendor, fx.A.ExpiringWaiver} {
		if strings.Contains(ics, id.String()) {
			t.Errorf("tenant B feed carries tenant A's %s", id)
		}
	}

	// A's subscriber with no role in B gets nothing gated from B's data
	// either: the token's tenant is A, so its feed is A's.
	tokenA := subscribeAs(t, env, fx.A.ID, fx.A.Owner)
	icsA := fetchICS(t, env, tokenA, "")
	for _, id := range []uuid.UUID{fx.B.OverdueRisk, fx.B.OverduePlan, fx.B.OverdueVendor, fx.B.ExpiringWaiver} {
		if strings.Contains(icsA, id.String()) {
			t.Errorf("tenant A feed carries tenant B's %s", id)
		}
	}
}

// TestCalendarICS_TokenRefusedOutsideTheFeed: the subscriber's ICS token
// reads the feed and nothing else. Presented as a bearer it is refused on
// every route, the calendar's own JSON list included, whatever the
// subscriber holds.
func TestCalendarICS_TokenRefusedOutsideTheFeed(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	fx := attention.Seed(t, admin, time.Now().UTC())
	env := authzServer(t, app, admin, fx)

	setRole(t, admin, fx.A.ID, fx.A.Owner, "admin")
	token := subscribeAs(t, env, fx.A.ID, fx.A.Owner)
	for _, rt := range []struct{ method, path string }{
		{http.MethodGet, "/v1/calendar"},
		{http.MethodGet, "/v1/risks"},
		{http.MethodGet, "/v1/vendors"},
		{http.MethodGet, "/v1/me"},
		{http.MethodPost, "/v1/calendar/subscription"},
	} {
		req, _ := http.NewRequest(rt.method, env.server.URL+rt.path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", rt.method, rt.path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s %s with an ICS token: status=%d, want 401", rt.method, rt.path, resp.StatusCode)
		}
	}
}
