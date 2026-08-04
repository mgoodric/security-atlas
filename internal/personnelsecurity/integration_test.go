//go:build integration

package personnelsecurity_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/connectors/hris/worker"
	apicalendar "github.com/mgoodric/security-atlas/internal/api/calendar"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/personnelsecurity"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func freshTenant(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, migrate,
		"notifications",
		"evidence_audit_log",
		"evidence_records",
		"personnel_security_checklist_items",
		"personnel_security_checklists",
		"controls",
	)
}

func seedAccessControl(t *testing.T, migrate *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := migrate.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, scf_id, title, control_family,
			implementation_type, lifecycle_state, bundle_id
		) VALUES (
			$1, $2, 'CC1-PS', 'Personnel security access control', 'CC1',
			'manual_attested', 'active', $3
		)
	`, id, tenant, "personnel-security-"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func TestManualChecklistLifecycleGeneratesEvidence(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)
	controlID := seedAccessControl(t, migrate, tenant)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := personnelsecurity.NewStore(app).WithClock(func() time.Time { return now })

	checklist, err := store.CreateManual(dbtest.WithTenantCtx(t, tenant), personnelsecurity.ManualInput{
		Kind:             personnelsecurity.WorkflowOnboarding,
		PersonExternalID: "worker-123",
		PersonWorkEmail:  "newhire@example.com",
		ControlID:        controlID,
		CreatedBy:        "security-admin",
	})
	if err != nil {
		t.Fatalf("CreateManual: %v", err)
	}
	if checklist.Kind != personnelsecurity.WorkflowOnboarding || len(checklist.Items) != 3 {
		t.Fatalf("checklist kind/items = %q/%d", checklist.Kind, len(checklist.Items))
	}

	completed, err := store.CompleteItem(dbtest.WithTenantCtx(t, tenant), personnelsecurity.Completion{
		ItemID:      checklist.Items[1].ID,
		CompletedBy: "security-admin",
		EvidenceURI: "atlas://evidence/access-grant/worker-123",
		Notes:       "Access grant ticket approved and applied.",
		ObservedAt:  now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("CompleteItem: %v", err)
	}
	if completed.EvidenceRecordID == uuid.Nil {
		t.Fatalf("completed item did not attach evidence")
	}

	ev := loadEvidence(t, app, tenant, completed.EvidenceRecordID)
	if ev.EvidenceKind == nil || *ev.EvidenceKind != personnelsecurity.EvidenceKind {
		t.Fatalf("evidence kind = %v", ev.EvidenceKind)
	}
	if got := uuid.UUID(ev.ControlID.Bytes); got != controlID {
		t.Fatalf("evidence control = %s, want %s", got, controlID)
	}
	var payload map[string]any
	if err := json.Unmarshal(ev.Payload, &payload); err != nil {
		t.Fatalf("payload JSON: %v", err)
	}
	if payload["workflow_kind"] != "onboarding" || payload["item_slug"] != "provision_access" {
		t.Fatalf("unexpected evidence payload: %#v", payload)
	}
}

func TestHRISLeaverTriggerOverdueSurfacingAndCalendar(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, migrate)
	controlID := seedAccessControl(t, migrate, tenant)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := personnelsecurity.NewStore(app).WithClock(func() time.Time { return now })

	w := worker.Worker{
		SourceHRIS: worker.HRISRippling,
		WorkerID:   "rippling-worker-7",
		Status:     worker.StatusTerminated,
		EndDate:    now.Add(-48 * time.Hour),
		WorkEmail:  "leaver@example.com",
	}
	checklist, err := store.HandleWorkerEvent(dbtest.WithTenantCtx(t, tenant), w, "evt-7", controlID)
	if err != nil {
		t.Fatalf("HandleWorkerEvent: %v", err)
	}
	if checklist.Kind != personnelsecurity.WorkflowOffboarding || checklist.Source != personnelsecurity.SourceRippling {
		t.Fatalf("checklist = %q/%q", checklist.Kind, checklist.Source)
	}
	if checklist.DueAt.After(now) || len(checklist.Items) != 4 {
		t.Fatalf("offboarding due/items = %s/%d", checklist.DueAt, len(checklist.Items))
	}
	again, err := store.HandleWorkerEvent(dbtest.WithTenantCtx(t, tenant), w, "evt-7", controlID)
	if err != nil {
		t.Fatalf("HandleWorkerEvent duplicate: %v", err)
	}
	if again.ID != checklist.ID {
		t.Fatalf("duplicate event created checklist %s; want %s", again.ID, checklist.ID)
	}

	overdue, err := store.SurfaceOverdueOffboarding(dbtest.WithTenantCtx(t, tenant), now, "security-owner")
	if err != nil {
		t.Fatalf("SurfaceOverdueOffboarding: %v", err)
	}
	if len(overdue) != 1 || overdue[0].ID != checklist.ID {
		t.Fatalf("overdue = %#v", overdue)
	}
	if got := countNotifications(t, app, tenant, "security-owner"); got != 1 {
		t.Fatalf("notifications = %d, want 1", got)
	}

	rows, err := apicalendar.NewStore(app).ListEvents(dbtest.WithTenantCtx(t, tenant), now.Add(-24*time.Hour), now.Add(24*time.Hour), now, "", 20)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	found := false
	for _, row := range rows {
		if row.EventType == "offboarding" && row.Status == "overdue" && row.RelatedEntityID == checklist.ID.String() {
			found = true
		}
	}
	if !found {
		t.Fatalf("calendar did not surface overdue offboarding event: %#v", rows)
	}
}

func TestRLSIsolation(t *testing.T) {
	app := dbtest.NewAppPool(t)
	migrate := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, migrate)
	tenantB := freshTenant(t, migrate)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := personnelsecurity.NewStore(app).WithClock(func() time.Time { return now })

	_, err := store.CreateManual(dbtest.WithTenantCtx(t, tenantA), personnelsecurity.ManualInput{
		Kind:             personnelsecurity.WorkflowOffboarding,
		PersonExternalID: "tenant-a-worker",
		DueAt:            now.Add(-time.Hour),
		CreatedBy:        "security-admin",
	})
	if err != nil {
		t.Fatalf("CreateManual tenant A: %v", err)
	}
	if got := countChecklists(t, app, tenantA); got != 1 {
		t.Fatalf("tenant A checklists = %d, want 1", got)
	}
	if got := countChecklists(t, app, tenantB); got != 0 {
		t.Fatalf("tenant B saw %d tenant A checklists", got)
	}
	overdue, err := store.ListOverdueOffboarding(dbtest.WithTenantCtx(t, tenantB), now)
	if err != nil {
		t.Fatalf("ListOverdueOffboarding tenant B: %v", err)
	}
	if len(overdue) != 0 {
		t.Fatalf("tenant B overdue = %#v, want none", overdue)
	}
}

func loadEvidence(t *testing.T, app *pgxpool.Pool, tenant string, id uuid.UUID) dbx.EvidenceRecord {
	t.Helper()
	var out dbx.EvidenceRecord
	withTenantTx(t, app, tenant, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) {
		row, err := q.GetEvidenceRecordByID(ctx, dbx.GetEvidenceRecordByIDParams{
			ID:       pgUUID(id),
			TenantID: pgUUID(tenantID),
		})
		if err != nil {
			t.Fatalf("GetEvidenceRecordByID: %v", err)
		}
		out = row
	})
	return out
}

func countNotifications(t *testing.T, app *pgxpool.Pool, tenant, userID string) int64 {
	t.Helper()
	var out int64
	withTenantTx(t, app, tenant, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) {
		count, err := q.CountUnreadNotificationsForUser(ctx, dbx.CountUnreadNotificationsForUserParams{
			TenantID:        pgUUID(tenantID),
			RecipientUserID: userID,
		})
		if err != nil {
			t.Fatalf("CountUnreadNotificationsForUser: %v", err)
		}
		out = count
	})
	return out
}

func countChecklists(t *testing.T, app *pgxpool.Pool, tenant string) int64 {
	t.Helper()
	var out int64
	withTenantTx(t, app, tenant, func(ctx context.Context, q *dbx.Queries, tenantID uuid.UUID) {
		count, err := q.CountPersonnelChecklistsForTenant(ctx, pgUUID(tenantID))
		if err != nil {
			t.Fatalf("CountPersonnelChecklistsForTenant: %v", err)
		}
		out = count
	})
	return out
}

func withTenantTx(t *testing.T, app *pgxpool.Pool, tenant string, fn func(context.Context, *dbx.Queries, uuid.UUID)) {
	t.Helper()
	ctx := dbtest.WithTenantCtx(t, tenant)
	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	tenantID := uuid.MustParse(tenant)
	fn(ctx, dbx.New(tx), tenantID)
	if err := tx.Commit(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatalf("Commit: %v", err)
	}
}

func pgUUID(u uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: u, Valid: true}
}
