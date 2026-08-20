//go:build integration

package driftalerts_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/drift"
	"github.com/mgoodric/security-atlas/internal/driftalerts"
	"github.com/mgoodric/security-atlas/internal/freshness"
	"github.com/mgoodric/security-atlas/internal/notify/scheduler"
	"github.com/mgoodric/security-atlas/internal/notify/slack"
	"github.com/mgoodric/security-atlas/internal/notify/webhook"
)

type fakePost struct{ bodies [][]byte }

func (f *fakePost) Post(_ context.Context, body []byte) error {
	f.bodies = append(f.bodies, append([]byte(nil), body...))
	return nil
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"channel_delivery_log",
		"drift_freshness_alert_log",
		"drift_freshness_alert_config",
		"slack_channel_optin",
		"webhook_channel_optin",
		"notifications",
		"control_drift_snapshots",
		"evidence_freshness",
		"control_evaluations",
		"users",
		"controls",
	)
}

func seedUser(t *testing.T, admin *pgxpool.Pool, tenant string, slackOn, pagerOn bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, time_zone)
		VALUES ($1, $2, $3, 'Alert User', 'active', '')
	`, id, tenant, id.String()+"@example.test"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if slackOn {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO slack_channel_optin (tenant_id, user_id, enabled, updated_at)
			VALUES ($1, $2, true, now())
		`, tenant, id); err != nil {
			t.Fatalf("seed slack optin: %v", err)
		}
	}
	if pagerOn {
		if _, err := admin.Exec(context.Background(), `
			INSERT INTO webhook_channel_optin (tenant_id, user_id, enabled, updated_at)
			VALUES ($1, $2, true, now())
		`, tenant, id); err != nil {
			t.Fatalf("seed webhook optin: %v", err)
		}
	}
	return id
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			lifecycle_state, applicability_expr, freshness_class, bundle_id
		)
		VALUES ($1, $2, 'OE-599 alert control', 'AAA', 'automated', 'active', 'true', 'daily', $3)
	`, id, tenant, "oe-599-alert-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func seedConfig(t *testing.T, admin *pgxpool.Pool, tenant string, slackOn, pagerOn bool) {
	t.Helper()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO drift_freshness_alert_config (
			tenant_id, enabled, slack_enabled, pagerduty_enabled,
			control_drift_enabled, evidence_staleness_enabled,
			min_drifted_controls, min_stale_age, debounce_interval
		)
		VALUES ($1, true, $2, $3, true, true, 1, '0 seconds', '15 minutes')
	`, tenant, slackOn, pagerOn); err != nil {
		t.Fatalf("seed config: %v", err)
	}
}

func countNotifications(t *testing.T, admin *pgxpool.Pool, tenant string) int {
	t.Helper()
	var n int
	if err := admin.QueryRow(context.Background(), `SELECT count(*) FROM notifications WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	return n
}

func TestAlertTenant_OneAlertPerEventDedupeAndTenantRouting(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	userA := seedUser(t, admin, tenantA, true, true)
	seedUser(t, admin, tenantB, true, true)
	controlA := seedControl(t, admin, tenantA)
	controlB := seedControl(t, admin, tenantB)
	seedConfig(t, admin, tenantA, true, true)
	seedConfig(t, admin, tenantB, false, true)

	now := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	validUntil := now.Add(-time.Minute)
	store := driftalerts.NewStore(app)

	ctxA := dbtest.WithTenantCtx(t, tenantA)
	before := drift.Snapshot{PassingControlIDs: []uuid.UUID{controlA}, SnapshotDate: now.AddDate(0, 0, -1)}
	after := drift.Snapshot{PassingControlIDs: nil, SnapshotDate: now}
	ev := driftalerts.Evaluation{
		BeforeDrift: &before,
		AfterDrift:  after,
		BeforeFreshness: []freshness.ControlFreshness{
			{ControlID: controlA, IsStale: false},
		},
		AfterFreshness: []freshness.ControlFreshness{
			{ControlID: controlA, IsStale: true, ValidUntil: &validUntil, FreshnessClass: "daily"},
		},
	}
	rep, err := store.AlertTenant(ctxA, ev)
	if err != nil {
		t.Fatalf("AlertTenant A: %v", err)
	}
	if rep.DriftWritten != 1 || rep.StalenessWritten != 1 {
		t.Fatalf("first alert report = %+v, want one drift and one staleness alert", rep)
	}
	rep, err = store.AlertTenant(ctxA, ev)
	if err != nil {
		t.Fatalf("AlertTenant A duplicate: %v", err)
	}
	if rep.DriftWritten != 0 || rep.StalenessWritten != 0 {
		t.Fatalf("duplicate alert report = %+v, want no new alerts", rep)
	}
	if got := countNotifications(t, admin, tenantA); got != 2 {
		t.Fatalf("tenant A notifications = %d, want exactly 2", got)
	}
	if got := countNotifications(t, admin, tenantB); got != 0 {
		t.Fatalf("tenant B notifications before own event = %d, want 0", got)
	}

	ctxB := dbtest.WithTenantCtx(t, tenantB)
	beforeB := drift.Snapshot{PassingControlIDs: []uuid.UUID{controlB}, SnapshotDate: now.AddDate(0, 0, -1)}
	if _, err := store.AlertTenant(ctxB, driftalerts.Evaluation{BeforeDrift: &beforeB, AfterDrift: after}); err != nil {
		t.Fatalf("AlertTenant B: %v", err)
	}

	slackSink := &fakePost{}
	pagerSink := &fakePost{}
	s := scheduler.New(admin, []scheduler.Channel{
		scheduler.SlackChannel(slack.NewChannel(app, slackSink, "https://atlas.example.test")),
		scheduler.WebhookChannel(webhook.NewChannel(app, pagerSink, "https://atlas.example.test")),
	}, nil)
	if _, err := s.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if len(slackSink.bodies) != 1 {
		t.Fatalf("slack sends = %d, want tenant A only", len(slackSink.bodies))
	}
	if len(pagerSink.bodies) != 2 {
		t.Fatalf("pagerduty/webhook sends = %d, want both tenants", len(pagerSink.bodies))
	}
	_ = userA
}
