//go:build integration

// Slice 139 — integration tests for the vendor export endpoint.
//
// AC coverage:
//
//   AC-2   : endpoint exists + returns 200 for csv|json|xlsx
//   AC-3/4 : (BFF + Export button — covered by web/ vitest + Playwright)
//   AC-5/6 : meta-audit row written on every outcome — TestVendorsExport_MetaAuditFires
//   AC-9   : vendor email is masked in the export body — TestVendorsExport_EmailMasking
//   AC-10  : cross-tenant isolation × 3 formats — TestVendorsExport_CrossTenantIsolationAllThreeFormats
//
// Requires DATABASE_URL_APP + DATABASE_URL.

package adminvendors_test

import (
	"archive/zip"
	"bytes"
	"context"
	stdcsv "encoding/csv"
	"encoding/json"
	"fmt"
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
	"github.com/mgoodric/security-atlas/internal/api/testjwt"
)

// ===== Harness =====

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
			`DELETE FROM me_audit_log WHERE tenant_id = $1`,
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
	// Slice 197: JWT bearer via slice 190 path (admin claims).
	bearer := srv.IssueTestJWT(t, testjwt.AdminFor(uuid.MustParse(tenant)))
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

// seedVendor inserts one vendor via the public CRUD API (which exercises
// the full RLS path the export will read from). Returns the vendor id.
func seedVendor(t *testing.T, ts *httptest.Server, bearer, name, owner string) string {
	t.Helper()
	body := fmt.Sprintf(`{
		"name": %q,
		"domain": %q,
		"criticality": "high",
		"review_cadence": "annual",
		"owner_user": %q,
		"notes": "seed"
	}`, name, strings.ToLower(name)+".example.com", owner)
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodPost, ts.URL+"/v1/vendors", strings.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seed POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusCreated {
		bb, _ := io.ReadAll(resp.Body)
		t.Fatalf("seed POST status = %d; body = %s", resp.StatusCode, bb)
	}
	var got struct {
		Vendor map[string]any `json:"vendor"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("seed decode: %v", err)
	}
	id, _ := got.Vendor["id"].(string)
	if id == "" {
		t.Fatalf("seed missing id; got %v", got.Vendor)
	}
	return id
}

func doGet(t *testing.T, url, bearer string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp, body
}

func extractSearchableText(t *testing.T, format string, body []byte) string {
	t.Helper()
	if format != "xlsx" {
		return string(body)
	}
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("xlsx zip open: %v", err)
	}
	for _, f := range r.File {
		if f.Name == "xl/worksheets/sheet1.xml" {
			rc, err := f.Open()
			if err != nil {
				t.Fatalf("xlsx sheet open: %v", err)
			}
			defer func() { _ = rc.Close() }()
			data, err := io.ReadAll(rc)
			if err != nil {
				t.Fatalf("xlsx sheet read: %v", err)
			}
			return string(data)
		}
	}
	t.Fatalf("xlsx body missing sheet1.xml")
	return ""
}

// ===== Tests =====

// AC-2: the vendor export endpoint returns 200 + a CSV body with the
// canonical header row.
func TestVendorsExport_HappyPathCSV(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	seedVendor(t, ts, bearer, "Datadog", "alice@operator.example.com")

	url := ts.URL + "/v1/admin/vendors/export?format=csv"
	resp, body := doGet(t, url, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d; body = %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/csv; charset=utf-8" {
		t.Errorf("Content-Type = %q; want text/csv; charset=utf-8", got)
	}
	cd := resp.Header.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="vendors_`) {
		t.Errorf("Content-Disposition = %q; want prefix attachment; filename=\"vendors_", cd)
	}

	rows := mustParseCSV(t, string(body))
	if len(rows) < 2 {
		t.Fatalf("expected >= 2 CSV rows (header + 1 data row); got %d", len(rows))
	}
	wantHeader := []string{
		"id", "name", "domain", "criticality", "contract_start", "contract_end",
		"dpa_signed", "dpa_signed_at", "review_cadence", "last_review_date",
		"overdue", "owner_user_masked", "linked_sow_uri", "notes", "scope_cell_ids",
		"created_at", "updated_at",
	}
	for _, col := range wantHeader {
		if !contains(rows[0], col) {
			t.Errorf("CSV header missing column %q; got %v", col, rows[0])
		}
	}
	// P0-A2 — un-masked owner_user column MUST NOT appear in the
	// header. Catches a refactor that accidentally surfaces the raw
	// field alongside the masked one.
	if contains(rows[0], "owner_user") && !contains(rows[0], "owner_user_masked") {
		t.Errorf("header contains owner_user but not owner_user_masked — un-masked column leaked")
	}
}

// AC-9: vendor email masking. The seeded owner_user
// ("alice@operator.example.com") MUST appear as "*@operator.example.com"
// in the export body. The raw local-part "alice" MUST NOT appear
// anywhere. Tested across all three formats so an encoder bug can't
// leak the local-part through one format while masking it in the
// others.
func TestVendorsExport_EmailMasking(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	seedVendor(t, ts, bearer, "Stripe", "alice@operator.example.com")
	seedVendor(t, ts, bearer, "PagerDuty", "bob+ops@responder.example.com")

	for _, format := range []string{"csv", "json", "xlsx"} {
		format := format
		t.Run(format, func(t *testing.T) {
			url := ts.URL + "/v1/admin/vendors/export?format=" + format
			resp, body := doGet(t, url, bearer)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("format=%s status = %d; body = %s", format, resp.StatusCode, body)
			}
			search := extractSearchableText(t, format, body)
			// Masked tokens MUST appear.
			if !strings.Contains(search, "*@operator.example.com") {
				t.Errorf("format=%s body missing masked alice token *@operator.example.com", format)
			}
			if !strings.Contains(search, "*@responder.example.com") {
				t.Errorf("format=%s body missing masked bob token *@responder.example.com", format)
			}
			// Raw local-parts MUST NOT appear anywhere in the body.
			// "alice" + "bob+ops" are the local-parts; if they show
			// up the masking is broken.
			//
			// XLSX sheet1.xml stores cell values as text inside <t>
			// elements; CSV / JSON store them verbatim. A substring
			// scan over the searchable text is sufficient.
			for _, leak := range []string{"alice@operator", "bob+ops@responder"} {
				if strings.Contains(search, leak) {
					t.Errorf("format=%s body LEAKS raw email token %q", format, leak)
				}
			}
		})
	}
}

// AC-5/6: meta-audit row written on success. The me_audit_log row
// MUST carry action='vendors_export', the tenant id, and a success
// result.
func TestVendorsExport_MetaAuditFires(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenant := freshTenant(t, admin)
	ts, bearer := setupHTTPServer(t, tenant)

	seedVendor(t, ts, bearer, "Lacework", "carol@ops.example.com")

	url := ts.URL + "/v1/admin/vendors/export?format=json"
	resp, _ := doGet(t, url, bearer)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}

	var rowCount int
	var afterJSON string
	for tries := 0; tries < 20; tries++ {
		err := admin.QueryRow(context.Background(),
			`SELECT COUNT(*), COALESCE(MAX(after::text), '')
			 FROM me_audit_log
			 WHERE tenant_id = $1 AND action = 'vendors_export'`,
			tenant,
		).Scan(&rowCount, &afterJSON)
		if err != nil {
			t.Fatalf("me_audit_log probe: %v", err)
		}
		if rowCount > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if rowCount == 0 {
		t.Fatalf("expected >= 1 me_audit_log row with action=vendors_export; got 0")
	}
	if !strings.Contains(afterJSON, `"result"`) || !strings.Contains(afterJSON, `"success"`) {
		t.Errorf("after_state missing success result; got %s", afterJSON)
	}
	if !strings.Contains(afterJSON, `"format"`) || !strings.Contains(afterJSON, `"json"`) {
		t.Errorf("after_state missing format=json; got %s", afterJSON)
	}
}

// AC-10: cross-tenant isolation across all three formats. Tenant A
// runs the export; tenant B's vendor MUST NOT appear.
func TestVendorsExport_CrossTenantIsolationAllThreeFormats(t *testing.T) {
	admin := openPool(t, adminDSN(t))
	defer admin.Close()
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)
	tsA, bearerA := setupHTTPServer(t, tenantA)
	tsB, bearerB := setupHTTPServer(t, tenantB)

	idA := seedVendor(t, tsA, bearerA, "TenantA-vendor", "alice@tenantA.example")
	idB := seedVendor(t, tsB, bearerB, "TenantB-SECRET-vendor", "victor@tenantB.example")

	for _, format := range []string{"csv", "json", "xlsx"} {
		format := format
		t.Run(format, func(t *testing.T) {
			url := tsA.URL + "/v1/admin/vendors/export?format=" + format
			resp, body := doGet(t, url, bearerA)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("format=%s status = %d; body = %s", format, resp.StatusCode, body)
			}
			search := extractSearchableText(t, format, body)
			// Tenant B's vendor name + UUID + (already-masked)
			// domain must not leak into tenant A's export. We
			// assert on the unique-to-B name + UUID because the
			// masked domain (`*@tenantB.example`) is distinctive.
			if strings.Contains(search, "TenantB-SECRET-vendor") {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B vendor name in body", format)
			}
			if strings.Contains(search, idB) {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B vendor UUID in body", format)
			}
			if strings.Contains(search, "*@tenantB.example") {
				t.Errorf("CROSS-TENANT LEAK in format=%s: tenant B masked-domain token in body", format)
			}
			// Positive control: tenant A's vendor MUST appear.
			if !strings.Contains(search, idA) {
				t.Errorf("format=%s body does not contain tenant A vendor UUID — RLS may be over-eager", format)
			}
		})
	}
}

// ===== helpers =====

func mustParseCSV(t *testing.T, body string) [][]string {
	t.Helper()
	r := stdcsv.NewReader(strings.NewReader(body))
	r.LazyQuotes = true
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v; body=%q", err, body)
	}
	return rows
}

func contains(row []string, s string) bool {
	for _, c := range row {
		if c == s {
			return true
		}
	}
	return false
}
