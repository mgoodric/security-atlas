//go:build integration

// Integration tests for slice 019: Risk register CRUD + per-treatment validation
// + heatmap + tenant isolation. Real Postgres only — RLS cannot be tested
// against a fake DB (memory rule: "Never mock the DB").
//
// Run with:  go test -tags=integration -race ./internal/risk/...

package risk_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/risk"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ---- harness helpers (same shape as scope/integration_test.go) ----

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
			`DELETE FROM risk_control_links WHERE tenant_id = $1`,
			`DELETE FROM risks WHERE tenant_id = $1`,
			`DELETE FROM controls WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

// seedControl creates a control row directly via the admin pool (BYPASSRLS) so
// we have something to link to in mitigate cases.
//
// Slice 020 patch: `controls.bundle_id` became NOT NULL in slice 009's
// migration, which landed in the cumulative schema after slice 019 authored
// this helper — same stale-fixture precedent as slices 019/018/009. The
// bundle_id is computed in Go (not a reused placeholder) so it does not trip
// pgx single-placeholder type inference (SQLSTATE 42P08).
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	bundleID := "slice-019-test-bundle-" + ctrlID.String()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (id, tenant_id, title, control_family, implementation_type, bundle_id)
		VALUES ($1, $2, 'Test control', 'AAA', 'automated', $3)
	`, ctrlID, tenant, bundleID); err != nil {
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

// ---- AC-1: methodology defaults to nist_800_30 ----

func TestCreate_DefaultsMethodologyToNist80030(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Title:         "Unauthorized PHI access",
		Category:      dbx.RiskCategoryConfidentiality,
		Methodology:   "", // omitted on purpose
		InherentScore: []byte(`{"likelihood":3,"impact":4}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Methodology != dbx.RiskMethodologyNist80030 {
		t.Fatalf("expected default methodology nist_800_30, got %q", created.Methodology)
	}
}

// ---- AC-2: inherent_score validated against methodology ----

func TestCreate_RejectsInvalidNistScore(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Title:         "Bad score",
		Category:      dbx.RiskCategoryIntegrity,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":9,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if !errors.Is(err, risk.ErrInherentScoreInvalid) {
		t.Fatalf("want ErrInherentScoreInvalid; got %v", err)
	}
}

func TestCreate_RejectsFairMissingLM(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Title:         "FAIR-style risk",
		Category:      dbx.RiskCategoryFinancial,
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.0}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if !errors.Is(err, risk.ErrInherentScoreInvalid) {
		t.Fatalf("want ErrInherentScoreInvalid; got %v", err)
	}
}

// ---- AC-3: treatment=mitigate requires linked controls ----

func TestCreate_MitigateRequiresLinkedControl(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Title:         "Needs mitigation",
		Category:      dbx.RiskCategoryOperational,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":4,"impact":3}`),
		Treatment:     dbx.RiskTreatmentMitigate,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation error; got %v", err)
	}
}

func TestCreate_MitigateWithLinkedControlSucceeds(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	ctrl := seedControl(t, admin, tenant)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Title:            "Mitigated",
		Category:         dbx.RiskCategoryOperational,
		Methodology:      dbx.RiskMethodologyNist80030,
		InherentScore:    []byte(`{"likelihood":2,"impact":2}`),
		Treatment:        dbx.RiskTreatmentMitigate,
		LinkedControlIDs: []uuid.UUID{ctrl},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if len(created.LinkedControlIDs) != 1 || created.LinkedControlIDs[0] != ctrl {
		t.Fatalf("expected linked control %s, got %v", ctrl, created.LinkedControlIDs)
	}
	// Re-read to confirm link persisted.
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.LinkedControlIDs) != 1 {
		t.Fatalf("expected 1 linked control after Get; got %d", len(got.LinkedControlIDs))
	}
}

// ---- AC-4: treatment=accept requires accepted_until + accepter ----

func TestCreate_AcceptRequiresBothFields(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Missing accepted_until.
	_, err := store.Create(ctx, risk.CreateInput{
		Title:         "Accepted",
		Category:      dbx.RiskCategoryRegulatory,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":1,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAccept,
		Accepter:      "ciso@example.com",
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation (missing accepted_until); got %v", err)
	}

	// Missing accepter.
	until := time.Now().AddDate(0, 6, 0)
	_, err = store.Create(ctx, risk.CreateInput{
		Title:         "Accepted",
		Category:      dbx.RiskCategoryRegulatory,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":1,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAccept,
		AcceptedUntil: &until,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation (missing accepter); got %v", err)
	}

	// Both fields present succeeds.
	created, err := store.Create(ctx, risk.CreateInput{
		Title:         "Accepted",
		Category:      dbx.RiskCategoryRegulatory,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":1,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAccept,
		AcceptedUntil: &until,
		Accepter:      "ciso@example.com",
	})
	if err != nil {
		t.Fatalf("Create with both accept fields: %v", err)
	}
	if created.Accepter != "ciso@example.com" {
		t.Fatalf("accepter not persisted: %q", created.Accepter)
	}
}

// ---- AC-5: treatment=transfer requires instrument_reference ----

func TestCreate_TransferRequiresInstrumentReference(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Title:         "Insured",
		Category:      dbx.RiskCategoryFinancial,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":3,"impact":3}`),
		Treatment:     dbx.RiskTreatmentTransfer,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation (missing instrument_reference); got %v", err)
	}

	created, err := store.Create(ctx, risk.CreateInput{
		Title:               "Insured",
		Category:            dbx.RiskCategoryFinancial,
		Methodology:         dbx.RiskMethodologyNist80030,
		InherentScore:       []byte(`{"likelihood":3,"impact":3}`),
		Treatment:           dbx.RiskTreatmentTransfer,
		InstrumentReference: "Cyber Policy #ACME-2026-001",
	})
	if err != nil {
		t.Fatalf("Create with instrument_reference: %v", err)
	}
	if created.InstrumentReference != "Cyber Policy #ACME-2026-001" {
		t.Fatalf("instrument_reference not persisted: %q", created.InstrumentReference)
	}
}

// ---- AC-6: filters work ----

func TestList_Filters(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	until := time.Now().AddDate(0, 6, 0)
	// One nist_800_30 + accept
	if _, err := store.Create(ctx, risk.CreateInput{
		Title: "A", Category: dbx.RiskCategoryPrivacy,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":2,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAccept,
		AcceptedUntil: &until, Accepter: "u@example.com",
	}); err != nil {
		t.Fatalf("A: %v", err)
	}
	// One fair + avoid
	if _, err := store.Create(ctx, risk.CreateInput{
		Title: "B", Category: dbx.RiskCategoryFinancial,
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.0,"lm":50000}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("B: %v", err)
	}
	// One qualitative_5x5 + avoid
	if _, err := store.Create(ctx, risk.CreateInput{
		Title: "C", Category: dbx.RiskCategoryAvailability,
		Methodology:   dbx.RiskMethodologyQualitative5x5,
		InherentScore: []byte(`{"likelihood":5,"impact":5}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("C: %v", err)
	}

	got, err := store.List(ctx, risk.ListFilter{Treatment: dbx.RiskTreatmentAccept})
	if err != nil {
		t.Fatalf("List treatment=accept: %v", err)
	}
	if len(got) != 1 || got[0].Title != "A" {
		t.Fatalf("treatment filter expected [A], got %v", titles(got))
	}

	got, err = store.List(ctx, risk.ListFilter{Methodology: dbx.RiskMethodologyFair})
	if err != nil {
		t.Fatalf("List methodology=fair: %v", err)
	}
	if len(got) != 1 || got[0].Title != "B" {
		t.Fatalf("methodology filter expected [B], got %v", titles(got))
	}

	got, err = store.List(ctx, risk.ListFilter{Category: dbx.RiskCategoryAvailability})
	if err != nil {
		t.Fatalf("List category=availability: %v", err)
	}
	if len(got) != 1 || got[0].Title != "C" {
		t.Fatalf("category filter expected [C], got %v", titles(got))
	}
}

// ---- AC-7: heatmap ----

func TestHeatmap_CountsBy5x5Methodology(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()
	tenant := freshTenant(t, admin)
	store := risk.NewStore(app)
	ctx := ctxFor(t, tenant)

	// Two nist_800_30 risks in the same bucket (3,4).
	for i := 0; i < 2; i++ {
		if _, err := store.Create(ctx, risk.CreateInput{
			Title:         "n",
			Category:      dbx.RiskCategoryConfidentiality,
			Methodology:   dbx.RiskMethodologyNist80030,
			InherentScore: []byte(`{"likelihood":3,"impact":4}`),
			Treatment:     dbx.RiskTreatmentAvoid,
		}); err != nil {
			t.Fatalf("Create nist %d: %v", i, err)
		}
	}
	// One qualitative_5x5 in bucket (5,5).
	if _, err := store.Create(ctx, risk.CreateInput{
		Title:         "q",
		Category:      dbx.RiskCategoryAvailability,
		Methodology:   dbx.RiskMethodologyQualitative5x5,
		InherentScore: []byte(`{"likelihood":5,"impact":5}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("Create qualitative: %v", err)
	}
	// One FAIR — must NOT appear in heatmap.
	if _, err := store.Create(ctx, risk.CreateInput{
		Title:         "f",
		Category:      dbx.RiskCategoryFinancial,
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.0,"lm":50000}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("Create fair: %v", err)
	}

	cells, err := store.Heatmap(ctx)
	if err != nil {
		t.Fatalf("Heatmap: %v", err)
	}
	want := map[[2]int]int{{3, 4}: 2, {5, 5}: 1}
	got := map[[2]int]int{}
	for _, c := range cells {
		got[[2]int{c.Likelihood, c.Impact}] = c.Count
	}
	if len(got) != len(want) {
		t.Fatalf("heatmap cell count mismatch: want %d, got %d (%v)", len(want), len(got), got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("heatmap[%v] = %d; want %d", k, got[k], v)
		}
	}
}

// ---- Invariant 6: cross-tenant isolation ----

func TestCrossTenant_RisksAreIsolated(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	app := openPool(t, appDSN(t))
	defer app.Close()

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	store := risk.NewStore(app)

	ctxA := ctxFor(t, tenantA)
	ctxB := ctxFor(t, tenantB)

	created, err := store.Create(ctxA, risk.CreateInput{
		Title:         "A's risk",
		Category:      dbx.RiskCategoryRegulatory,
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":1,"impact":1}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if err != nil {
		t.Fatalf("Create as A: %v", err)
	}

	// Tenant B should not see it.
	listB, err := store.List(ctxB, risk.ListFilter{})
	if err != nil {
		t.Fatalf("List as B: %v", err)
	}
	for _, r := range listB {
		if r.ID == created.ID {
			t.Fatalf("tenant B saw tenant A's risk")
		}
	}

	// Tenant B GET-by-id should not find it.
	if _, err := store.Get(ctxB, created.ID); !errors.Is(err, risk.ErrNotFound) {
		t.Fatalf("tenant B Get expected ErrNotFound; got %v", err)
	}

	// Tenant B DELETE should also surface ErrNotFound (RLS hides the row).
	if err := store.Delete(ctxB, created.ID); !errors.Is(err, risk.ErrNotFound) {
		t.Fatalf("tenant B Delete expected ErrNotFound; got %v", err)
	}

	// Tenant A still sees + can delete its own row.
	if _, err := store.Get(ctxA, created.ID); err != nil {
		t.Fatalf("tenant A Get own: %v", err)
	}
	if err := store.Delete(ctxA, created.ID); err != nil {
		t.Fatalf("tenant A Delete own: %v", err)
	}
}

func titles(in []risk.Risk) []string {
	out := make([]string, len(in))
	for i, r := range in {
		out[i] = r.Title
	}
	return out
}
