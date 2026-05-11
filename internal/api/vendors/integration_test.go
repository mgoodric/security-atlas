//go:build integration

// HTTP-level integration tests for slice 024. Real Postgres + real chi
// router. Mirrors internal/scope HTTP smoke pattern.

package vendors_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/api"
)

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
			`DELETE FROM vendor_scope_cells WHERE tenant_id = $1`,
			`DELETE FROM vendors WHERE tenant_id = $1`,
		} {
			if _, err := admin.Exec(ctx, stmt, tenant); err != nil {
				t.Logf("cleanup %s: %v", stmt, err)
			}
		}
	})
	return tenant
}

func setupHTTPServer(t *testing.T, tenant string) (*httptest.Server, string) {
	t.Helper()
	app := openPool(t, appDSN(t))
	srv := api.New(api.Config{RotationGrace: time.Hour})
	srv.AttachDB(app)
	_, bearer, err := srv.IssueBootstrapCredential(tenant)
	if err != nil {
		t.Fatalf("IssueBootstrapCredential: %v", err)
	}
	h := srv.HTTPHandlerForTests()
	if h == nil {
		t.Fatal("HTTPHandlerForTests nil")
	}
	ts := httptest.NewServer(h)
	t.Cleanup(func() {
		ts.Close()
		app.Close()
	})
	return ts, bearer
}

func doJSON(t *testing.T, method, url, bearer, body string) (*http.Response, []byte) {
	t.Helper()
	var reqBody io.Reader
	if body != "" {
		reqBody = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, reqBody)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do %s %s: %v", method, url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	bb, _ := io.ReadAll(resp.Body)
	return resp, bb
}

// AC-1 + AC-5 end-to-end: POST creates, returns the wire shape.
func TestHTTP_CreateVendor(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	body := `{
		"name": "Datadog",
		"domain": "datadoghq.com",
		"criticality": "high",
		"contract_start": "2025-01-01",
		"contract_end": "2026-01-01",
		"dpa_signed": true,
		"dpa_signed_at": "2025-01-15",
		"review_cadence": "annual",
		"last_review_date": "2026-04-01",
		"owner_user": "alice@example.com",
		"linked_sow_uri": "s3://contracts/datadog.pdf",
		"notes": "obs"
	}`
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("POST status = %d; body = %s", resp.StatusCode, payload)
	}
	var got struct {
		Vendor map[string]any `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Vendor["criticality"] != "high" {
		t.Fatalf("criticality = %v", got.Vendor["criticality"])
	}
	if got.Vendor["domain"] != "datadoghq.com" {
		t.Fatalf("domain = %v", got.Vendor["domain"])
	}
}

// AC-2 end-to-end: list with criticality filter.
func TestHTTP_ListVendors_FilterByCriticality(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	for _, c := range []string{"high", "high", "medium", "low"} {
		body := `{"name":"v-` + c + `-` + uuid.NewString()[:6] + `","criticality":"` + c + `","review_cadence":"annual"}`
		resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed POST: %d %s", resp.StatusCode, payload)
		}
	}
	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors?criticality=high", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d; body = %s", resp.StatusCode, payload)
	}
	var got struct {
		Vendors []map[string]any `json:"vendors"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Vendors) != 2 {
		t.Fatalf("want 2 high vendors; got %d (%v)", len(got.Vendors), got.Vendors)
	}
}

// AC-3 end-to-end: GET /v1/vendors/burndown returns on-time fractions.
func TestHTTP_Burndown(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	// Two on-time, one overdue, all high.
	for _, last := range []string{"2026-04-01", "2026-03-15", "2024-01-01"} {
		body := `{"name":"v-` + last + `","criticality":"high","review_cadence":"annual","last_review_date":"` + last + `"}`
		resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed: %d %s", resp.StatusCode, payload)
		}
	}
	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/burndown?criticality=high&as_of=2026-05-11", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("burndown status = %d; body = %s", resp.StatusCode, payload)
	}
	var got struct {
		Bands []struct {
			Criticality    string  `json:"criticality"`
			Total          int     `json:"total"`
			Overdue        int     `json:"overdue"`
			OnTimeFraction float64 `json:"on_time_fraction"`
		} `json:"bands"`
		Total struct {
			Total          int     `json:"total"`
			Overdue        int     `json:"overdue"`
			OnTimeFraction float64 `json:"on_time_fraction"`
		} `json:"total"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Bands) != 1 {
		t.Fatalf("want 1 band; got %d", len(got.Bands))
	}
	if got.Bands[0].Total != 3 || got.Bands[0].Overdue != 1 {
		t.Fatalf("totals wrong: %+v", got.Bands[0])
	}
	want := 2.0 / 3.0
	if abs(got.Bands[0].OnTimeFraction-want) > 1e-9 {
		t.Fatalf("OnTimeFraction = %v; want %v", got.Bands[0].OnTimeFraction, want)
	}
}

// AC-4 end-to-end: list?overdue=true returns only overdue rows.
func TestHTTP_ListVendors_OverdueOnly(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	seed := []struct{ name, last string }{
		{"recent", "2026-04-01"},
		{"stale", "2024-01-01"},
	}
	for _, s := range seed {
		body := `{"name":"` + s.name + `","criticality":"medium","review_cadence":"annual","last_review_date":"` + s.last + `"}`
		resp, _ := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, body)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("seed %s: %d", s.name, resp.StatusCode)
		}
	}
	// Plus one never-reviewed.
	resp, _ := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors",
		bearer, `{"name":"never","criticality":"medium","review_cadence":"annual"}`)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed never: %d", resp.StatusCode)
	}

	resp, payload := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors?overdue=true&as_of=2026-05-11", bearer, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %s", resp.StatusCode, payload)
	}
	var got struct {
		Vendors []map[string]any `json:"vendors"`
	}
	if err := json.Unmarshal(payload, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Vendors) != 2 {
		t.Fatalf("want 2 overdue rows; got %d", len(got.Vendors))
	}
}

// Bad criticality filter returns 400.
func TestHTTP_ListVendors_RejectsBadCriticality(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)
	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors?criticality=ultra", bearer, "")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400; got %d", resp.StatusCode)
	}
}

// GET on missing id returns 404.
func TestHTTP_GetVendor_NotFound(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)
	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors/"+uuid.NewString(), bearer, "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404; got %d", resp.StatusCode)
	}
}

// PATCH replaces the row.
func TestHTTP_UpdateVendor(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	body := `{"name":"v1","criticality":"low","review_cadence":"annual"}`
	resp, payload := doJSON(t, http.MethodPost, ts.URL+"/v1/vendors", bearer, body)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("seed: %d %s", resp.StatusCode, payload)
	}
	var seeded struct {
		Vendor struct {
			ID string `json:"id"`
		} `json:"vendor"`
	}
	if err := json.Unmarshal(payload, &seeded); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	patch := `{"name":"v1-edited","criticality":"high","review_cadence":"quarterly"}`
	resp, payload = doJSON(t, http.MethodPatch, ts.URL+"/v1/vendors/"+seeded.Vendor.ID, bearer, patch)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("PATCH status = %d; body = %s", resp.StatusCode, payload)
	}
	if !strings.Contains(string(payload), `"high"`) || !strings.Contains(string(payload), `"quarterly"`) {
		t.Fatalf("patch did not propagate: %s", payload)
	}
}

// Unauthenticated request is rejected at the middleware.
func TestHTTP_AuthRequired(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, _ := setupHTTPServer(t, tenant)
	resp, _ := doJSON(t, http.MethodGet, ts.URL+"/v1/vendors", "", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401; got %d", resp.StatusCode)
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}
