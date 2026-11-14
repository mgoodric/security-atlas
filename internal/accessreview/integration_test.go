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

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
)

func TestCampaignFlowEvidenceReminderAndRLS(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := dbtest.SeedTenant(t, admin,
		"notifications",
		"access_review_items",
		"access_review_reviewer_assignments",
		"access_review_campaigns",
		"evidence_audit_log",
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
		"evidence_audit_log",
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
	store := newTestStore(t, admin, app, now, now)
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
	var controlRef, kind, schemaVersion string
	var payload map[string]any
	if err := pool.QueryRow(context.Background(), `
		SELECT control_ref, evidence_kind, schema_version, payload
		FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, evidenceID).Scan(&controlRef, &kind, &schemaVersion, &payload); err != nil {
		t.Fatalf("query evidence: %v", err)
	}
	if controlRef != CC6ControlRef || kind != EvidenceKind || schemaVersion != EvidenceSchemaVersion {
		t.Fatalf("evidence = (%s, %s, %s), want (%s, %s, %s)",
			controlRef, kind, schemaVersion, CC6ControlRef, EvidenceKind, EvidenceSchemaVersion)
	}
	for _, field := range []string{"review_id", "completed_by", "reviewer_role", "users_reviewed"} {
		if _, ok := payload[field]; !ok {
			t.Fatalf("evidence payload missing required schema field %q: %v", field, payload)
		}
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

// newTestStore builds the production wiring: an access-review store
// writing completion evidence through an ingest.Service over the app
// role, validated by the DB-backed schema registry seeded with the
// bundled platform schemas. storeNow is the store's clock (the evidence
// observed_at); ingestNow is the ingest stage's received_at clock.
func newTestStore(t *testing.T, admin, app *pgxpool.Pool, storeNow, ingestNow time.Time) *Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reg := schemaregistry.NewService(admin)
	if _, _, err := reg.ImportPlatformSchemas(ctx, schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}
	svc := ingest.New(app, reg).WithClock(func() time.Time { return ingestNow })
	return NewStore(app).WithClock(func() time.Time { return storeNow }).WithIngest(svc)
}

// #1830: completion evidence goes through ingest.Service.ProcessInTx. It
// keeps the quarterly freshness and 400-day valid_until the raw insert
// set, and its acceptance is audited. A completion whose observed_at is
// outside the skew window is refused with ErrObservedAtSkew, the campaign
// stays active, no ledger row lands, and the refusal is audited.
func TestCompleteEvidenceThroughIngest(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := dbtest.SeedTenant(t, admin,
		"notifications",
		"access_review_items",
		"access_review_reviewer_assignments",
		"access_review_campaigns",
		"evidence_audit_log",
		"evidence_records",
		"users",
	)
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	ctx := dbtest.WithTenantCtx(t, tenant)

	attestAll := func(store *Store, name string) uuid.UUID {
		t.Helper()
		campaign, items, err := store.CreateCampaign(ctx, CreateInput{
			Name:      name,
			Source:    SourceManualCSV,
			DueAt:     now.Add(24 * time.Hour),
			Reviewers: []string{"reviewer-a"},
			CreatedBy: "owner",
			ManualCSV: strings.NewReader("system,entitlement,user_id,email\ngithub,admin,u1,u1@example.test\n"),
		})
		if err != nil {
			t.Fatalf("CreateCampaign: %v", err)
		}
		for _, item := range items {
			if _, err := store.Attest(ctx, AttestInput{ItemID: item.ID, ReviewerID: item.ReviewerID, Decision: DecisionKeep, Reason: "still required"}); err != nil {
				t.Fatalf("Attest: %v", err)
			}
		}
		return campaign.ID
	}
	decisionFor := func(campaignID uuid.UUID) (string, string) {
		t.Helper()
		var decision, credential string
		if err := admin.QueryRow(context.Background(), `
			SELECT decision, credential_id FROM evidence_audit_log
			WHERE tenant_id = $1 AND idempotency_key = $2
		`, tenant, "access-review:"+campaignID.String()+":completion").Scan(&decision, &credential); err != nil {
			t.Fatalf("fetch audit row: %v", err)
		}
		return decision, credential
	}

	// Accepted: evidence keeps its freshness window and is audited.
	store := newTestStore(t, admin, app, now, now)
	okID := attestAll(store, "accepted campaign")
	rollup, err := store.Complete(ctx, okID)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	var freshness string
	var validUntil time.Time
	if err := admin.QueryRow(context.Background(), `
		SELECT freshness_class::text, valid_until FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, *rollup.EvidenceRecordID).Scan(&freshness, &validUntil); err != nil {
		t.Fatalf("fetch evidence: %v", err)
	}
	if freshness != "quarterly" || !validUntil.Equal(now.Add(400*24*time.Hour)) {
		t.Fatalf("freshness/valid_until = %s/%s, want quarterly/%s", freshness, validUntil, now.Add(400*24*time.Hour))
	}
	if d, c := decisionFor(okID); d != "accepted" || c != EvidenceCredentialID {
		t.Fatalf("audit row = %s/%s, want accepted/%s", d, c, EvidenceCredentialID)
	}

	// Refused: the store's clock is two days behind the ingest clock, so
	// the completion evidence is backdated past the skew window.
	backdated := newTestStore(t, admin, app, now.Add(-ingest.MaxObservedAtSkew-time.Hour), now)
	badID := attestAll(backdated, "backdated campaign")
	if _, err := backdated.Complete(ctx, badID); !errors.Is(err, ingest.ErrObservedAtSkew) {
		t.Fatalf("backdated Complete: got %v, want ErrObservedAtSkew", err)
	}
	var status string
	var evidenceID *uuid.UUID
	if err := admin.QueryRow(context.Background(), `
		SELECT status, evidence_record_id FROM access_review_campaigns
		WHERE tenant_id = $1 AND id = $2
	`, tenant, badID).Scan(&status, &evidenceID); err != nil {
		t.Fatalf("fetch campaign: %v", err)
	}
	if status != StatusActive || evidenceID != nil {
		t.Fatalf("campaign after refusal = %s/%v, want active/nil", status, evidenceID)
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if n != 1 {
		t.Fatalf("evidence rows = %d, want 1 (the accepted campaign only)", n)
	}
	if d, _ := decisionFor(badID); d != "rejected_observed_at_skew" {
		t.Fatalf("audit decision = %s, want rejected_observed_at_skew", d)
	}
}
