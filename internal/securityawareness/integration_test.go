//go:build integration

package securityawareness_test

import (
	"context"
	"testing"
	"time"

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
