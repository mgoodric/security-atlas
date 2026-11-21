//go:build integration

// Integration tests for slice 019: Risk register CRUD + per-treatment validation
// + heatmap + tenant isolation. Real Postgres only — RLS cannot be tested
// against a fake DB (memory rule: "Never mock the DB").
//
// Run with:  go test -tags=integration -race ./internal/risk/...

package risk_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/risk"
)

// ---- harness helpers ----
//
// Slice 435: the pool/DSN/tenant-seed/context boilerplate this file (and
// scope/integration_test.go, which it was copied from) used to re-derive
// now lives in the shared internal/dbtest harness. The two-pool open is
// dbtest.NewMigratePool / NewAppPool; the tenant-context tag is
// dbtest.WithTenantCtx; freshTenant delegates to dbtest.SeedTenant with the
// slice-019 cleanup tables.

// freshTenant seeds a fresh tenant and cleans risk's tables (children before
// parents) through the privileged (migrate) pool after the test.
func freshTenant(t *testing.T, migrate *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, migrate,
		"risk_control_links",
		"risks",
		"controls",
	)
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

// ---- AC-1: methodology defaults to nist_800_30 ----

func TestCreate_DefaultsMethodologyToNist80030(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "Unauthorized PHI access",
		CategoryRef:   "confidentiality",
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
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "Bad score",
		CategoryRef:   "integrity",
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":9,"impact":2}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if !errors.Is(err, risk.ErrInherentScoreInvalid) {
		t.Fatalf("want ErrInherentScoreInvalid; got %v", err)
	}
}

func TestCreate_RejectsFairMissingLM(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "FAIR-style risk",
		CategoryRef:   "financial",
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.0}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if !errors.Is(err, risk.ErrInherentScoreInvalid) {
		t.Fatalf("want ErrInherentScoreInvalid; got %v", err)
	}
}

func TestCreate_FairPersistsCanonicalDollarExposure(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "FAIR-style risk",
		CategoryRef:   "financial",
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"loss_event_frequency":2,"loss_magnitude":50000}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if err != nil {
		t.Fatalf("Create FAIR: %v", err)
	}
	got, err := risk.FairScoreFromJSON(created.InherentScore)
	if err != nil {
		t.Fatalf("created FAIR score: %v", err)
	}
	if got.AnnualizedLossExposure != 100000 {
		t.Fatalf("annualized loss exposure = %v, want 100000", got.AnnualizedLossExposure)
	}
	residual, err := risk.FairScoreFromJSON(created.ResidualScore)
	if err != nil {
		t.Fatalf("created FAIR residual score: %v", err)
	}
	if residual.AnnualizedLossExposure != got.AnnualizedLossExposure {
		t.Fatalf("residual FAIR exposure = %v, want %v", residual.AnnualizedLossExposure, got.AnnualizedLossExposure)
	}
}

func TestCreate_FairAcceptsLegacyAliasesAndNormalizes(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "FAIR aliases",
		CategoryRef:   "financial",
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.5,"lm":40000}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	})
	if err != nil {
		t.Fatalf("Create FAIR aliases: %v", err)
	}
	// Postgres JSONB does not preserve key order or byte layout, so assert
	// the canonical shape semantically rather than as a raw string.
	got, err := risk.FairScoreFromJSON(created.InherentScore)
	if err != nil {
		t.Fatalf("parse canonical FAIR inherent_score %s: %v", created.InherentScore, err)
	}
	if got.LossEventFrequency != 1.5 || got.LossMagnitude != 40000 || got.AnnualizedLossExposure != 60000 {
		t.Fatalf("canonical FAIR inherent_score = %s", created.InherentScore)
	}
	var keys map[string]any
	if err := json.Unmarshal(created.InherentScore, &keys); err != nil {
		t.Fatalf("unmarshal persisted FAIR score: %v", err)
	}
	for _, alias := range []string{"lef", "lm"} {
		if _, present := keys[alias]; present {
			t.Fatalf("legacy alias %q survived normalization: %s", alias, created.InherentScore)
		}
	}
}

// ---- AC-3: treatment=mitigate requires linked controls ----

func TestCreate_MitigateRequiresLinkedControl(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "Needs mitigation",
		CategoryRef:   "operational",
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":4,"impact":3}`),
		Treatment:     dbx.RiskTreatmentMitigate,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation error; got %v", err)
	}
}

func TestCreate_MitigateWithLinkedControlSucceeds(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	ctrl := seedControl(t, migrate, tenant)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:            "test-actor",
		Title:            "Mitigated",
		CategoryRef:      "operational",
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

// ---- #2371: create cannot approve an acceptance ----

// TestCreate_RefusesApprovedAcceptance: an acceptance is approved only
// through propose then approve, so a create naming an approved status, an
// accepter or an expiry is refused even with every field present.
func TestCreate_RefusesApprovedAcceptance(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	until := time.Now().AddDate(0, 6, 0)
	for name, in := range map[string]risk.CreateInput{
		"approved with accepter and expiry": {
			AcceptanceStatus: dbx.RiskAcceptanceStatusApproved,
			AcceptedUntil:    &until,
			Accepter:         "ciso@example.com",
		},
		"proposed with accepter": {
			AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
			Accepter:         "ciso@example.com",
		},
		"proposed with expiry": {
			AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
			AcceptedUntil:    &until,
		},
	} {
		in.Title = "Accepted"
		in.Actor = "test-actor"
		in.CategoryRef = "regulatory"
		in.Methodology = dbx.RiskMethodologyNist80030
		in.InherentScore = []byte(`{"likelihood":1,"impact":2}`)
		in.Treatment = dbx.RiskTreatmentAccept
		if _, err := store.Create(ctx, in); !risk.IsTreatmentValidation(err) {
			t.Errorf("%s: want treatment validation; got %v", name, err)
		}
	}
}

// ---- #1746: untreated default + acceptance status ----

func TestCreate_OmittedTreatmentStoresUntreated(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "Intake risk",
		CategoryRef:   "operational",
		InherentScore: []byte(`{"likelihood":2,"impact":2}`),
	})
	if err != nil {
		t.Fatalf("Create without treatment: %v", err)
	}
	if created.Treatment != dbx.RiskTreatmentUntreated {
		t.Fatalf("treatment = %q; want untreated", created.Treatment)
	}
	if created.AcceptanceStatus != "" {
		t.Fatalf("acceptance_status = %q; want empty", created.AcceptanceStatus)
	}
}

func TestCreate_ProposedAcceptNeedsNoAccepterOrExpiry(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	until := time.Now().UTC().AddDate(0, 3, 0)
	created, err := store.Create(ctx, risk.CreateInput{
		Actor:            "test-actor",
		Title:            "Pending acceptance",
		CategoryRef:      "operational",
		InherentScore:    []byte(`{"likelihood":2,"impact":2}`),
		Treatment:        dbx.RiskTreatmentAccept,
		AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
		Proposer:         "creator",
		Justification:    "Compensated by the edge filter.",
		ProposedUntil:    until,
	})
	if err != nil {
		t.Fatalf("Create proposed accept: %v", err)
	}
	got, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AcceptanceStatus != dbx.RiskAcceptanceStatusProposed {
		t.Fatalf("acceptance_status = %q; want proposed", got.AcceptanceStatus)
	}
	// #2371: the creator is the proposer the approval rule compares with.
	if got.Acceptance.ProposedBy != "creator" || got.Acceptance.ProposedAt == nil {
		t.Fatalf("acceptance = %+v; want proposed by the creator, with a time", got.Acceptance)
	}
	// #2391: the proposal's terms are recorded as the propose transition
	// records them.
	if got.Acceptance.Justification != "Compensated by the edge filter." ||
		got.Acceptance.ProposedUntil == nil ||
		got.Acceptance.ProposedUntil.Format(time.DateOnly) != until.Format(time.DateOnly) {
		t.Fatalf("acceptance = %+v; want the justification and proposed expiry", got.Acceptance)
	}

	// A proposed acceptance with no proposer, justification or future
	// proposed expiry is refused, as the propose transition refuses it
	// (#2371, #2391).
	for name, in := range map[string]risk.CreateInput{
		"no proposer":      {Justification: "j", ProposedUntil: until},
		"no justification": {Proposer: "creator", ProposedUntil: until},
		"no expiry":        {Proposer: "creator", Justification: "j"},
		"past expiry":      {Proposer: "creator", Justification: "j", ProposedUntil: time.Now().UTC().AddDate(0, 0, -1)},
	} {
		in.Title = "Pending, " + name
		in.Actor = "test-actor"
		in.CategoryRef = "operational"
		in.InherentScore = []byte(`{"likelihood":2,"impact":2}`)
		in.Treatment = dbx.RiskTreatmentAccept
		in.AcceptanceStatus = dbx.RiskAcceptanceStatusProposed
		if _, err := store.Create(ctx, in); !risk.IsTransitionInput(err) {
			t.Errorf("%s: want transition input error; got %v", name, err)
		}
	}

	// Approved without accepter / expiry: the domain layer rejects it.
	_, err = store.Create(ctx, risk.CreateInput{
		Actor:            "test-actor",
		Title:            "Approved, unsigned",
		CategoryRef:      "operational",
		InherentScore:    []byte(`{"likelihood":2,"impact":2}`),
		Treatment:        dbx.RiskTreatmentAccept,
		AcceptanceStatus: dbx.RiskAcceptanceStatusApproved,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation for unsigned approved accept; got %v", err)
	}
}

// TestDB_AcceptanceChecks pins the DB-side guards on the app role, bypassing
// the domain layer: an approved acceptance needs accepter + accepted_until,
// and acceptance_status is set exactly when treatment is accept.
func TestDB_AcceptanceChecks(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	ctx := dbtest.WithTenantCtx(t, tenant)

	insert := func(treatment, status string) error {
		conn, err := app.Acquire(ctx)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		defer conn.Release()
		tx, err := conn.Begin(ctx)
		if err != nil {
			t.Fatalf("begin: %v", err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SELECT set_config('app.current_tenant', $1, true)", tenant); err != nil {
			t.Fatalf("set tenant: %v", err)
		}
		var st any
		if status != "" {
			st = status
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO risks (id, tenant_id, title, category_id, treatment, acceptance_status)
			VALUES ($1, $2, 'db check', risk_category_id($2, 'operational'), $3::risk_treatment, $4::risk_acceptance_status)
		`, uuid.New(), tenant, treatment, st)
		return err
	}

	if err := insert("accept", "proposed"); err != nil {
		t.Fatalf("proposed accept without fields rejected: %v", err)
	}
	if err := insert("accept", "approved"); !isCheckViolation(err) {
		t.Fatalf("approved accept without fields: want check violation, got %v", err)
	}
	if err := insert("accept", ""); !isCheckViolation(err) {
		t.Fatalf("accept without status: want check violation, got %v", err)
	}
	if err := insert("untreated", "proposed"); !isCheckViolation(err) {
		t.Fatalf("status on untreated: want check violation, got %v", err)
	}
	if err := insert("untreated", ""); err != nil {
		t.Fatalf("untreated rejected: %v", err)
	}
}

func isCheckViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23514"
}

// ---- AC-5: treatment=transfer requires instrument_reference ----

func TestCreate_TransferRequiresInstrumentReference(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	_, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "Insured",
		CategoryRef:   "financial",
		Methodology:   dbx.RiskMethodologyNist80030,
		InherentScore: []byte(`{"likelihood":3,"impact":3}`),
		Treatment:     dbx.RiskTreatmentTransfer,
	})
	if !risk.IsTreatmentValidation(err) {
		t.Fatalf("want treatment validation (missing instrument_reference); got %v", err)
	}

	created, err := store.Create(ctx, risk.CreateInput{
		Actor:               "test-actor",
		Title:               "Insured",
		CategoryRef:         "financial",
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
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	// One nist_800_30 + accept
	if _, err := store.Create(ctx, risk.CreateInput{
		Actor: "test-actor",
		Title: "A", CategoryRef: "privacy",
		Methodology:      dbx.RiskMethodologyNist80030,
		InherentScore:    []byte(`{"likelihood":2,"impact":2}`),
		Treatment:        dbx.RiskTreatmentAccept,
		AcceptanceStatus: dbx.RiskAcceptanceStatusProposed,
		Proposer:         "creator",
		Justification:    "Low exposure.",
		ProposedUntil:    time.Now().UTC().AddDate(0, 3, 0),
	}); err != nil {
		t.Fatalf("A: %v", err)
	}
	// One fair + avoid
	if _, err := store.Create(ctx, risk.CreateInput{
		Actor: "test-actor",
		Title: "B", CategoryRef: "financial",
		Methodology:   dbx.RiskMethodologyFair,
		InherentScore: []byte(`{"lef":1.0,"lm":50000}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("B: %v", err)
	}
	// One qualitative_5x5 + avoid
	if _, err := store.Create(ctx, risk.CreateInput{
		Actor: "test-actor",
		Title: "C", CategoryRef: "availability",
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

	got, err = store.List(ctx, risk.ListFilter{Categories: []string{"availability"}})
	if err != nil {
		t.Fatalf("List category=availability: %v", err)
	}
	if len(got) != 1 || got[0].Title != "C" {
		t.Fatalf("category filter expected [C], got %v", titles(got))
	}
}

// ---- AC-7: heatmap ----

func TestHeatmap_CountsBy5x5Methodology(t *testing.T) {
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)
	tenant := freshTenant(t, migrate)
	store := risk.NewStore(app)
	ctx := dbtest.WithTenantCtx(t, tenant)

	// Two nist_800_30 risks in the same bucket (3,4).
	for i := 0; i < 2; i++ {
		if _, err := store.Create(ctx, risk.CreateInput{
			Actor:         "test-actor",
			Title:         "n",
			CategoryRef:   "confidentiality",
			Methodology:   dbx.RiskMethodologyNist80030,
			InherentScore: []byte(`{"likelihood":3,"impact":4}`),
			Treatment:     dbx.RiskTreatmentAvoid,
		}); err != nil {
			t.Fatalf("Create nist %d: %v", i, err)
		}
	}
	// One qualitative_5x5 in bucket (5,5).
	if _, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "q",
		CategoryRef:   "availability",
		Methodology:   dbx.RiskMethodologyQualitative5x5,
		InherentScore: []byte(`{"likelihood":5,"impact":5}`),
		Treatment:     dbx.RiskTreatmentAvoid,
	}); err != nil {
		t.Fatalf("Create qualitative: %v", err)
	}
	// One FAIR — must NOT appear in heatmap.
	if _, err := store.Create(ctx, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "f",
		CategoryRef:   "financial",
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
	migrate := dbtest.NewMigratePool(t)
	app := dbtest.NewAppPool(t)

	tenantA := freshTenant(t, migrate)
	tenantB := freshTenant(t, migrate)
	store := risk.NewStore(app)

	ctxA := dbtest.WithTenantCtx(t, tenantA)
	ctxB := dbtest.WithTenantCtx(t, tenantB)

	created, err := store.Create(ctxA, risk.CreateInput{
		Actor:         "test-actor",
		Title:         "A's risk",
		CategoryRef:   "regulatory",
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
	if err := store.Delete(ctxB, created.ID, "test-actor"); !errors.Is(err, risk.ErrNotFound) {
		t.Fatalf("tenant B Delete expected ErrNotFound; got %v", err)
	}

	// Tenant A still sees + can delete its own row.
	if _, err := store.Get(ctxA, created.ID); err != nil {
		t.Fatalf("tenant A Get own: %v", err)
	}
	if err := store.Delete(ctxA, created.ID, "test-actor"); err != nil {
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
