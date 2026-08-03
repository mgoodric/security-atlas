//go:build integration

package securityawareness_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/securityawareness"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"notifications",
		"security_training_phishing_results",
		"security_training_assignments",
		"security_training_campaigns",
		"security_training_courses",
		"security_training_people",
	)
}

func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

func strptr(v string) *string { return &v }
func boolptr(v bool) *bool    { return &v }

func TestAssignmentCompletionRollupReminderAndEvidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := securityawareness.NewStore(app)

	person1, err := store.UpsertPerson(ctx, securityawareness.UpsertPersonInput{
		Source:          securityawareness.PersonSourceManual,
		DisplayName:     "Alice Example",
		WorkEmail:       strptr("alice@example.test"),
		RecipientUserID: strptr("alice-user"),
		Active:          boolptr(true),
	})
	if err != nil {
		t.Fatalf("UpsertPerson manual: %v", err)
	}
	person2, err := store.UpsertPerson(ctx, securityawareness.UpsertPersonInput{
		Source:          securityawareness.PersonSourceHRIS,
		SourcePersonID:  strptr("rippling:worker-2"),
		DisplayName:     "Bob Example",
		WorkEmail:       strptr("bob@example.test"),
		RecipientUserID: strptr("bob-user"),
		Active:          boolptr(true),
	})
	if err != nil {
		t.Fatalf("UpsertPerson hris: %v", err)
	}
	course, err := store.CreateCourse(ctx, securityawareness.CreateCourseInput{
		Code:     "SAT-2026",
		Title:    "Annual security awareness",
		Required: true,
	})
	if err != nil {
		t.Fatalf("CreateCourse: %v", err)
	}
	start := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	due := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	campaign, err := store.CreateCampaign(ctx, securityawareness.CreateCampaignInput{
		CourseID: course.ID,
		Name:     "2026 annual",
		StartsAt: start,
		DueAt:    due,
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	a1, err := store.Assign(ctx, securityawareness.AssignInput{CampaignID: campaign.ID, PersonID: person1.ID})
	if err != nil {
		t.Fatalf("Assign alice: %v", err)
	}
	if _, err := store.Assign(ctx, securityawareness.AssignInput{CampaignID: campaign.ID, PersonID: person2.ID}); err != nil {
		t.Fatalf("Assign bob: %v", err)
	}
	if _, err := store.RecordPhishing(ctx, securityawareness.PhishingInput{
		AssignmentID: a1.ID,
		SimulationID: "sim-2026-07",
		SentAt:       start,
		Outcome:      securityawareness.PhishingReported,
		ReportedAt:   &start,
	}); err != nil {
		t.Fatalf("RecordPhishing: %v", err)
	}
	completedAt := time.Date(2026, 7, 10, 9, 0, 0, 0, time.UTC)
	completed, err := store.Complete(ctx, securityawareness.CompleteInput{
		AssignmentID: a1.ID,
		CompletedAt:  completedAt,
		Source:       securityawareness.CompletionSourceManual,
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rec, err := securityawareness.BuildCompletionEvidence(completed, "alice-user")
	if err != nil {
		t.Fatalf("BuildCompletionEvidence: %v", err)
	}
	if rec.GetEvidenceKind() != securityawareness.EvidenceKindTrainingCompletion {
		t.Fatalf("evidence kind = %q", rec.GetEvidenceKind())
	}

	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	rollup, err := store.Rollup(ctx, campaign.ID, asOf)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if rollup.Assigned != 2 || rollup.Completed != 1 || rollup.Overdue != 1 {
		t.Fatalf("rollup mismatch: %+v", rollup)
	}
	if rollup.CompletionRate == nil || *rollup.CompletionRate != 0.5 {
		t.Fatalf("completion rate = %v, want 0.5", rollup.CompletionRate)
	}
	if len(rollup.OverdueList) != 1 || rollup.OverdueList[0].PersonDisplayName != "Bob Example" {
		t.Fatalf("overdue list mismatch: %+v", rollup.OverdueList)
	}
	n, err := store.EmitOverdueNotifications(ctx, asOf)
	if err != nil {
		t.Fatalf("EmitOverdueNotifications: %v", err)
	}
	if n != 1 {
		t.Fatalf("notifications emitted = %d, want 1", n)
	}
	var noteCount int
	if err := admin.QueryRow(context.Background(), `SELECT COUNT(*) FROM notifications WHERE tenant_id = $1 AND type = $2`, tenant, securityawareness.NotificationTypeTrainingOverdue).Scan(&noteCount); err != nil {
		t.Fatalf("count notifications: %v", err)
	}
	if noteCount != 1 {
		t.Fatalf("notification rows = %d, want 1", noteCount)
	}
	events, err := store.CalendarEvents(ctx, start, asOf, asOf)
	if err != nil {
		t.Fatalf("CalendarEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("calendar events = %d, want 2", len(events))
	}
}

// rosterTenant seeds a tenant whose cleanup also covers the roster-sync
// source tables (evidence_records, users).
func rosterTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"notifications",
		"security_training_phishing_results",
		"security_training_assignments",
		"security_training_campaigns",
		"security_training_courses",
		"security_training_people",
		"evidence_records",
		"users",
	)
}

// seedWorkerLifecycle inserts one hris.worker_lifecycle.v1 ledger record the
// way the push endpoint would land it (control_id NULL + free-form
// control_ref, per slice 013).
func seedWorkerLifecycle(t *testing.T, admin *pgxpool.Pool, tenant, vendor, workerID, status, email string, observedAt time.Time) {
	t.Helper()
	payload := map[string]string{
		"source_hris":       vendor,
		"worker_id":         workerID,
		"employment_status": status,
	}
	if email != "" {
		payload["work_email"] = email
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal lifecycle payload: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
INSERT INTO evidence_records
    (id, tenant_id, control_id, observed_at, provenance, result, payload, hash,
     evidence_kind, schema_version, control_ref, ingestion_path)
VALUES ($1, $2, NULL, $3, '{}'::jsonb, 'inconclusive', $4, $5, $6, '1.0.0', 'scf:IAC-22', 'push')`,
		uuid.New(), tenant, observedAt, body, "test-hash-"+uuid.NewString(),
		securityawareness.EvidenceKindHRISWorkerLifecycle); err != nil {
		t.Fatalf("seed worker lifecycle record: %v", err)
	}
}

// seedSCIMUser inserts a SCIM-managed platform user and returns its id.
func seedSCIMUser(t *testing.T, admin *pgxpool.Pool, tenant, email, displayName string, active bool) string {
	t.Helper()
	id := uuid.New()
	status := "active"
	if !active {
		status = "disabled"
	}
	if _, err := admin.Exec(context.Background(), `
INSERT INTO users (id, tenant_id, email, display_name, status, active, scim_managed)
VALUES ($1, $2, $3, $4, $5, $6, true)`,
		id, tenant, email, displayName, status, active); err != nil {
		t.Fatalf("seed scim user: %v", err)
	}
	return id.String()
}

// loadPeople reads a tenant's security_training_people keyed by
// (source, source_person_id), via the admin pool.
func loadPeople(t *testing.T, admin *pgxpool.Pool, tenant string) map[string]securityawareness.Person {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
SELECT id, source, source_person_id, display_name, work_email, recipient_user_id, active
FROM security_training_people WHERE tenant_id = $1`, tenant)
	if err != nil {
		t.Fatalf("load people: %v", err)
	}
	defer rows.Close()
	out := map[string]securityawareness.Person{}
	for rows.Next() {
		var p securityawareness.Person
		if err := rows.Scan(&p.ID, &p.Source, &p.SourcePersonID, &p.DisplayName, &p.WorkEmail, &p.RecipientUserID, &p.Active); err != nil {
			t.Fatalf("scan person: %v", err)
		}
		key := p.Source
		if p.SourcePersonID != nil {
			key += "|" + *p.SourcePersonID
		}
		out[key] = p
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("load people rows: %v", err)
	}
	return out
}

func TestRosterSyncUpsertsDeactivatesAndIsIdempotent(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := rosterTenant(t, admin)
	ctx := tenantCtx(t, tenant)
	store := securityawareness.NewStore(app)
	syncer := securityawareness.NewRosterSyncer(admin, app, nil)

	day1 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	seedWorkerLifecycle(t, admin, tenant, "rippling", "w-1", "active", "Alice.HRIS@example.test", day1)
	seedWorkerLifecycle(t, admin, tenant, "bamboohr", "42", "active", "bob.hris@example.test", day1)
	scimUserID := seedSCIMUser(t, admin, tenant, "carol@example.test", "Carol Example", true)

	if _, err := syncer.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	people := loadPeople(t, admin, tenant)
	if len(people) != 3 {
		t.Fatalf("people rows = %d, want 3: %+v", len(people), people)
	}
	alice, ok := people["hris|rippling:w-1"]
	if !ok {
		t.Fatalf("missing hris person rippling:w-1: %+v", people)
	}
	if alice.WorkEmail == nil || *alice.WorkEmail != "alice.hris@example.test" {
		t.Fatalf("hris email not lowercased: %v", alice.WorkEmail)
	}
	if alice.Active == nil || !*alice.Active {
		t.Fatalf("hris person not active: %+v", alice)
	}
	if alice.RecipientUserID != nil {
		t.Fatalf("hris person unexpectedly linked to a user: %+v", alice)
	}
	carol, ok := people["scim|"+scimUserID]
	if !ok {
		t.Fatalf("missing scim person %s: %+v", scimUserID, people)
	}
	if carol.RecipientUserID == nil || *carol.RecipientUserID != scimUserID {
		t.Fatalf("scim person recipient = %v, want %s", carol.RecipientUserID, scimUserID)
	}
	if carol.DisplayName != "Carol Example" {
		t.Fatalf("scim display name = %q", carol.DisplayName)
	}

	// An operator links the bamboohr person to a platform user; the next
	// sweep must carry the link forward, not null it out.
	if _, err := admin.Exec(context.Background(), `
UPDATE security_training_people SET recipient_user_id = 'linked-operator'
WHERE tenant_id = $1 AND source = 'hris' AND source_person_id = 'bamboohr:42'`, tenant); err != nil {
		t.Fatalf("link bamboohr person: %v", err)
	}

	// Overdue assignment for the SCIM person surfaces in reminders.
	course, err := store.CreateCourse(ctx, securityawareness.CreateCourseInput{Code: "SAT", Title: "Awareness", Required: true})
	if err != nil {
		t.Fatalf("CreateCourse: %v", err)
	}
	campaign, err := store.CreateCampaign(ctx, securityawareness.CreateCampaignInput{
		CourseID: course.ID,
		Name:     "2026",
		StartsAt: day1,
		DueAt:    time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if _, err := store.Assign(ctx, securityawareness.AssignInput{CampaignID: campaign.ID, PersonID: carol.ID}); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	reminders, err := store.OverdueReminders(ctx, asOf)
	if err != nil {
		t.Fatalf("OverdueReminders: %v", err)
	}
	if len(reminders) != 1 || reminders[0].PersonID != carol.ID {
		t.Fatalf("reminders before deactivation = %+v, want carol", reminders)
	}

	// Deactivate at the sources: a newer terminated lifecycle record for the
	// rippling worker, and the SCIM user flipped inactive (deprovision).
	seedWorkerLifecycle(t, admin, tenant, "rippling", "w-1", "terminated", "alice.hris@example.test", day1.Add(48*time.Hour))
	if _, err := admin.Exec(context.Background(), `
UPDATE users SET active = false, status = 'disabled' WHERE tenant_id = $1 AND id = $2`, tenant, scimUserID); err != nil {
		t.Fatalf("deactivate scim user: %v", err)
	}

	if _, err := syncer.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce after deactivation: %v", err)
	}
	people = loadPeople(t, admin, tenant)
	if p := people["hris|rippling:w-1"]; p.Active == nil || *p.Active {
		t.Fatalf("terminated worker still active: %+v", p)
	}
	if p := people["scim|"+scimUserID]; p.Active == nil || *p.Active {
		t.Fatalf("deprovisioned scim person still active: %+v", p)
	}
	if p := people["hris|bamboohr:42"]; p.RecipientUserID == nil || *p.RecipientUserID != "linked-operator" {
		t.Fatalf("operator link not carried forward: %+v", p)
	}
	reminders, err = store.OverdueReminders(ctx, asOf)
	if err != nil {
		t.Fatalf("OverdueReminders after deactivation: %v", err)
	}
	if len(reminders) != 0 {
		t.Fatalf("deactivated person still in reminders: %+v", reminders)
	}

	// Idempotency: a third sweep is a no-op diff — same rows, same ids.
	before := loadPeople(t, admin, tenant)
	if _, err := syncer.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce idempotent re-run: %v", err)
	}
	after := loadPeople(t, admin, tenant)
	if len(after) != len(before) {
		t.Fatalf("re-sync changed row count: %d -> %d", len(before), len(after))
	}
	for key, was := range before {
		now, ok := after[key]
		if !ok {
			t.Fatalf("re-sync dropped person %s", key)
		}
		if now.ID != was.ID || now.DisplayName != was.DisplayName ||
			(now.Active == nil) != (was.Active == nil) || (now.Active != nil && *now.Active != *was.Active) {
			t.Fatalf("re-sync mutated person %s: %+v -> %+v", key, was, now)
		}
	}
}

func TestRosterSync_TwoTenantIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := rosterTenant(t, admin)
	tenantB := rosterTenant(t, admin)

	day := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	seedWorkerLifecycle(t, admin, tenantA, "rippling", "wa-1", "active", "a1@tenant-a.test", day)
	seedSCIMUser(t, admin, tenantA, "a2@tenant-a.test", "Tenant A User", true)

	syncer := securityawareness.NewRosterSyncer(admin, app, nil)
	if _, err := syncer.SweepOnce(context.Background()); err != nil {
		t.Fatalf("SweepOnce: %v", err)
	}

	if got := len(loadPeople(t, admin, tenantA)); got != 2 {
		t.Fatalf("tenant A people = %d, want 2", got)
	}
	if got := len(loadPeople(t, admin, tenantB)); got != 0 {
		t.Fatalf("tenant B gained people from tenant A sources: %d rows", got)
	}

	// RLS view: under tenant B's GUC the app role sees none of tenant A's
	// synced roster.
	countUnderTenant := func(tenant string) int {
		ctx := tenantCtx(t, tenant)
		tx, err := app.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := tenancy.ApplyTenant(ctx, tx); err != nil {
			t.Fatalf("apply tenant: %v", err)
		}
		var n int
		if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM security_training_people`).Scan(&n); err != nil {
			t.Fatalf("count under tenant: %v", err)
		}
		return n
	}
	if n := countUnderTenant(tenantB); n != 0 {
		t.Fatalf("tenant B sees %d synced people via RLS, want 0", n)
	}
	if n := countUnderTenant(tenantA); n != 2 {
		t.Fatalf("tenant A sees %d synced people via RLS, want 2", n)
	}
}

func TestRLSIsolationAcrossTenants(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := securityawareness.NewStore(app)

	ctxA := tenantCtx(t, tenantA)
	ctxB := tenantCtx(t, tenantB)
	personA, err := store.UpsertPerson(ctxA, securityawareness.UpsertPersonInput{
		Source:      securityawareness.PersonSourceManual,
		DisplayName: "Tenant A Person",
		Active:      boolptr(true),
	})
	if err != nil {
		t.Fatalf("seed tenant A person: %v", err)
	}
	courseA, err := store.CreateCourse(ctxA, securityawareness.CreateCourseInput{Code: "A", Title: "A", Required: true})
	if err != nil {
		t.Fatalf("seed tenant A course: %v", err)
	}
	campaignA, err := store.CreateCampaign(ctxA, securityawareness.CreateCampaignInput{
		CourseID: courseA.ID,
		Name:     "A",
		StartsAt: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		DueAt:    time.Date(2026, 7, 2, 0, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("seed tenant A campaign: %v", err)
	}
	if _, err := store.Assign(ctxA, securityawareness.AssignInput{CampaignID: campaignA.ID, PersonID: personA.ID}); err != nil {
		t.Fatalf("seed tenant A assignment: %v", err)
	}

	rollupB, err := store.Rollup(ctxB, campaignA.ID, time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("tenant B Rollup: %v", err)
	}
	if rollupB.Assigned != 0 || len(rollupB.OverdueList) != 0 {
		t.Fatalf("cross-tenant leakage in rollup: %+v", rollupB)
	}
	eventsB, err := store.CalendarEvents(ctxB, time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("tenant B CalendarEvents: %v", err)
	}
	if len(eventsB) != 0 {
		t.Fatalf("cross-tenant calendar leakage: %+v", eventsB)
	}
}
