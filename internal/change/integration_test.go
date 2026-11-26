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

	"github.com/mgoodric/security-atlas/internal/api/schemaregistry"
	"github.com/mgoodric/security-atlas/internal/change"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidence/ingest"
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

// newStore builds the production wiring: a change store writing evidence
// through an ingest.Service over the app role, validated by the DB-backed
// schema registry seeded with the bundled platform schemas.
func newStore(t *testing.T, admin, app *pgxpool.Pool) *change.Store {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	reg := schemaregistry.NewService(admin)
	if _, _, err := reg.ImportPlatformSchemas(ctx, schemaregistry.PlatformSchemasFS()); err != nil {
		t.Fatalf("ImportPlatformSchemas: %v", err)
	}
	return change.NewStore(app).WithIngest(ingest.New(app, reg))
}

// auditDecisions returns the evidence_audit_log decisions recorded for an
// idempotency key, oldest first.
func auditDecisions(t *testing.T, admin *pgxpool.Pool, tenant, idemKey string) []string {
	t.Helper()
	rows, err := admin.Query(context.Background(), `
		SELECT decision FROM evidence_audit_log
		WHERE tenant_id = $1 AND idempotency_key = $2
		ORDER BY received_at
	`, tenant, idemKey)
	if err != nil {
		t.Fatalf("query evidence_audit_log: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatalf("scan audit row: %v", err)
		}
		out = append(out, d)
	}
	return out
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
	store := newStore(t, admin, app)
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
	store := newStore(t, admin, app)

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
	store := newStore(t, admin, app)

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
	store := newStore(t, admin, app)

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

// #1830: change evidence goes through ingest.Service.ProcessInTx, so a
// backdated approval is refused with ErrObservedAtSkew, the transition
// rolls back, and the refusal leaves an evidence_audit_log row.
func TestApprove_BackdatedEvidenceRefusedAndAudited(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := seedTenant(t, admin)
	actor := seedUser(t, admin, tenant)
	controlID := seedControl(t, admin, tenant)
	store := newStore(t, admin, app)
	ctx := ctxFor(t, tenant)

	ch, err := store.Create(ctx, validCreate(actor, controlID))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	backdated := time.Now().Add(-ingest.MaxObservedAtSkew - time.Hour)
	if _, err := store.Approve(ctx, ch.ID, actor, backdated); !errors.Is(err, ingest.ErrObservedAtSkew) {
		t.Fatalf("backdated Approve: got %v, want ErrObservedAtSkew", err)
	}
	got, err := store.Get(ctx, ch.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusProposed {
		t.Fatalf("status after refused approval = %q, want proposed (transition rolled back)", got.Status)
	}
	var n int
	if err := admin.QueryRow(context.Background(),
		`SELECT count(*) FROM evidence_records WHERE tenant_id = $1`, tenant).Scan(&n); err != nil {
		t.Fatalf("count evidence: %v", err)
	}
	if n != 0 {
		t.Fatalf("evidence rows after refusal = %d, want 0", n)
	}
	idem := "change:" + ch.ID.String() + ":" + controlID.String() + ":" + change.ActionApproved
	if d := auditDecisions(t, admin, tenant, idem); len(d) != 1 || d[0] != "rejected_observed_at_skew" {
		t.Fatalf("audit decisions = %v, want [rejected_observed_at_skew]", d)
	}

	// The same approval at the current time is accepted and audited in
	// the transition's transaction.
	if _, err := store.Approve(ctx, ch.ID, actor, time.Now()); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if d := auditDecisions(t, admin, tenant, idem); len(d) != 2 || d[1] != "accepted" {
		t.Fatalf("audit decisions = %v, want [rejected_observed_at_skew accepted]", d)
	}
}

// #1830: a store built without an ingest service fails closed instead of
// writing the ledger some other way.
func TestApprove_WithoutIngestFailsClosed(t *testing.T) {
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
	if _, err := store.Approve(ctx, ch.ID, actor, time.Now()); !errors.Is(err, ingest.ErrUnavailable) {
		t.Fatalf("Approve without ingest: got %v, want ErrUnavailable", err)
	}
	got, err := store.Get(ctx, ch.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != change.StatusProposed {
		t.Fatalf("status = %q, want proposed", got.Status)
	}
}
