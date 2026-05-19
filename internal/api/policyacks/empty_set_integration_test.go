//go:build integration

// Slice 150 — empty-set robustness integration test for the slice-023
// policy-acknowledgments HTTP API. On a fresh install, the dashboard
// renders the bootstrap-owner credential against GET
// /v1/me/acknowledgments. The bootstrap credential's UserID is a
// `key_*` string (the credstore IssueOwner contract — see
// internal/api/credstore/credstore.go), not a UUID. Before slice 150
// the handler called PendingForUser which then called uuid.Parse on
// the non-UUID id and returned an error that the handler bubbled up
// as a 500. This test pins the post-fix invariant: a non-UUID UserID
// is a service-account marker and the response is 200 with an empty
// pending list.
//
// See docs/issues/150-empty-set-robustness-audit-across-list-endpoints.md.

package policyacks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
)

func emptySetAppDSN(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL_APP")
	if v == "" {
		t.Skip("DATABASE_URL_APP not set; skipping integration test")
	}
	return v
}

func emptySetOpenPool(t *testing.T, dsn string) *pgxpool.Pool {
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

// TestMyAcknowledgments_BootstrapCred_Returns200EmptyEnvelope is the
// slice-150 reproducer for the operator-reported fresh-install bug:
// the GET /v1/me/acknowledgments call from the dashboard panel was
// returning 500 because the bootstrap-owner credential carries a
// non-UUID UserID. Post-fix the handler returns 200 with
// `{pending: [], count: 0, window_seconds: <int>}`.
func TestMyAcknowledgments_BootstrapCred_Returns200EmptyEnvelope(t *testing.T) {
	app := emptySetOpenPool(t, emptySetAppDSN(t))
	srv := api.New(api.Config{})
	srv.AttachDB(app)
	tenant := uuid.NewString()
	_, bearer, err := srv.IssueBootstrapOwnerCredential(tenant, []string{"owner"})
	if err != nil {
		t.Fatalf("IssueBootstrapOwnerCredential: %v", err)
	}
	ts := httptest.NewServer(srv.HTTPHandlerForTests())
	t.Cleanup(ts.Close)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/me/acknowledgments", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/me/acknowledgments: %v", err)
	}
	defer resp.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%v", resp.StatusCode, body)
	}
	pending, ok := body["pending"].([]any)
	if !ok {
		t.Fatalf("pending is not a JSON array: %T (%v)", body["pending"], body["pending"])
	}
	if len(pending) != 0 {
		t.Errorf("pending length = %d, want 0", len(pending))
	}
	if got, want := body["count"], float64(0); got != want {
		t.Errorf("count = %v, want %v", got, want)
	}
	if ws, ok := body["window_seconds"].(float64); !ok || ws <= 0 {
		t.Errorf("window_seconds = %v (ok=%v), want positive number", body["window_seconds"], ok)
	}
}
