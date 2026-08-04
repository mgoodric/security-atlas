//go:build integration

package incident_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/incident"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"incident_timeline",
		"incident_evidence_links",
		"incident_vendors",
		"incident_risks",
		"incident_controls",
		"incidents",
		"evidence_records",
		"vendors",
		"risks",
		"controls",
	)
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Incident response control', 'IR', 'manual_attested', $3)
	`, id, tenant, "legacy_"+id.String())
	if err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return id
}

func seedRisk(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := admin.Exec(context.Background(), `
		INSERT INTO risks (id, tenant_id, title, category, treatment, accepted_until, accepter)
		VALUES ($1, $2, 'Incident risk', 'operational', 'accept', CURRENT_DATE + 30, 'owner')
	`, id, tenant)
	if err != nil {
		t.Fatalf("seed risk: %v", err)
	}
	return id
}

func seedVendor(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := admin.Exec(context.Background(), `
		INSERT INTO vendors (id, tenant_id, name, criticality)
		VALUES ($1, $2, 'Incident Vendor', 'high')
	`, id, tenant)
	if err != nil {
		t.Fatalf("seed vendor: %v", err)
	}
	return id
}

func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, controlID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	sum := sha256.Sum256([]byte(id.String()))
	_, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, provenance, result,
			payload, hash, freshness_class, idempotency_key, evidence_kind, schema_version,
			credential_id, ingestion_path, source_attribution, scope_canonical, observed_at_nanos
		) VALUES (
			$1, $2, $3, $7, now(), '{}'::jsonb, 'pass',
			'{}'::jsonb, $4, 'monthly', $5, 'seed.evidence.v1', '1.0.0',
			'seed', 'manual_upload', '{}'::jsonb, '[]'::jsonb, $6
		)
	`, id, tenant, controlID, hex.EncodeToString(sum[:]), "seed:"+id.String(), time.Now().UnixNano(), controlID.String())
	if err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	return dbtest.WithTenantCtx(t, tenant)
}

func validCreate(controlID, riskID, vendorID, evidenceID uuid.UUID) incident.CreateInput {
	tier := "critical"
	return incident.CreateInput{
		Title:              "Suspicious production access",
		Description:        "Unexpected admin session detected",
		OperatorSeverity:   incident.SeverityP2,
		AffectedSystemTier: &tier,
		AffectedSystems:    []byte(`[{"name":"prod-api","kind":"service","criticality":"critical"}]`),
		DetectedBy:         "key_detector",
		DetectedAt:         time.Now().UTC(),
		ControlIDs:         []uuid.UUID{controlID},
		RiskIDs:            []uuid.UUID{riskID},
		VendorIDs:          []uuid.UUID{vendorID},
		EvidenceIDs:        []uuid.UUID{evidenceID},
	}
}

func TestIncidentLifecycleLinksTimelineAndClosureEvidence(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	risk := seedRisk(t, admin, tenant)
	vendor := seedVendor(t, admin, tenant)
	ev := seedEvidence(t, admin, tenant, ctrl)
	store := incident.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, risk, vendor, ev))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Incident.Status != incident.StateDetected {
		t.Fatalf("status = %q", created.Incident.Status)
	}
	if created.Incident.Severity != incident.SeverityP1 {
		t.Fatalf("severity floor = %q, want p1", created.Incident.Severity)
	}
	if len(created.Links.ControlIDs) != 1 || len(created.Links.RiskIDs) != 1 || len(created.Links.VendorIDs) != 1 || len(created.Links.EvidenceIDs) != 1 {
		t.Fatalf("links not captured: %+v", created.Links)
	}
	if len(created.Timeline) != 1 || created.Timeline[0].Action != incident.ActionCreated {
		t.Fatalf("create timeline missing: %+v", created.Timeline)
	}

	for _, next := range []string{incident.StateTriaged, incident.StateContained, incident.StateResolved} {
		if _, err := store.Transition(ctx, created.Incident.ID, next, "key_responder", ""); err != nil {
			t.Fatalf("Transition(%s): %v", next, err)
		}
	}
	closed, err := store.Close(ctx, created.Incident.ID, incident.CloseInput{
		Actor:             "key_responder",
		PostmortemSummary: "Root cause was a stale admin session; session revocation and alert tuning completed.",
	})
	if err != nil {
		t.Fatalf("Close: %v", err)
	}
	if closed.Incident.Status != incident.StateClosed {
		t.Fatalf("closed status = %q", closed.Incident.Status)
	}
	if len(closed.Timeline) != 5 {
		t.Fatalf("timeline rows = %d, want 5: %+v", len(closed.Timeline), closed.Timeline)
	}
	if got := closed.Timeline[len(closed.Timeline)-1].Action; got != incident.ActionClosed {
		t.Fatalf("last action = %q", got)
	}
	if len(closed.Links.EvidenceIDs) != 2 {
		t.Fatalf("expected seed evidence plus generated postmortem evidence, got %+v", closed.Links.EvidenceIDs)
	}

	var kind, path string
	if err := admin.QueryRow(context.Background(), `
		SELECT evidence_kind, ingestion_path
		FROM evidence_records
		WHERE tenant_id = $1 AND id = $2
	`, tenant, closed.Links.EvidenceIDs[1]).Scan(&kind, &path); err != nil {
		t.Fatalf("fetch generated evidence: %v", err)
	}
	if kind != incident.EvidenceKindPostmortem || path != "manual_upload" {
		t.Fatalf("generated evidence kind/path = %q/%q", kind, path)
	}

	listed, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.Incident.ID {
		t.Fatalf("list returned %+v", listed)
	}
	fetched, err := store.Get(ctx, created.Incident.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if fetched.Incident.ID != created.Incident.ID || len(fetched.Timeline) != 5 {
		t.Fatalf("detail mismatch: %+v", fetched)
	}
}

func TestIncidentRejectsOutOfOrderTransitions(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	risk := seedRisk(t, admin, tenant)
	vendor := seedVendor(t, admin, tenant)
	ev := seedEvidence(t, admin, tenant, ctrl)
	store := incident.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, risk, vendor, ev))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.Transition(ctx, created.Incident.ID, incident.StateContained, "key_responder", "skip triage"); !errors.Is(err, incident.ErrWrongState) {
		t.Fatalf("expected ErrWrongState, got %v", err)
	}
	entries, err := store.Get(ctx, created.Incident.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(entries.Timeline) != 1 {
		t.Fatalf("out-of-order transition wrote timeline rows: %+v", entries.Timeline)
	}
}

func TestIncidentTimelineAppendOnlyForAppRole(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	risk := seedRisk(t, admin, tenant)
	vendor := seedVendor(t, admin, tenant)
	ev := seedEvidence(t, admin, tenant, ctrl)
	store := incident.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, validCreate(ctrl, risk, vendor, ev))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	tx, err := app.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := tenancy.ApplyTenant(ctx, tx); err != nil {
		t.Fatalf("ApplyTenant: %v", err)
	}
	_, updateErr := tx.Exec(ctx, `UPDATE incident_timeline SET summary = 'tampered' WHERE tenant_id = $1 AND incident_id = $2`, tenant, created.Incident.ID)
	if updateErr == nil {
		t.Fatal("expected UPDATE on incident_timeline to be denied")
	}
	_, deleteErr := tx.Exec(ctx, `DELETE FROM incident_timeline WHERE tenant_id = $1 AND incident_id = $2`, tenant, created.Incident.ID)
	if deleteErr == nil {
		t.Fatal("expected DELETE on incident_timeline to be denied")
	}
}

func TestIncidentRLSTwoTenantIsolationAndCrossTenantLinks(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenantA)
	ctrlB := seedControl(t, admin, tenantB)
	riskA := seedRisk(t, admin, tenantA)
	vendorA := seedVendor(t, admin, tenantA)
	evA := seedEvidence(t, admin, tenantA, ctrlA)
	evB := seedEvidence(t, admin, tenantB, ctrlB)
	store := incident.NewStore(app)

	created, err := store.Create(ctxFor(t, tenantA), validCreate(ctrlA, riskA, vendorA, evA))
	if err != nil {
		t.Fatalf("Create tenant A: %v", err)
	}
	if _, err := store.Get(ctxFor(t, tenantB), created.Incident.ID); !errors.Is(err, incident.ErrNotFound) {
		t.Fatalf("tenant B Get expected ErrNotFound, got %v", err)
	}
	listB, err := store.List(ctxFor(t, tenantB))
	if err != nil {
		t.Fatalf("tenant B List: %v", err)
	}
	if len(listB) != 0 {
		t.Fatalf("tenant B saw tenant A incidents: %+v", listB)
	}

	in := validCreate(ctrlA, riskA, vendorA, evB)
	_, err = store.Create(ctxFor(t, tenantA), in)
	if err == nil {
		t.Fatal("expected cross-tenant evidence link to fail")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) && !strings.Contains(err.Error(), "target does not exist in tenant") {
		t.Fatalf("expected FK-shaped cross-tenant failure, got %v", err)
	}
}

func TestSeverityWithFloor(t *testing.T) {
	got, err := incident.SeverityWithFloor(incident.SeverityP2, nil)
	if err != nil {
		t.Fatalf("SeverityWithFloor nil tier: %v", err)
	}
	if got != incident.SeverityP2 {
		t.Fatalf("nil tier severity = %q", got)
	}
	tier := "critical"
	got, err = incident.SeverityWithFloor(incident.SeverityP3, &tier)
	if err != nil {
		t.Fatalf("SeverityWithFloor critical tier: %v", err)
	}
	if got != incident.SeverityP1 {
		t.Fatalf("critical tier floor = %q, want p1", got)
	}
	tier = "medium"
	got, err = incident.SeverityWithFloor(incident.SeverityP1, &tier)
	if err != nil {
		t.Fatalf("SeverityWithFloor medium tier: %v", err)
	}
	if got != incident.SeverityP1 {
		t.Fatalf("medium tier lowered severity = %q", got)
	}
}

func TestCloseRequiresPostmortemAndLinkedControl(t *testing.T) {
	admin := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	risk := seedRisk(t, admin, tenant)
	vendor := seedVendor(t, admin, tenant)
	ev := seedEvidence(t, admin, tenant, ctrl)
	store := incident.NewStore(app)
	ctx := ctxFor(t, tenant)

	noControlInput := validCreate(ctrl, risk, vendor, ev)
	noControlInput.ControlIDs = nil
	noControl, err := store.Create(ctx, noControlInput)
	if err != nil {
		t.Fatalf("Create no-control incident: %v", err)
	}
	for _, next := range []string{incident.StateTriaged, incident.StateContained, incident.StateResolved} {
		if _, err := store.Transition(ctx, noControl.Incident.ID, next, "key_responder", ""); err != nil {
			t.Fatalf("Transition no-control %s: %v", next, err)
		}
	}
	_, err = store.Close(ctx, noControl.Incident.ID, incident.CloseInput{Actor: "key_responder", PostmortemSummary: "done"})
	if !errors.Is(err, incident.ErrIRControlRequired) {
		t.Fatalf("expected ErrIRControlRequired, got %v", err)
	}
	_, err = store.Close(ctx, noControl.Incident.ID, incident.CloseInput{Actor: "key_responder"})
	if !errors.Is(err, incident.ErrPostmortemRequired) {
		t.Fatalf("expected ErrPostmortemRequired, got %v", err)
	}
}
