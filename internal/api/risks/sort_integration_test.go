//go:build integration

// Slice 066 — integration test for AC-3: the additive ?sort=residual,age
// capability on GET /v1/risks. The program dashboard's "top risks aging"
// panel ranks by residual-score magnitude descending, then risk age
// ascending (oldest in treatment first).
//
// Run with:
//
//	go test -tags=integration -race ./internal/api/risks/...
//
// Requires DATABASE_URL_APP (atlas_app role) and DATABASE_URL (admin role).
//
// Coverage:
//
//	ISC-20  GET /v1/risks?sort=residual,age orders by residual desc then age asc
//	(plus)  ?sort= is additive — omitting it keeps the default order;
//	        an unrecognized ?sort= value is a 400.

package risks_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// seedRiskWithResidual inserts a risk with an explicit residual_score JSONB
// and an explicit created_at, so the AC-3 sort can be asserted
// deterministically. residual magnitude = likelihood * impact.
func seedRiskWithResidual(t *testing.T, admin *pgxpool.Pool, tenant, title string, likelihood, impact int, createdAt time.Time) uuid.UUID {
	t.Helper()
	riskID := uuid.New()
	residual, _ := json.Marshal(map[string]int{"likelihood": likelihood, "impact": impact})
	if _, err := admin.Exec(t.Context(), `
		INSERT INTO risks (
			id, tenant_id, title, description, category, methodology,
			inherent_score, treatment, treatment_owner, residual_score,
			created_at
		)
		VALUES ($1, $2, $3, '', 'operational', 'nist_800_30',
		        '{"likelihood":5,"impact":5}'::jsonb, 'mitigate', 'owner',
		        $4::jsonb, $5)
	`, riskID, tenant, title, string(residual), createdAt); err != nil {
		t.Fatalf("seed risk with residual: %v", err)
	}
	return riskID
}

// ===== ISC-20: ?sort=residual,age orders by residual desc then age asc =====

func TestListRisks_SortResidualAge(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	// Three risks:
	//   "high-old"  residual 4*5 = 20, created 100 days ago
	//   "high-new"  residual 5*4 = 20, created  10 days ago (same magnitude,
	//                                            newer -> ranks AFTER high-old)
	//   "low"       residual 1*2 =  2, created  50 days ago
	// Expected order: high-old, high-new, low.
	seedRiskWithResidual(t, admin, tenant, "high-old", 4, 5, now.Add(-100*24*time.Hour))
	seedRiskWithResidual(t, admin, tenant, "high-new", 5, 4, now.Add(-10*24*time.Hour))
	seedRiskWithResidual(t, admin, tenant, "low", 1, 2, now.Add(-50*24*time.Hour))

	resp, body := get(t, env, "/v1/risks?sort=residual,age")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks?sort=residual,age: status %d, want 200", resp.StatusCode)
	}
	rows, _ := body["risks"].([]any)
	if len(rows) != 3 {
		t.Fatalf("ISC-20: expected 3 risks, got %d", len(rows))
	}
	gotOrder := make([]string, len(rows))
	for i, r := range rows {
		gotOrder[i] = r.(map[string]any)["title"].(string)
	}
	wantOrder := []string{"high-old", "high-new", "low"}
	for i := range wantOrder {
		if gotOrder[i] != wantOrder[i] {
			t.Fatalf("ISC-20: sort order = %v, want %v", gotOrder, wantOrder)
		}
	}
}

// (plus): ?sort= is additive — omitting it keeps the slice-019 default
// order (created_at DESC). An unrecognized ?sort= is a 400.

func TestListRisks_SortIsAdditive(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	app := openPool(t, appDSN(t))
	tenant := freshTenant(t, admin)
	env := testServer(t, app, tenant)

	now := time.Now().UTC()
	// Default order is created_at DESC: newest first.
	seedRiskWithResidual(t, admin, tenant, "older", 1, 1, now.Add(-30*24*time.Hour))
	seedRiskWithResidual(t, admin, tenant, "newer", 5, 5, now.Add(-1*24*time.Hour))

	// No ?sort= -> default order, newest first (created_at DESC).
	resp, body := get(t, env, "/v1/risks")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/risks (no sort): status %d", resp.StatusCode)
	}
	rows, _ := body["risks"].([]any)
	if len(rows) != 2 {
		t.Fatalf("expected 2 risks, got %d", len(rows))
	}
	if title := rows[0].(map[string]any)["title"].(string); title != "newer" {
		t.Fatalf("default order: first risk = %q, want newer (created_at DESC)", title)
	}

	// Unrecognized ?sort= -> 400.
	resp, _ = get(t, env, "/v1/risks?sort=bogus")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /v1/risks?sort=bogus: status %d, want 400", resp.StatusCode)
	}
}
