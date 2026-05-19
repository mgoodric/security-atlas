//go:build integration

// Slice 150 — empty-set robustness integration test for the freshnessdrift
// HTTP API. Reproduces the operator-reported v1.10.0 fresh-install 500 on
// GET /v1/controls/drift before the fix, and pins the post-fix invariant:
// every list/aggregate endpoint MUST return 200 with an empty envelope on a
// zero-row DB, never a 500. See docs/issues/150-empty-set-robustness-audit-
// across-list-endpoints.md AC-2 + AC-8 (drift) and the convention added to
// CONTRIBUTING.md.
//
// The test piggy-backs on the slice-016 harness in integration_test.go
// (admin/app pool helpers, freshTenant, testServer). No new helpers
// introduced — the test is intentionally small.

package freshnessdrift_test

import (
	"net/http"
	"testing"
)

// TestDrift_EmptyTenant_Returns200EmptyEnvelope is the slice-150 reproducer
// for the operator-reported "Could not load this panel · 500 Internal
// Server Error" on the recent-drift panel of a fresh install. Asserts:
//
//   - 200 OK (NOT 500)
//   - body.delta == 0 (no snapshots -> zero delta)
//   - body.flipped_out_count == 0
//   - body.flipped_out is the JSON literal [] (an array, not null)
//
// The flipped_out:null vs flipped_out:[] distinction matters: the frontend
// dashboard panel iterates the array and treats null as a malformed
// response. The handler already initializes `flips` to a 0-cap make() —
// but the asserted shape pins the contract.
func TestDrift_EmptyTenant_Returns200EmptyEnvelope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/controls/drift?since=7d")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
	}
	if got, want := body["flipped_out_count"], float64(0); got != want {
		t.Errorf("flipped_out_count = %v, want %v", got, want)
	}
	flips, ok := body["flipped_out"].([]any)
	if !ok {
		t.Fatalf("flipped_out is not a JSON array: %T (%v)", body["flipped_out"], body["flipped_out"])
	}
	if len(flips) != 0 {
		t.Errorf("flipped_out length = %d, want 0", len(flips))
	}
	if got, want := body["delta"], float64(0); got != want {
		t.Errorf("delta = %v, want %v", got, want)
	}
}

// TestFreshness_EmptyTenant_Returns200EmptyEnvelope is the slice-150
// companion for GET /v1/evidence/freshness on a fresh install. Same
// contract: 200 with `buckets: []` + `total: 0` + `total_stale: 0`.
func TestFreshness_EmptyTenant_Returns200EmptyEnvelope(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	resp, body := get(t, env, "/v1/evidence/freshness?bucket=class")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
	}
	buckets, ok := body["buckets"].([]any)
	if !ok {
		t.Fatalf("buckets is not a JSON array: %T (%v)", body["buckets"], body["buckets"])
	}
	if len(buckets) != 0 {
		t.Errorf("buckets length = %d, want 0", len(buckets))
	}
	if got, want := body["total"], float64(0); got != want {
		t.Errorf("total = %v, want %v", got, want)
	}
	if got, want := body["total_stale"], float64(0); got != want {
		t.Errorf("total_stale = %v, want %v", got, want)
	}
}
