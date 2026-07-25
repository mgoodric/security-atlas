//go:build integration

// Slice 502 — integration tests for the AI evidence-summarization surface. Real
// Postgres + the real evidencesummary.Store (RLS-backed evidence-set assembly +
// citation resolution) + the llm.StubClient (NO live Ollama in CI — the stub is
// the slice-498 CI seam every AI-assist surface reuses). The stub lets us craft
// a VALID draft (cites real tenant-owned ids) or a FABRICATED draft (cites a
// non-existent / cross-tenant id) and assert the deterministic
// citation-validation + suppression + cross-tenant behavior.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/evidencesummary/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage (AC-8/9/10):
//
//	AC-8   a valid summary with resolvable citations is NOT suppressed
//	AC-9   a summary citing a non-existent id is suppressed -> evidence list only
//	AC-10  a Tenant-B summary cannot cite/quote a Tenant-A record (cross-tenant)
package evidencesummary_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/llm"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// ----- harness -----

// freshTenant returns a new tenant id + registers cleanup of the rows this
// slice's tests create, in FK order, via the slice-435/742 dbtest harness.
func freshTenant(t *testing.T, admin *pgxpool.Pool) string {
	t.Helper()
	return dbtest.SeedTenant(t, admin,
		"evidence_records",
		"controls",
	)
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
		VALUES ($1, $2, 'slice 502 evidence-summary test control', 'AAA', 'automated',
		        $3, '[]'::jsonb, 'true', 'quarterly')
	`, ctrlID, tenant, "test-bundle-502-"+ctrlID.String()); err != nil {
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
		"hash-502-"+id.String()[:8]); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	return id
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
func stubWith(store *evidencesummary.Store, draft string) *evidencesummary.Service {
	client := &llm.StubClient{Result: llm.GenerateResult{
		Text:          draft,
		ModelName:     "stub-model",
		ModelVersion:  "1",
		ModelProvider: "stub",
	}}
	return evidencesummary.NewService(store, client, store)
}

// ----- AC-8: valid summary with resolvable citations renders -----

func TestSummarize_ValidCitationsRender(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-30 * 24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	evID := seedEvidence(t, admin, tenant, ctrlID, observed)

	store := evidencesummary.NewStore(app)
	draft := fmt.Sprintf(
		"Control (%s) has a recent access-review completion (%s) that passed.",
		ctrlID, evID)
	svc := stubWith(store, draft)

	sum, err := svc.Summarize(tenantCtx(t, tenant), ctrlID)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if sum.Suppressed {
		t.Fatalf("expected summary rendered, got suppressed (reason %q)", sum.Reason)
	}
	if sum.Text != draft {
		t.Errorf("summary text not surfaced verbatim")
	}
	if len(sum.Citations) != 2 {
		t.Fatalf("expected 2 resolved citations, got %d: %+v", len(sum.Citations), sum.Citations)
	}
	// AC-1: the deterministic bounded set carries the live evidence.
	if sum.EvidenceSet.ControlID != ctrlID {
		t.Error("evidence-set control id mismatch")
	}
	if len(sum.EvidenceSet.Records) != 1 || sum.EvidenceSet.TotalCount != 1 {
		t.Errorf("want 1 of 1 evidence record, got %d of %d",
			len(sum.EvidenceSet.Records), sum.EvidenceSet.TotalCount)
	}
}

// ----- AC-1 / P0-502-8: corpus is bounded to top-N most-recent -----

func TestSummarize_BoundedTopN(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	ctrlID := seedControl(t, admin, tenant)
	// Seed more than the bound; the newest should win.
	total := evidencesummary.MaxCitedExcerpts + 5
	var newest uuid.UUID
	for i := 0; i < total; i++ {
		observed := time.Now().UTC().Add(-time.Duration(i) * time.Hour)
		id := seedEvidence(t, admin, tenant, ctrlID, observed)
		if i == 0 {
			newest = id
		}
	}

	store := evidencesummary.NewStore(app)
	set, err := store.EvidenceSet(tenantCtx(t, tenant), ctrlID)
	if err != nil {
		t.Fatalf("EvidenceSet: %v", err)
	}
	if len(set.Records) != evidencesummary.MaxCitedExcerpts {
		t.Fatalf("expected bounded set of %d, got %d (P0-502-8)", evidencesummary.MaxCitedExcerpts, len(set.Records))
	}
	if set.TotalCount != total {
		t.Errorf("TotalCount = %d, want %d (full live count for the N-of-M label)", set.TotalCount, total)
	}
	// The most-recent record is in the bounded set (observed_at DESC ordering).
	if set.Records[0].EvidenceID != newest {
		t.Error("bounded set must be the most-recent records (recency bound)")
	}
}

// ----- AC-9: a non-existent-id citation is suppressed -> evidence list only ---

func TestSummarize_NonExistentCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-30 * 24 * time.Hour)
	ctrlID := seedControl(t, admin, tenant)
	seedEvidence(t, admin, tenant, ctrlID, observed)

	store := evidencesummary.NewStore(app)
	// The model fabricates an evidence id that does not exist anywhere.
	fabricated := uuid.New()
	draft := fmt.Sprintf("Control (%s) shows record (%s).", ctrlID, fabricated)
	svc := stubWith(store, draft)

	sum, err := svc.Summarize(tenantCtx(t, tenant), ctrlID)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !sum.Suppressed {
		t.Fatal("expected suppression for a fabricated-id citation (AC-9 / P0-502-1)")
	}
	if sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", sum.Reason, evidencesummary.ReasonUnresolvedCitation)
	}
	if sum.Text != "" {
		t.Error("suppressed summary must not surface text")
	}
	// AC-7: the deterministic evidence list STILL renders.
	if sum.EvidenceSet.ControlID != ctrlID || len(sum.EvidenceSet.Records) != 1 {
		t.Error("evidence list must still render on suppression")
	}
}

// ----- AC-10: cross-tenant isolation (THE headline, P0-502-2) -----
//
// Tenant A owns a control + evidence. The model (for a Tenant-B request)
// produces a draft that cites/quotes Tenant A's evidence id. Under Tenant B's
// RLS context the Tenant-A id is invisible, so the resolver cannot confirm it
// tenant-owned and the summary is SUPPRESSED. A Tenant-B summary can never cite
// a Tenant-A record (threat-model I, P0-502-2).
func TestSummarize_CrossTenantCitationSuppressed(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)

	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	observed := time.Now().UTC().Add(-30 * 24 * time.Hour)
	// Tenant A's records.
	ctrlA := seedControl(t, admin, tenantA)
	evA := seedEvidence(t, admin, tenantA, ctrlA, observed)

	// Tenant B's own control + evidence (so B has a real set to summarize).
	ctrlB := seedControl(t, admin, tenantB)
	seedEvidence(t, admin, tenantB, ctrlB, observed)

	store := evidencesummary.NewStore(app)
	// The model, serving Tenant B's request for ctrlB, leaks Tenant A's
	// evidence id evA into the draft.
	draft := fmt.Sprintf(
		"Control (%s) summary referencing cross-tenant record (%s).", ctrlB, evA)
	svc := stubWith(store, draft)

	sum, err := svc.Summarize(tenantCtx(t, tenantB), ctrlB)
	if err != nil {
		t.Fatalf("Summarize: %v", err)
	}
	if !sum.Suppressed {
		t.Fatal("CROSS-TENANT LEAK: a Tenant-B summary cited a Tenant-A record without suppression (AC-10 / P0-502-2)")
	}
	if sum.Reason != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("reason = %q, want %q", sum.Reason, evidencesummary.ReasonUnresolvedCitation)
	}
	if sum.Text != "" {
		t.Error("cross-tenant citation must not surface any text or quote")
	}
	// Prove the leaked id is genuinely invisible to Tenant B at the resolver
	// level (defense-in-depth on the AC-10 mechanism).
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantB), evA); rerr != nil {
		t.Fatalf("Resolve(tenantB, evA): %v", rerr)
	} else if ok {
		t.Fatal("CROSS-TENANT LEAK: Tenant B resolved Tenant A's evidence id")
	}
	// And prove Tenant B's evidence set never contains a Tenant-A record.
	setB, err := store.EvidenceSet(tenantCtx(t, tenantB), ctrlB)
	if err != nil {
		t.Fatalf("EvidenceSet(tenantB): %v", err)
	}
	for _, r := range setB.Records {
		if r.EvidenceID == evA {
			t.Fatal("CROSS-TENANT LEAK: Tenant B's evidence set contains a Tenant-A record")
		}
	}
	// Sanity: Tenant A CAN resolve its own id (the resolver works at all).
	if _, ok, rerr := store.Resolve(tenantCtx(t, tenantA), evA); rerr != nil {
		t.Fatalf("Resolve(tenantA, evA): %v", rerr)
	} else if !ok {
		t.Fatal("Tenant A could not resolve its own evidence id — resolver is broken")
	}
}

// ----- not-found control: ErrNoControl -----

func TestSummarize_UnknownControlNotFound(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)

	store := evidencesummary.NewStore(app)
	svc := stubWith(store, "ignored")
	_, err := svc.Summarize(tenantCtx(t, tenant), uuid.New())
	if err == nil {
		t.Fatal("expected ErrNoControl for an unknown control id")
	}
}
