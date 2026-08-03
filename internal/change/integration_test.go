//go:build integration

package change_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/change"
	"github.com/mgoodric/security-atlas/internal/dbtest"
)

var cleanupTables = []string{
	"change_audit_log",
	"change_controls",
	"changes",
	"evidence_audit_log",
	"evidence_records",
	"controls",
	"users",
}

func seedTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin, cleanupTables...)
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	return dbtest.WithTenantCtx(t, tenant)
}

func seedUser(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO users (id, tenant_id, email, display_name, status)
		VALUES ($1, $2, $3, 'Change Owner', 'active')
	`, id, tenant, id.String()+"@example.com"); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'CC8 change-management control', 'CC8', 'manual_attested', $3)
	`, id, tenant, "cc8_"+id.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func validCreate(actor, controlID uuid.UUID) change.CreateInput {
	return change.CreateInput{
		Title:              "Deploy privileged-access workflow update",
		Description:        "Change to approval routing for elevated access.",
		ProposedBy:         actor,
		RiskNotes:          "Medium operational risk during routing cutover.",
		RollbackNotes:      "Restore previous routing rule set.",
		AffectedControlIDs: []uuid.UUID{controlID},
	}
}

func TestCreateApproveImplementVerify_EmitsCC8Evidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	actor := seedUser(t, admin, tenant)
	controlID := seedControl(t, admin, tenant)
	store := change.NewStore(app)
	ctx := ctxFor(t, tenant)

	ch, err := store.Create(ctx, validCreate(actor, controlID))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if ch.Status != change.StatusProposed {
		t.Fatalf("status = %q, want proposed", ch.Status)
	}
	links, err := store.ListControls(ctx, ch.ID)
	if err != nil {
		t.Fatalf("ListControls: %v", err)
	}
	if len(links) != 1 || links[0].ControlID != controlID {
		t.Fatalf("links = %+v, want control %s", links, controlID)
	}

	if _, err := store.Implement(ctx, ch.ID, actor, time.Now()); !errors.Is(err, change.ErrWrongState) {
		t.Fatalf("Implement before approval: got %v, want ErrWrongState", err)
	}
	if _, err := store.Approve(ctx, ch.ID, actor, time.Now()); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := store.Implement(ctx, ch.ID, actor, time.Now()); err != nil {
		t.Fatalf("Implement: %v", err)
	}
	if _, err := store.Verify(ctx, ch.ID, actor, time.Now()); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	var approval, verification int
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM evidence_records
		WHERE tenant_id = $1 AND control_id = $2 AND evidence_kind = 'change.approval.v1'
	`, tenant, controlID).Scan(&approval); err != nil {
		t.Fatalf("count approval evidence: %v", err)
	}
	if err := admin.QueryRow(context.Background(), `
		SELECT count(*) FROM evidence_records
		WHERE tenant_id = $1 AND control_id = $2 AND evidence_kind = 'change.verification.v1'
	`, tenant, controlID).Scan(&verification); err != nil {
		t.Fatalf("count verification evidence: %v", err)
	}
	if approval != 1 || verification != 1 {
		t.Fatalf("evidence counts approval=%d verification=%d, want 1/1", approval, verification)
	}

	rollup, err := store.Rollup(ctx)
	if err != nil {
		t.Fatalf("Rollup: %v", err)
	}
	if rollup.Total != 1 || rollup.Verified != 1 || rollup.Backlog != 0 || rollup.VerifiedLast30Days != 1 {
		t.Fatalf("rollup = %+v, want one verified with no backlog", rollup)
	}
}

func TestImportJira_MapsTicketToProposedChange(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	actor := seedUser(t, admin, tenant)
	controlID := seedControl(t, admin, tenant)
	store := change.NewStore(app)

	changes, err := store.ImportJira(ctxFor(t, tenant), actor, []change.JiraTicket{{
		TicketKey: "CHG-42",
		Summary:   "Rotate production deploy key",
		Status:    "Ready for CAB",
		URL:       "https://jira.example.test/browse/CHG-42",
	}}, []uuid.UUID{controlID})
	if err != nil {
		t.Fatalf("ImportJira: %v", err)
	}
	if len(changes) != 1 {
		t.Fatalf("imported %d changes, want 1", len(changes))
	}
	ch := changes[0]
	if ch.Source != change.SourceJira || ch.SourceRef != "CHG-42" || ch.Title != "Rotate production deploy key" || ch.Status != change.StatusProposed {
		t.Fatalf("mapped change = %+v", ch)
	}
}

func TestImportCSV_CreatesManualFallbackRecords(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	actor := seedUser(t, admin, tenant)
	controlID := seedControl(t, admin, tenant)
	store := change.NewStore(app)

	body := "title,description,control_id,source_ref,risk_notes,rollback_notes\n" +
		"Patch SSO routing,Change from CSV," + controlID.String() + ",CSV-1,Low risk,Revert routing\n"
	got, err := store.ImportCSV(ctxFor(t, tenant), actor, strings.NewReader(body))
	if err != nil {
		t.Fatalf("ImportCSV: %v", err)
	}
	if len(got) != 1 || got[0].Source != change.SourceCSV || got[0].SourceRef != "CSV-1" {
		t.Fatalf("csv import = %+v", got)
	}
}

func TestRLS_CrossTenantChangeIsolation(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := seedTenant(t, admin)
	tenantB := seedTenant(t, admin)
	actorA := seedUser(t, admin, tenantA)
	controlA := seedControl(t, admin, tenantA)
	store := change.NewStore(app)

	ch, err := store.Create(ctxFor(t, tenantA), validCreate(actorA, controlA))
	if err != nil {
		t.Fatalf("Create tenant A: %v", err)
	}
	if _, err := store.Get(ctxFor(t, tenantB), ch.ID); !errors.Is(err, change.ErrNotFound) {
		t.Fatalf("cross-tenant Get: got %v, want ErrNotFound", err)
	}
	rows, err := store.List(ctxFor(t, tenantB), "", 25)
	if err != nil {
		t.Fatalf("List tenant B: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("tenant B saw %d tenant A changes", len(rows))
	}
}
