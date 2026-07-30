//go:build integration

package accessreview

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
)

func TestCampaignFlowEvidenceReminderAndRLS(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := dbtest.SeedTenant(t, admin,
		"notifications",
		"access_review_items",
		"access_review_reviewer_assignments",
		"access_review_campaigns",
		"evidence_records",
		"scim_group_members",
		"scim_groups",
		"users",
	)
	tenantB := dbtest.SeedTenant(t, admin,
		"notifications",
		"access_review_items",
		"access_review_reviewer_assignments",
		"access_review_campaigns",
		"evidence_records",
		"scim_group_members",
		"scim_groups",
		"users",
	)

	userA1 := seedUser(t, admin, tenantA, "alice@example.test")
	userA2 := seedUser(t, admin, tenantA, "bob@example.test")
	groupA := seedGroup(t, admin, tenantA, "Production Admins", "prod-admins")
	seedMember(t, admin, tenantA, groupA, userA1, "prod-admins")
	seedUser(t, admin, tenantB, "mallory@example.test")

	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	store := NewStore(app).WithClock(func() time.Time { return now })
	ctxA := dbtest.WithTenantCtx(t, tenantA)

	campaign, items, err := store.CreateCampaign(ctxA, CreateInput{
		Name:      "Q3 access review",
		Source:    SourceSCIM,
		Scope:     Scope{Systems: []string{"security-atlas"}},
		DueAt:     now.Add(-time.Hour),
		Reviewers: []string{"reviewer-a", "reviewer-b"},
		CreatedBy: "owner",
	})
	if err != nil {
		t.Fatalf("CreateCampaign: %v", err)
	}
	if campaign.Status != StatusActive {
		t.Fatalf("campaign status = %s, want active", campaign.Status)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2 (one group entitlement + one user fallback)", len(items))
	}

	var revokeItem, keepItem Item
	for _, item := range items {
		if item.PrincipalUserID == userA1.String() {
			revokeItem = item
		} else if item.PrincipalUserID == userA2.String() {
			keepItem = item
		}
	}
	if revokeItem.ID == uuid.Nil || keepItem.ID == uuid.Nil {
		t.Fatalf("did not find expected review items: %+v", items)
	}

	if _, err := store.Attest(ctxA, AttestInput{ItemID: revokeItem.ID, ReviewerID: revokeItem.ReviewerID, Decision: DecisionRevoke, Reason: "No longer owns production"}); err != nil {
		t.Fatalf("Attest revoke: %v", err)
	}
	if _, err := store.Attest(ctxA, AttestInput{ItemID: keepItem.ID, ReviewerID: keepItem.ReviewerID, Decision: DecisionKeep, Reason: "Baseline account still required"}); err != nil {
		t.Fatalf("Attest keep: %v", err)
	}

	revokes, err := store.RevokeList(ctxA, campaign.ID)
	if err != nil {
		t.Fatalf("RevokeList: %v", err)
	}
	if len(revokes) != 1 {
		t.Fatalf("len(revokes) = %d, want 1", len(revokes))
	}
	if revokes[0].PrincipalUserID != userA1.String() || revokes[0].Entitlement != "Production Admins" {
		t.Fatalf("unexpected revoke decision: %+v", revokes[0])
	}

	rollup, err := store.Complete(ctxA, campaign.ID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if rollup.PendingItems != 0 || rollup.RevokeDecisions != 1 || rollup.KeepDecisions != 1 || rollup.EvidenceRecordID == nil {
		t.Fatalf("bad rollup: %+v", rollup)
	}
	assertEvidence(t, admin, tenantA, *rollup.EvidenceRecordID)

	created, err := store.FireDueReminders(ctxA, now)
	if err != nil {
		t.Fatalf("FireDueReminders completed campaign: %v", err)
	}
	if created != 0 {
		t.Fatalf("created reminders for completed campaign = %d, want 0", created)
	}

	pendingCampaign, _, err := store.CreateCampaign(ctxA, CreateInput{
		Name:      "Due campaign",
		Source:    SourceManualCSV,
		DueAt:     now.Add(-time.Minute),
		Reviewers: []string{"reviewer-c"},
		CreatedBy: "owner",
		ManualCSV: strings.NewReader("system,entitlement,user_id,email\ngithub,admin,u9,u9@example.test\n"),
	})
	if err != nil {
		t.Fatalf("CreateCampaign manual: %v", err)
	}
	created, err = store.FireDueReminders(ctxA, now)
	if err != nil {
		t.Fatalf("FireDueReminders: %v", err)
	}
	if created != 1 {
		t.Fatalf("created reminders = %d, want 1", created)
	}
	created, err = store.FireDueReminders(ctxA, now)
	if err != nil {
		t.Fatalf("FireDueReminders duplicate: %v", err)
	}
	if created != 0 {
		t.Fatalf("duplicate reminders = %d, want 0", created)
	}
	assertReminder(t, admin, tenantA, "reviewer-c", pendingCampaign.ID)

	ctxB := dbtest.WithTenantCtx(t, tenantB)
	_, err = store.Rollup(ctxB, campaign.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("tenant B Rollup err = %v, want ErrNotFound", err)
	}
	revokesB, err := store.RevokeList(ctxB, campaign.ID)
	if err != nil {
		t.Fatalf("tenant B RevokeList: %v", err)
	}
	if len(revokesB) != 0 {
		t.Fatalf("tenant B saw revoke decisions: %+v", revokesB)
	}
}

func seedUser(t *testing.T, pool *pgxpool.Pool, tenant string, email string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status, active)
		VALUES ($1, $2, $3, $4, 'active', true)
	`, id, tenant, email, email); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedGroup(t *testing.T, pool *pgxpool.Pool, tenant string, displayName, externalID string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO scim_groups (id, tenant_id, display_name, scim_external_id)
		VALUES ($1, $2, $3, $4)
	`, id, tenant, displayName, externalID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	return id
}

func seedMember(t *testing.T, pool *pgxpool.Pool, tenant string, groupID, userID uuid.UUID, groupRef string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO scim_group_members (id, tenant_id, group_id, user_id, group_ref)
		VALUES ($1, $2, $3, $4, $5)
	`, uuid.New(), tenant, groupID, userID.String(), groupRef); err != nil {
		t.Fatalf("seed member: %v", err)
	}
}

func assertEvidence(t *testing.T, pool *pgxpool.Pool, tenant string, evidenceID uuid.UUID) {
	t.Helper()
	var controlRef, kind string
	if err := pool.QueryRow(context.Background(), `
		SELECT control_ref, evidence_kind
		FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, evidenceID).Scan(&controlRef, &kind); err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if controlRef != CC6ControlRef || kind != EvidenceKind {
		t.Fatalf("evidence = (%s, %s), want (%s, %s)", controlRef, kind, CC6ControlRef, EvidenceKind)
	}
}

func assertReminder(t *testing.T, pool *pgxpool.Pool, tenant, reviewer string, campaignID uuid.UUID) {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(), `
		SELECT COUNT(*)
		FROM notifications
		WHERE tenant_id = $1
		  AND recipient_user_id = $2
		  AND type = $3
		  AND payload->>'campaign_id' = $4
	`, tenant, reviewer, ReminderNotificationType, campaignID.String()).Scan(&count); err != nil {
		t.Fatalf("query reminder: %v", err)
	}
	if count != 1 {
		t.Fatalf("reminder count = %d, want 1", count)
	}
}
