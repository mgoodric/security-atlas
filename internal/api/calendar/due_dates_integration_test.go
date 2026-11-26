//go:build integration

// #2130 — the calendar is the one list of what is due. These tests read
// the two-tenant attention fixture through GET /v1/calendar and assert
// what a user of each tenant sees: risk reviews and action-plan due dates
// with overdue / due-soon status, and past-due items when the window
// starts in the past.

package calendar_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/dbtest/attention"
)

// calendarEvents GETs path and returns the events keyed by event id.
func calendarEvents(t *testing.T, env testEnv, path string) map[string]map[string]any {
	t.Helper()
	resp, body := get(t, env, path)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, resp.StatusCode, string(body))
	}
	out := map[string]map[string]any{}
	for _, e := range decodeJSON(t, body)["events"].([]any) {
		ev := e.(map[string]any)
		out[ev["id"].(string)] = ev
	}
	return out
}

func ymd(t time.Time) string { return t.UTC().Format("2006-01-02") }

// wantEvent asserts events holds id with the given type and status.
func wantEvent(t *testing.T, events map[string]map[string]any, id, typ, status string) {
	t.Helper()
	ev, ok := events[id]
	if !ok {
		t.Errorf("event %s (%s, want status %s) missing from the calendar", id, typ, status)
		return
	}
	if ev["type"] != typ || ev["status"] != status {
		t.Errorf("event %s: type=%v status=%v, want type=%s status=%s", id, ev["type"], ev["status"], typ, status)
	}
}

// TestCalendar_RiskReviewsAndActionPlansCarryStatus: a window from 7 days
// ago to 14 days ahead returns the overdue risk review and action plan as
// overdue, the ones due in 7 days as due-soon, and nothing from the other
// tenant. The risk due in 30 days is outside the window.
func TestCalendar_RiskReviewsAndActionPlansCarryStatus(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)

	path := "/v1/calendar?types=risk,action_plan&from=" + ymd(now.AddDate(0, 0, -7)) + "&to=" + ymd(now.AddDate(0, 0, 14))
	events := calendarEvents(t, testServer(t, app, fx.A.ID), path)

	wantEvent(t, events, fx.A.OverdueRisk.String(), "risk", "overdue")
	wantEvent(t, events, fx.A.DueSoonRisk.String(), "risk", "due-soon")
	wantEvent(t, events, fx.A.OverduePlan.String(), "action_plan", "overdue")
	wantEvent(t, events, fx.A.DueSoonPlan.String(), "action_plan", "due-soon")

	if ev := events[fx.A.OverdueRisk.String()]; ev != nil {
		if ev["title"] != "Risk review: tenant A risk: overdue review" || ev["related_entity_kind"] != "risk" {
			t.Errorf("risk event: title=%v kind=%v", ev["title"], ev["related_entity_kind"])
		}
	}
	if ev := events[fx.A.OverduePlan.String()]; ev != nil {
		if ev["title"] != "Action plan due: tenant A plan: past due" || ev["related_entity_kind"] != "action_plan" {
			t.Errorf("action plan event: title=%v kind=%v", ev["title"], ev["related_entity_kind"])
		}
		// due_date is a DATE: the event sits at 00:00 UTC on that day.
		if want := ymd(now.AddDate(0, 0, -2)) + "T00:00:00Z"; ev["starts_at"] != want {
			t.Errorf("action plan starts_at=%v, want %s", ev["starts_at"], want)
		}
	}

	if _, ok := events[fx.A.CompletedPlan.String()]; ok {
		t.Error("a completed action plan is on the calendar")
	}
	if _, ok := events[fx.A.LaterRisk.String()]; ok {
		t.Error("a risk review due in 30 days is inside a 14-day window")
	}
	for _, id := range []string{fx.B.OverdueRisk.String(), fx.B.DueSoonRisk.String(), fx.B.OverduePlan.String(), fx.B.DueSoonPlan.String()} {
		if _, ok := events[id]; ok {
			t.Errorf("tenant A sees tenant B's event %s", id)
		}
	}
	if got := len(events); got != 4 {
		t.Errorf("tenant A sees %d risk/action-plan events, want 4: %v", got, events)
	}

	// A review further out is upcoming, not due soon.
	later := calendarEvents(t, testServer(t, app, fx.A.ID),
		"/v1/calendar?types=risk&from="+ymd(now)+"&to="+ymd(now.AddDate(0, 0, 60)))
	wantEvent(t, later, fx.A.LaterRisk.String(), "risk", "upcoming")
}

// TestCalendar_PastWindowIncludesPastDueItems: every dated source in the
// fixture that fell due in the last 60 days appears when the window
// starts in the past, each marked overdue; the default window (from now)
// leaves them out.
func TestCalendar_PastWindowIncludesPastDueItems(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)
	env := testServer(t, app, fx.A.ID)

	past := calendarEvents(t, env, "/v1/calendar?from="+ymd(now.AddDate(0, 0, -60))+"&to="+ymd(now.AddDate(0, 0, -1)))
	wantEvent(t, past, fx.A.OverdueRisk.String(), "risk", "overdue")
	wantEvent(t, past, fx.A.OverduePlan.String(), "action_plan", "overdue")
	wantEvent(t, past, fx.A.OverduePolicy.String(), "policy", "overdue")
	if _, ok := past[fx.A.OverdueVendor.String()]; !ok {
		t.Errorf("the overdue vendor review is missing from a past window")
	}
	for id, ev := range past {
		if ev["type"] == "risk" || ev["type"] == "action_plan" || ev["type"] == "policy" {
			if ev["status"] != "overdue" {
				t.Errorf("past event %s (%v) status=%v, want overdue", id, ev["type"], ev["status"])
			}
		}
	}

	current := calendarEvents(t, env, "/v1/calendar")
	for _, id := range []string{fx.A.OverdueRisk.String(), fx.A.OverduePlan.String(), fx.A.OverduePolicy.String()} {
		if _, ok := current[id]; ok {
			t.Errorf("the default window (from now) includes past-due event %s", id)
		}
	}
}

// TestCalendar_FixtureOtherSourcesStayInTheirTenant: tenant B's calendar
// over a wide window holds tenant B's dated items and none of tenant A's.
func TestCalendar_FixtureOtherSourcesStayInTheirTenant(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)

	events := calendarEvents(t, testServer(t, app, fx.B.ID),
		"/v1/calendar?from="+ymd(now.AddDate(0, 0, -60))+"&to="+ymd(now.AddDate(0, 0, 60)))
	for _, id := range []string{
		fx.A.OverdueRisk.String(), fx.A.OverduePlan.String(), fx.A.OverduePolicy.String(),
		fx.A.OverdueVendor.String(), fx.A.ExpiringWaiver.String(), fx.A.PeriodicControl.String(),
	} {
		if _, ok := events[id]; ok {
			t.Errorf("tenant B sees tenant A's event %s", id)
		}
	}
	wantEvent(t, events, fx.B.ExpiringWaiver.String(), "exception", "active")
	wantEvent(t, events, fx.B.PeriodicControl.String(), "control", "overdue")
	wantEvent(t, events, fx.B.OverdueRisk.String(), "risk", "overdue")
}

// TestCalendar_RollUpAndChildrenEachListedOnce: a roll-up's own review
// date is real, so it gets an event; its children keep theirs, and no
// risk appears twice however many roll-ups it sits under (#2130, D2).
func TestCalendar_RollUpAndChildrenEachListedOnce(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	now := time.Now().UTC()
	fx := attention.Seed(t, admin, now)
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DELETE FROM risk_aggregations WHERE tenant_id = $1`, fx.A.ID)
	})

	// Two roll-ups over the same two children: the due-soon risk is the
	// first roll-up, the 30-day risk the second; the overdue and unowned
	// risks are the children of both.
	for _, parent := range []uuid.UUID{fx.A.DueSoonRisk, fx.A.LaterRisk} {
		for _, child := range []uuid.UUID{fx.A.OverdueRisk, fx.A.UnownedRisk} {
			if _, err := admin.Exec(context.Background(), `
				INSERT INTO risk_aggregations (parent_risk_id, child_risk_id, tenant_id)
				VALUES ($1, $2, $3)
			`, parent, child, fx.A.ID); err != nil {
				t.Fatalf("seed risk_aggregations: %v", err)
			}
		}
	}

	resp, body := get(t, testServer(t, app, fx.A.ID),
		"/v1/calendar?types=risk&from="+ymd(now.AddDate(0, 0, -7))+"&to="+ymd(now.AddDate(0, 0, 150)))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("calendar GET: status=%d body=%s", resp.StatusCode, string(body))
	}
	seen := map[string]int{}
	for _, e := range decodeJSON(t, body)["events"].([]any) {
		seen[e.(map[string]any)["id"].(string)]++
	}
	for _, id := range []uuid.UUID{fx.A.DueSoonRisk, fx.A.LaterRisk, fx.A.OverdueRisk, fx.A.UnownedRisk} {
		if seen[id.String()] != 1 {
			t.Errorf("risk %s listed %d times, want once", id, seen[id.String()])
		}
	}
	if len(seen) != 4 {
		t.Errorf("calendar lists %d risks, want 4: %v", len(seen), seen)
	}
}
