//go:build integration

// Integration tests for slice 027: walkthrough recording primitive. Real
// Postgres only -- RLS cannot be tested against a fake DB, and the
// canonical-hash + tamper-detection contracts (AC-3 + AC-6) only have
// meaning against a real ledger.
//
// Run with: go test -tags=integration -race ./internal/audit/walkthrough/...
//
// Required env:
//
//	DATABASE_URL      - migration role DSN (BYPASSRLS); used by the harness
//	                    to seed controls + periods outside the tenant GUC.
//	DATABASE_URL_APP  - application role DSN (NOSUPERUSER NOBYPASSRLS); the
//	                    walkthrough.Store runs against this so RLS is enforced.

package walkthrough_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/audit/period"
	"github.com/mgoodric/security-atlas/internal/audit/walkthrough"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

func appDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func adminDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

func openPool(t *testing.T, dsn string) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("pgxpool.New: %v", err)
	}
	return pool
}

func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM walkthrough_audit_log WHERE tenant_id = $1`,
			`DELETE FROM walkthrough_attachments WHERE tenant_id = $1`,
			`DELETE FROM walkthroughs WHERE tenant_id = $1`,
			`DELETE FROM audit_period_audit_log WHERE tenant_id = $1`,
			`DELETE FROM audit_periods WHERE tenant_id = $1`,
			`DELETE FROM evidence_records WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func seedFrameworkVersion(t *testing.T, admin *pgxpool.Pool) uuid.UUID {
	t.Helper()
	fwID := uuid.New()
	versionID := uuid.New()
	slug := fmt.Sprintf("slice027-%s", uuid.NewString()[:8])
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO frameworks (id, tenant_id, name, slug, issuer)
		VALUES ($1, NULL, 'Slice 027 test framework', $2, 'test')
	`, fwID, slug); err != nil {
		t.Fatalf("seed framework: %v", err)
	}
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO framework_versions (id, tenant_id, framework_id, version)
		VALUES ($1, NULL, $2, '1.0')
	`, versionID, fwID); err != nil {
		t.Fatalf("seed framework_version: %v", err)
	}
	return versionID
}

func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'slice 027 test control', 'AAA', 'automated', 'test-bundle-027')
	`, ctrlID, tenant); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

func ctxFor(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// ----- tests -----

// AC-1: POST /v1/walkthroughs creates with control_id + narrative; the
// initial canonical_hash is stamped.
func TestCreate_StampsInitialHash(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID:  ctrlID,
		Narrative:  "The team rotates the API key every 90 days. We verify rotation via the IAM access keys API.",
		Transcript: "auditor: walk me through. engineer: cron triggers rotation on day 89.",
		CreatedBy:  "key_test_027_ac1",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if w.Status != walkthrough.StatusDraft {
		t.Fatalf("AC-1: expected status=draft, got %q", w.Status)
	}
	if len(w.CanonicalHash) != 32 {
		t.Fatalf("AC-1: expected 32-byte sha256, got %d bytes", len(w.CanonicalHash))
	}
	if w.CreatedBy != "key_test_027_ac1" {
		t.Fatalf("AC-1: expected created_by=key_test_027_ac1, got %q", w.CreatedBy)
	}
}

// AC-3 + AC-6 (write side): adding an attachment recomputes the
// canonical_hash to commit to the new attachment set. The post-add hash
// must NOT equal the pre-add hash.
func TestAddAttachment_RecomputesHash(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "AC-3 narrative",
		CreatedBy: "key_test_027_ac3",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	preHash := append([]byte(nil), w.CanonicalHash...)

	after, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      1024,
		SHA256Hex:      "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		AnnotationsRaw: []byte(`{"regions":[{"x":10,"y":10,"w":100,"h":50,"label":"key rotation cron"}]}`),
		UploadedBy:     "key_test_027_ac3",
	})
	if err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	if hex.EncodeToString(after.CanonicalHash) == hex.EncodeToString(preHash) {
		t.Fatalf("AC-3: hash did not change after attachment add (pre=%x post=%x)",
			preHash, after.CanonicalHash)
	}
}

// AC-4: GET /v1/walkthroughs/:id returns the walkthrough with the
// attachment list and a tamper-detected flag = false on the happy path.
func TestGet_ReturnsAttachmentsAndCleanTamperFlag(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "AC-4 narrative",
		CreatedBy: "key_test_027_ac4",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      256,
		SHA256Hex:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_ac4",
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	got, err := store.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.TamperDetected {
		t.Fatalf("AC-4: tamper_detected should be false on a freshly-attached walkthrough, got true")
	}
	if len(got.Attachments) != 1 {
		t.Fatalf("AC-4: expected 1 attachment, got %d", len(got.Attachments))
	}
	if got.Attachments[0].SHA256Hex != "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("AC-4: attachment sha256 mismatch, got %q", got.Attachments[0].SHA256Hex)
	}
}

// AC-6: tamper detection at retrieval. Out-of-band mutation of an
// attachment's sha256_hash (simulating someone replacing the bytes
// behind the platform's back) must surface tamper_detected=true on the
// next Get. The mutation must NOT silently flip the stored
// canonical_hash, so the GET-time re-hash deviates.
func TestGet_DetectsAttachmentTampering(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "AC-6 narrative",
		CreatedBy: "key_test_027_ac6",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      512,
		SHA256Hex:      "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_ac6",
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}

	// Out-of-band mutation: rewrite the attachment sha256_hash directly
	// in the DB, bypassing the application's hash-recompute. This
	// simulates a privileged operator (or a successful exploit) mutating
	// stored bytes behind the platform's back. The walkthrough's
	// canonical_hash stays at the as-attached value; the GET-time
	// re-compute MUST diverge.
	if _, err := admin.Exec(context.Background(), `
		UPDATE walkthrough_attachments
		SET sha256_hash = 'dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
		WHERE tenant_id = $1 AND walkthrough_id = $2
	`, tenant, w.ID); err != nil {
		t.Fatalf("out-of-band tamper: %v", err)
	}

	got, err := store.Get(ctx, w.ID)
	if err != nil {
		t.Fatalf("Get post-tamper: %v", err)
	}
	if !got.TamperDetected {
		t.Fatalf("AC-6: expected tamper_detected=true after out-of-band attachment mutation, got false")
	}
}

// P0-3 / AC-6 (period freeze): once the walkthrough's audit_period_id
// points at a frozen period, AddAttachment + Finalize both return
// ErrPeriodFrozen.
func TestMutationAfterPeriodFreeze_Rejected(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	fwID := seedFrameworkVersion(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	periodStore := period.NewStore(app)
	wtStore := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	p, err := periodStore.Create(ctx, period.CreateInput{
		Name:               "Slice 027 freeze test",
		FrameworkVersionID: fwID,
		PeriodStart:        time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:          time.Date(2026, 6, 30, 0, 0, 0, 0, time.UTC),
		CreatedBy:          "key_test_027_p0",
	})
	if err != nil {
		t.Fatalf("period.Create: %v", err)
	}

	w, err := wtStore.Create(ctx, walkthrough.CreateInput{
		ControlID:     ctrlID,
		AuditPeriodID: &p.ID,
		Narrative:     "freeze-gated walkthrough",
		CreatedBy:     "key_test_027_p0",
	})
	if err != nil {
		t.Fatalf("walkthrough.Create: %v", err)
	}

	if _, err := periodStore.Freeze(ctx, p.ID, "key_test_027_p0", time.Now().UTC()); err != nil {
		t.Fatalf("period.Freeze: %v", err)
	}

	// Attempt to add an attachment after the period is frozen.
	_, err = wtStore.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      128,
		SHA256Hex:      "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_p0",
	})
	if !errors.Is(err, walkthrough.ErrPeriodFrozen) {
		t.Fatalf("P0-3: expected ErrPeriodFrozen on add after freeze, got %v", err)
	}

	// Attempt to finalize after the period is frozen.
	_, err = wtStore.Finalize(ctx, w.ID, "key_test_027_p0")
	if !errors.Is(err, walkthrough.ErrPeriodFrozen) {
		t.Fatalf("P0-3: expected ErrPeriodFrozen on finalize after freeze, got %v", err)
	}
}

// Tamper rejection writes a tamper_detected row to the audit log.
func TestTamperDetection_WritesAuditLog(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "audit-log narrative",
		CreatedBy: "key_test_027_log",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      256,
		SHA256Hex:      "1111111111111111111111111111111111111111111111111111111111111111",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_log",
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	// Tamper.
	if _, err := admin.Exec(context.Background(), `
		UPDATE walkthrough_attachments
		SET sha256_hash = '2222222222222222222222222222222222222222222222222222222222222222'
		WHERE tenant_id = $1 AND walkthrough_id = $2
	`, tenant, w.ID); err != nil {
		t.Fatalf("out-of-band tamper: %v", err)
	}
	// Trigger detection.
	if _, err := store.Get(ctx, w.ID); err != nil {
		t.Fatalf("Get: %v", err)
	}
	// Check the audit log.
	rows, err := store.ListAuditLog(ctx, w.ID)
	if err != nil {
		t.Fatalf("ListAuditLog: %v", err)
	}
	var sawTamper, sawCreated, sawAttach bool
	for _, r := range rows {
		switch r.Action {
		case "tamper_detected":
			sawTamper = true
		case "walkthrough_created":
			sawCreated = true
		case "attachment_added":
			sawAttach = true
		}
	}
	if !sawCreated {
		t.Errorf("expected walkthrough_created audit log row, missing")
	}
	if !sawAttach {
		t.Errorf("expected attachment_added audit log row, missing")
	}
	if !sawTamper {
		t.Errorf("expected tamper_detected audit log row, missing")
	}
}

// AC-5 (JSON side): finalize then export to JSON. The export shape must
// carry the audit-binding hash + every attachment.
func TestFinalizeAndExportJSON(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrlID := seedControl(t, admin, tenant)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})
	ctx := ctxFor(t, tenant)

	w, err := store.Create(ctx, walkthrough.CreateInput{
		ControlID: ctrlID,
		Narrative: "AC-5 narrative",
		CreatedBy: "key_test_027_ac5",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      777,
		SHA256Hex:      "3333333333333333333333333333333333333333333333333333333333333333",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_ac5",
	}); err != nil {
		t.Fatalf("AddAttachment: %v", err)
	}
	finalized, err := store.Finalize(ctx, w.ID, "key_test_027_ac5")
	if err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	if finalized.Status != walkthrough.StatusFinalized {
		t.Fatalf("AC-5: expected status=finalized, got %q", finalized.Status)
	}

	// Finalize-after-finalize rejects.
	if _, err := store.Finalize(ctx, w.ID, "key_test_027_ac5"); !errors.Is(err, walkthrough.ErrFinalized) {
		t.Fatalf("expected ErrFinalized on double-finalize, got %v", err)
	}

	// Add-after-finalize rejects.
	if _, err := store.AddAttachment(ctx, walkthrough.AttachInput{
		WalkthroughID:  w.ID,
		StorageKey:     "tenant-" + tenant + "/" + uuid.NewString(),
		ContentType:    "image/png",
		SizeBytes:      1,
		SHA256Hex:      "4444444444444444444444444444444444444444444444444444444444444444",
		AnnotationsRaw: []byte(`{}`),
		UploadedBy:     "key_test_027_ac5",
	}); !errors.Is(err, walkthrough.ErrFinalized) {
		t.Fatalf("expected ErrFinalized on attach after finalize, got %v", err)
	}

	ex := walkthrough.ToExportJSON(finalized)
	if ex.Status != string(walkthrough.StatusFinalized) {
		t.Fatalf("AC-5 export: status mismatch %q", ex.Status)
	}
	if len(ex.Attachments) != 1 {
		t.Fatalf("AC-5 export: expected 1 attachment, got %d", len(ex.Attachments))
	}
	if ex.Attachments[0].SHA256 != "3333333333333333333333333333333333333333333333333333333333333333" {
		t.Fatalf("AC-5 export: attachment sha256 mismatch")
	}
	if ex.CanonicalHash == "" {
		t.Fatalf("AC-5 export: canonical_hash must be non-empty")
	}
}

// RLS: a tenant cannot read another tenant's walkthroughs. Two distinct
// tenant GUC contexts must yield two non-overlapping List results.
func TestRLS_TenantBoundaryEnforced(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	ctrlA := seedControl(t, admin, tenantA)
	ctrlB := seedControl(t, admin, tenantB)

	store := walkthrough.NewStore(walkthrough.Config{Pool: app})

	wA, err := store.Create(ctxFor(t, tenantA), walkthrough.CreateInput{
		ControlID: ctrlA,
		Narrative: "tenant A walkthrough",
		CreatedBy: "key_test_027_rls_a",
	})
	if err != nil {
		t.Fatalf("Create A: %v", err)
	}
	wB, err := store.Create(ctxFor(t, tenantB), walkthrough.CreateInput{
		ControlID: ctrlB,
		Narrative: "tenant B walkthrough",
		CreatedBy: "key_test_027_rls_b",
	})
	if err != nil {
		t.Fatalf("Create B: %v", err)
	}

	// Tenant A's List must NOT include the tenant-B walkthrough.
	listA, err := store.List(ctxFor(t, tenantA))
	if err != nil {
		t.Fatalf("List A: %v", err)
	}
	for _, w := range listA {
		if w.ID == wB.ID {
			t.Fatalf("RLS: tenant A list returned tenant B walkthrough %s", wB.ID)
		}
	}
	// Tenant A cannot Get the tenant-B walkthrough -- the RLS-shielded
	// lookup returns ErrNotFound, NOT a 403, to avoid existence
	// disclosure.
	if _, err := store.Get(ctxFor(t, tenantA), wB.ID); !errors.Is(err, walkthrough.ErrNotFound) {
		t.Fatalf("RLS: tenant A get on tenant B walkthrough: expected ErrNotFound, got %v", err)
	}

	// And the symmetric check.
	if _, err := store.Get(ctxFor(t, tenantB), wA.ID); !errors.Is(err, walkthrough.ErrNotFound) {
		t.Fatalf("RLS: tenant B get on tenant A walkthrough: expected ErrNotFound, got %v", err)
	}
}
