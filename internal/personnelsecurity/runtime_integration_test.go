//go:build integration

// OE-661 runtime-wiring integration tests.
//
// TestWorkerEventThroughIngestionPathCreatesChecklistOnce drives the REAL
// connector→platform path: a record built by connectors/hris/workerrecord
// (the exact bytes the Rippling cmd layer pushes), published through the
// slice-015 JetStream publisher onto a real NATS stream, consumed by the
// WorkerEventSubscriber's durable consumer, landing in Postgres through
// Store.HandleWorkerEvent under RLS. The replay half re-observes the SAME
// lifecycle fact an hour later (a fresh push idempotency key — the connector
// hour-truncates — but the same derived source event id) and asserts exactly
// one checklist survives.
//
// TestOverdueSweepNotifiesOnceAndIsolatesTenants runs the OverdueNotifier
// sweep twice over an overdue offboarding checklist: one notification per
// (checklist, active-user) after the first sweep, none added by the second,
// and a second tenant's user sees nothing (RLS + recipient enumeration under
// the per-tenant GUC).
//
// Requires NATS_URL (first test only) + the dbtest Postgres env; both are
// provided by the sharded `Go · integration` CI job (leg B2).

package personnelsecurity_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	"github.com/mgoodric/security-atlas/connectors/hris/workerrecord"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/streambuf"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
)

// freshTenantWithUsers is freshTenant plus users-table cleanup, for the
// sweep test's recipient seeding.
func freshTenantWithUsers(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, migrate,
		"notifications",
		"evidence_audit_log",
		"evidence_records",
		"personnel_security_checklist_items",
		"personnel_security_checklists",
		"controls",
		"users",
	)
}

func seedActiveUser(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(),
		`INSERT INTO users (id, tenant_id, email, display_name, status)
		 VALUES ($1, $2, $3, $4, 'active')`,
		id, tenant, "user-"+id.String()[:8]+"@example.test", "Test User"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// testLogger discards structured logs; the subscriber's own error paths are
// asserted through message outcomes and DB state, not log lines.
func testLogger(t *testing.T) *slog.Logger {
	t.Helper()
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func waitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

func TestWorkerEventThroughIngestionPathCreatesChecklistOnce(t *testing.T) {
	natsURL := os.Getenv("NATS_URL")
	if natsURL == "" {
		t.Skip("NATS_URL not set; skipping JetStream-backed integration test")
	}
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)
	controlID := seedAccessControl(t, migrate, tenant)

	// A unique stream per test run so concurrent CI legs and stale durables
	// never interfere.
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:10]
	streamName := "PS_OE661_" + strings.ToUpper(suffix)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := streambuf.Open(ctx, streambuf.Config{
		URL:        natsURL,
		StreamName: streamName,
		Subject:    "evidence.oe661." + suffix,
		Logger:     testLogger(t),
	})
	if err != nil {
		t.Fatalf("streambuf.Open: %v", err)
	}
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		_ = conn.JS().DeleteStream(cleanCtx, streamName)
		conn.Close()
	})

	store := personnelsecurity.NewStore(app)
	sub := personnelsecurity.NewWorkerEventSubscriber(conn.Stream(), conn.Cfg().Subject, store, testLogger(t))
	subCtx, subCancel := context.WithCancel(context.Background())
	t.Cleanup(subCancel)
	go func() {
		if err := sub.Start(subCtx); err != nil && subCtx.Err() == nil {
			t.Errorf("subscriber Start: %v", err)
		}
	}()

	pub := streambuf.NewJetStreamPublisher(conn)
	cred := credstore.Credential{ID: "cred-oe661", TenantID: tenant}
	observed := time.Now().UTC().Truncate(time.Hour)
	end := observed.Add(-48 * time.Hour)
	leaver := worker.Worker{
		SourceHRIS: worker.HRISRippling,
		WorkerID:   "rip-int-1",
		Status:     worker.StatusTerminated,
		EndDate:    end,
		WorkEmail:  "leaver@example.test",
		ObservedAt: observed,
	}
	rec, err := workerrecord.Build(leaver, controlID.String(), "connector:rippling:hris@test", "hris", "production")
	if err != nil {
		t.Fatalf("workerrecord.Build: %v", err)
	}
	if _, _, err := pub.Publish(ctx, rec, cred); err != nil {
		t.Fatalf("publish leaver: %v", err)
	}
	waitFor(t, 20*time.Second, "offboarding checklist", func() bool {
		return countChecklists(t, app, tenant) == 1
	})

	// Replay: the same lifecycle fact re-observed an hour later. The push
	// idempotency key changes (hour-truncated), so this is a genuinely new
	// stream message — the checklist dedup must come from the stable derived
	// source event id.
	replay := leaver
	replay.ObservedAt = observed.Add(time.Hour)
	rec2, err := workerrecord.Build(replay, controlID.String(), "connector:rippling:hris@test", "hris", "production")
	if err != nil {
		t.Fatalf("workerrecord.Build replay: %v", err)
	}
	if rec2.IdempotencyKey == rec.IdempotencyKey {
		t.Fatalf("replay did not change the push idempotency key; test would prove nothing")
	}
	if _, _, err := pub.Publish(ctx, rec2, cred); err != nil {
		t.Fatalf("publish replay: %v", err)
	}

	// Marker record: a distinct joiner whose checklist appearing proves the
	// ordered stream has processed the replay message before we assert.
	joiner := worker.Worker{
		SourceHRIS: worker.HRISRippling,
		WorkerID:   "rip-int-2",
		Status:     worker.StatusPending,
		StartDate:  observed.Add(7 * 24 * time.Hour),
		ObservedAt: observed,
	}
	rec3, err := workerrecord.Build(joiner, controlID.String(), "connector:rippling:hris@test", "hris", "production")
	if err != nil {
		t.Fatalf("workerrecord.Build joiner: %v", err)
	}
	if _, _, err := pub.Publish(ctx, rec3, cred); err != nil {
		t.Fatalf("publish joiner: %v", err)
	}
	waitFor(t, 20*time.Second, "joiner checklist (replay processed)", func() bool {
		return countChecklists(t, app, tenant) >= 2
	})
	if got := countChecklists(t, app, tenant); got != 2 {
		t.Fatalf("checklists = %d, want 2 (replay must not create a third)", got)
	}

	// The offboarding checklist landed under the stable event id, with the
	// connector's control bound and the due date derived from the leaver fact.
	withTenantTx(t, app, tenant, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) {
		eventID := "rip-int-1|offboarding|" + end.Format("2006-01-02")
		row, err := q.GetPersonnelChecklistBySourceEvent(ctx, dbx.GetPersonnelChecklistBySourceEventParams{
			TenantID:      pgUUID(tenantID),
			Source:        "rippling",
			SourceEventID: &eventID,
		})
		if err != nil {
			t.Fatalf("GetPersonnelChecklistBySourceEvent(%q): %v", eventID, err)
		}
		if row.WorkflowKind != "offboarding" || row.PersonExternalID != "rip-int-1" {
			t.Fatalf("checklist = %s/%s", row.WorkflowKind, row.PersonExternalID)
		}
		if got := uuid.UUID(row.ControlID.Bytes); got != controlID {
			t.Fatalf("control = %s, want %s", got, controlID)
		}
		// The hris.worker_lifecycle.v1 wire schema carries end_date at DAY
		// granularity, so the platform-side leaver fact is midnight UTC of
		// the termination date and the offboarding due date is 24h after
		// that.
		endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
		if want := endDay.Add(24 * time.Hour); !row.DueAt.Time.UTC().Equal(want) {
			t.Fatalf("due_at = %s, want %s", row.DueAt.Time.UTC(), want)
		}
	})
}

func TestOverdueSweepNotifiesOnceAndIsolatesTenants(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenantA := freshTenantWithUsers(t, migrate)
	tenantB := freshTenantWithUsers(t, migrate)
	controlID := seedAccessControl(t, migrate, tenantA)
	userA := seedActiveUser(t, migrate, tenantA)
	userB := seedActiveUser(t, migrate, tenantB)

	now := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	store := personnelsecurity.NewStore(app).WithClock(func() time.Time { return now })

	w := worker.Worker{
		SourceHRIS: worker.HRISBambooHR,
		WorkerID:   "bhr-sweep-1",
		Status:     worker.StatusTerminated,
		EndDate:    now.Add(-48 * time.Hour),
		WorkEmail:  "sweep-leaver@example.test",
	}
	checklist, err := store.HandleWorkerEvent(dbtest.WithTenantCtx(t, tenantA), w, "evt-sweep-1", controlID)
	if err != nil {
		t.Fatalf("HandleWorkerEvent: %v", err)
	}
	if checklist.DueAt.After(now) {
		t.Fatalf("checklist due %s is not overdue at %s", checklist.DueAt, now)
	}

	notifier := personnelsecurity.NewOverdueNotifier(migrate, store, nil)
	rep, err := notifier.SweepOnce(context.Background())
	if err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}
	if rep.Tenants < 1 {
		t.Fatalf("sweep report = %+v, want at least tenant A swept", rep)
	}
	if got := countNotifications(t, app, tenantA, userA.String()); got != 1 {
		t.Fatalf("tenant A notifications after first sweep = %d, want 1", got)
	}
	if got := countNotifications(t, app, tenantB, userB.String()); got != 0 {
		t.Fatalf("tenant B user received %d notifications from tenant A's checklist", got)
	}

	// Re-run: the checklist is still open + overdue, but the notification row
	// is the dedup marker — no double notification.
	if _, err := notifier.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce re-run: %v", err)
	}
	if got := countNotifications(t, app, tenantA, userA.String()); got != 1 {
		t.Fatalf("tenant A notifications after re-run = %d, want 1 (double-notified)", got)
	}
}
