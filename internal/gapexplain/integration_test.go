//go:build integration

// Slice 444 — integration tests for the AI gap-explanation surface. Real
// Postgres + the real gapexplain.Store (RLS-backed rollup assembly + citation
// resolution) + the llm.StubClient (NO live Ollama in CI — the stub is the
// slice-498 CI seam every AI-assist surface reuses). The stub lets us craft a
// VALID draft (cites real tenant-owned ids) or a FABRICATED draft (cites a
// non-existent / cross-tenant id) and assert the deterministic
// citation-validation + suppression + cross-tenant behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/gapexplain/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (AC-8/9/10):
//
//	AC-8   a valid explanation with resolvable citations is NOT suppressed
//	AC-9   an explanation citing a non-existent id is suppressed -> rollup only
//	AC-10  a Tenant-B explanation cannot cite a Tenant-A record (cross-tenant)
package gapexplain_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/gapexplain"
	"github.com/mgoodric/security-atlas/internal/llm"
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
	t.Cleanup(func() { pool.Close() })
	return pool
}

// freshTenant returns a new tenant id + registers cleanup of the rows this
// slice's tests create.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	tenant := uuid.NewString()
	t.Cleanup(func() {
		ctx := context.Background()
		for _, stmt := range []string{
			`DELETE FROM evidence_freshness WHERE tenant_id = $1`,
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

// seedControl inserts a minimal control row (mirrors the slice-064 harness).
func seedControl(t *testing.T, admin *pgxpool.Pool, tenant string) uuid.UUID {
	t.Helper()
	ctrlID := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO controls (
			id, tenant_id, title, control_family, implementation_type,
			bundle_id, evidence_queries, applicability_expr, freshness_class
		)
		VALUES ($1, $2, 'slice 444 gap-explanation test control', 'AAA', 'automated',
		        $3, '[]'::jsonb, 'true', 'quarterly')
	`, ctrlID, tenant, "test-bundle-444-"+ctrlID.String()); err != nil {
		t.Fatalf("seed control: %v", err)
	}
	return ctrlID
}

// seedEvidence inserts one evidence_records row (mirrors the slice-064 harness).
func seedEvidence(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, observedAt time.Time) uuid.UUID {
	t.Helper()
	id := uuid.New()
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_records (
			id, tenant_id, control_id, control_ref, observed_at, ingested_at,
			provenance, result, payload, hash, evidence_kind
		)
		VALUES ($1, $2, $3, $4, $5, now(), $6, 'pass', '{}'::jsonb, $7, 'access_review.completion')
	`, id, tenant, ctrlID, ctrlID.String(), observedAt,
		`{"connector_id":"test-connector"}`,
		"hash-444-"+id.String()[:8]); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
}

// seedStaleFreshness inserts an evidence_freshness row marking the control
// stale (valid_until in the past) so the rollup has a real gap signal.
func seedStaleFreshness(t *testing.T, admin *pgxpool.Pool, tenant string, ctrlID uuid.UUID, latest time.Time) {
	t.Helper()
	validUntil := latest.Add(-24 * time.Hour) // already expired
	if _, err := admin.Exec(context.Background(), `
		INSERT INTO evidence_freshness (
			id, tenant_id, control_id, freshness_class, latest_observed_at,
			valid_until, is_stale, evidence_count, refreshed_at
		)
		VALUES ($1, $2, $3, 'quarterly', $4, $5, true, 1, now())
	`, uuid.New(), tenant, ctrlID, latest, validUntil); err != nil {
		t.Fatalf("seed freshness: %v", err)
	}
}

// tenantCtx returns a context carrying the tenant GUC value (the Store's inTx
// resolves it and applies app.current_tenant under RLS).
func tenantCtx(t *testing.T, tenant string) context.Context {
	t.Helper()
	ctx, err := tenancy.WithTenant(context.Background(), tenant)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	return ctx
}

// stubWith builds a Service whose model returns the given crafted draft.
func stubWith(store *gapexplain.Store, draft string) *gapexplain.Service {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "stub",
	}}
	return gapexplain.NewService(store, client, store)
}

// ----- AC-8: valid explanation with resolvable citations renders -----

func TestExplain_ValidCitationsRender(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-120 * 24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	evID := seedEvidence(t, admin, tenant, ctrlID, observed)
	seedStaleFreshness(t, admin, tenant, ctrlID, observed)

	store := gapexplain.NewStore(app)
	draft := fmt.Sprintf(
		"Control (%s) is in a freshness gap: its most recent evidence (%s) is past the quarterly window.",
		ctrlID, evID)
	svc := stubWith(store, draft)

	exp, err := svc.Explain(tenantCtx(t, tenant), ctrlID)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if exp.Suppressed {
		t.Fatalf("expected explanation rendered, got suppressed (reason %q)", exp.Reason)
	}
	if exp.Text != draft {
		t.Errorf("explanation text not surfaced verbatim")
	}
	if len(exp.Citations) != 2 {
		t.Fatalf("expected 2 resolved citations, got %d: %+v", len(exp.Citations), exp.Citations)
	}
	// The rollup carries the deterministic gap signal.
	if !exp.Rollup.IsStale {
		t.Error("rollup should report the control stale")
	}
	if exp.Rollup.ControlID != ctrlID {
		t.Error("rollup control id mismatch")
	}
}

// ----- AC-9: a non-existent-id citation is suppressed -> rollup only -----

func TestExplain_NonExistentCitationSuppressed(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))
	tenant := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-120 * 24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	seedEvidence(t, admin, tenant, ctrlID, observed)
	seedStaleFreshness(t, admin, tenant, ctrlID, observed)

	store := gapexplain.NewStore(app)
	// The model fabricates an evidence id that does not exist anywhere.
	fabricated := uuid.New()
	draft := fmt.Sprintf(
		"Control (%s) is stale; see record (%s).", ctrlID, fabricated)
	svc := stubWith(store, draft)

	exp, err := svc.Explain(tenantCtx(t, tenant), ctrlID)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !exp.Suppressed {
		t.Fatal("expected suppression for a fabricated-id citation (AC-9 / P0-444-1)")
	}
	if exp.Reason != gapexplain.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", exp.Reason, gapexplain.ReasonUnresolvedCitation)
	}
	if exp.Text != "" {
		t.Error("suppressed explanation must not surface text")
	}
	// AC-7: the deterministic rollup STILL renders.
	if exp.Rollup.ControlID != ctrlID || !exp.Rollup.IsStale {
		t.Error("rollup must still render on suppression")
	}
}

// ----- AC-10: cross-tenant isolation (THE headline) -----
//
// Tenant A owns a control + evidence. The model (for a Tenant-B request)
// produces a draft that cites Tenant A's evidence id. Under Tenant B's RLS
// context the Tenant-A id is invisible, so the resolver cannot confirm it
// tenant-owned and the explanation is SUPPRESSED. A Tenant-B explanation can
// never cite a Tenant-A record (threat-model I, P0-444-2).
func TestExplain_CrossTenantCitationSuppressed(t *testing.T) {
	app := openPool(t, appDSN(t))
	admin := openPool(t, adminDSN(t))

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-120 * 24 * time.Hour)
	// Tenant A's records.
	ctrlA := seedControl(t, admin, tenantA)
	evA := seedEvidence(t, admin, tenantA, ctrlA, observed)
	seedStaleFreshness(t, admin, tenantA, ctrlA, observed)

	// Tenant B's own control (so B has a real rollup to explain).
	ctrlB := seedControl(t, admin, tenantB)
	seedEvidence(t, admin, tenantB, ctrlB, observed)
	seedStaleFreshness(t, admin, tenantB, ctrlB, observed)

	store := gapexplain.NewStore(app)
	// The model, serving Tenant B's request for ctrlB, leaks Tenant A's
	// evidence id evA into the draft.
	draft := fmt.Sprintf(
		"Control (%s) is stale; cross-tenant record (%s).", ctrlB, evA)
	svc := stubWith(store, draft)

	exp, err := svc.Explain(tenantCtx(t, tenantB), ctrlB)
	if err != nil {
		t.Fatalf("Explain: %v", err)
	}
	if !exp.Suppressed {
		t.Fatal("CROSS-TENANT LEAK: a Tenant-B explanation cited a Tenant-A record without suppression (AC-10 / P0-444-2)")
	}
	if exp.Reason != gapexplain.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", exp.Reason, gapexplain.ReasonUnresolvedCitation)
	}
	if exp.Text != "" {
		t.Error("cross-tenant citation must not surface any text")
	}
	// And prove the leaked id is genuinely invisible to Tenant B at the
	// resolver level (defense-in-depth on the AC-10 mechanism).
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantB), evA); rerr != nil {
		t.Fatalf("Resolve(tenantB, evA): %v", rerr)
	} else if ok {
		t.Fatal("CROSS-TENANT LEAK: Tenant B resolved Tenant A's evidence id")
	}
	// Sanity: Tenant A CAN resolve its own id (the resolver works at all).
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantA), evA); rerr != nil {
		t.Fatalf("Resolve(tenantA, evA): %v", rerr)
	} else if !ok {
		t.Fatal("Tenant A could not resolve its own evidence id — resolver is broken")
	}
}
