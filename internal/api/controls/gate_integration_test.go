//go:build integration

// Slice 574 — HTTP-level integration tests for the control-bundle upload
// test-gate. Real Postgres (the SQL-fixture path needs a tenant tx) + the
// shared controls test harness (setupHTTP, seedSCFAnchor). These tests POST a
// tarball (the only ingest path that carries a tests/ tree) and assert:
//
//   - a bundle whose tests PASS is accepted (AC-6),
//   - a bundle whose tests FAIL is rejected 400 with the per-case report (AC-2),
//   - a bundle with a SQL-language fixture runs on the upload path and a SQL
//     fixture failure maps to the same 400 (AC-4),
//   - a bundle with NO tests is accepted with a gate warning (no-tests policy).
package controls_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/control"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// gateTarball builds a gzip-tar bundle with control.yaml + tests/<name> files.
func gateTarball(t *testing.T, manifest string, testFiles map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name, body string) {
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("tar write %s: %v", name, err)
		}
	}
	write("control.yaml", manifest)
	for name, body := range testFiles {
		write("tests/"+name, body)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gz close: %v", err)
	}
	return buf.Bytes()
}

// postGateTarball POSTs a tarball bundle and returns the status + raw body.
func postGateTarball(t *testing.T, s setupResult, tarBytes []byte) (int, []byte) {
	t.Helper()
	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	part, err := mp.CreateFormFile("bundle.tar.gz", "bundle.tar.gz")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(tarBytes); err != nil {
		t.Fatalf("write form file: %v", err)
	}
	if err := mp.Close(); err != nil {
		t.Fatalf("mp close: %v", err)
	}
	req, _ := http.NewRequestWithContext(context.Background(),
		http.MethodPost, s.server.URL+"/v1/controls:upload-bundle", &body)
	req.Header.Set("Authorization", "Bearer "+s.adminBear)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post tarball: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

const gateJSONPathManifest = `bundle_schema_version: "1"
bundle_id: gate_it_jsonpath
title: "Encrypted buckets (gate IT)"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: payload_encrypted
    language: jsonpath
    expression: "$.encrypted"
`

// setTenantGateMode upserts a tenants row for the given tenant id with the
// requested bundle_gate_mode, via an admin (BYPASSRLS-capable) pool. The
// upload handler does not create the tenants row, so a test that wants a
// non-default policy must seed it directly. Cleans up the row afterwards.
func setTenantGateMode(t *testing.T, tenant, mode string) {
	t.Helper()
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, adminDSN(t))
	if err != nil {
		t.Fatalf("admin pool: %v", err)
	}
	defer admin.Close()
	if _, err := admin.Exec(ctx,
		`INSERT INTO tenants (id, name, bundle_gate_mode)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET bundle_gate_mode = EXCLUDED.bundle_gate_mode`,
		tenant, "gate-it-"+tenant[:8], mode); err != nil {
		t.Fatalf("seed tenant gate mode: %v", err)
	}
	t.Cleanup(func() {
		c := context.Background()
		a, e := pgxpool.New(c, adminDSN(t))
		if e != nil {
			t.Logf("cleanup admin pool: %v", e)
			return
		}
		defer a.Close()
		if _, e := a.Exec(c, `DELETE FROM tenants WHERE id = $1`, tenant); e != nil {
			t.Logf("cleanup tenants row: %v", e)
		}
	})
}

// TestGate_DefaultTenantStrictRejectsRed proves the slice-574 default is
// preserved per-tenant: a tenant with NO tenants row (the freshTenant case)
// resolves to strict, so a red bundle is rejected 400. (This is the absence =
// default hard-block path of slice 608 AC-2.)
func TestGate_DefaultTenantStrictRejectsRed(t *testing.T) {
	s := setupHTTP(t)
	// No setTenantGateMode call — s.tenant has no tenants row → strict default.
	tests := `cases:
  - name: encrypted-wrong
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: dev, encrypted: false }
`
	tarBytes := gateTarball(t, gateJSONPathManifest, map[string]string{"t.yaml": tests})
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusBadRequest {
		t.Fatalf("default (no policy row) tenant must hard-block a red bundle; got %d body=%s", code, raw)
	}
}

// TestGate_AdvisoryTenantAcceptsRed proves slice 608 AC-3/AC-6: a tenant set to
// advisory accepts the SAME red bundle with a warning + gate_test_report,
// instead of the 400 the default tenant gets.
func TestGate_AdvisoryTenantAcceptsRed(t *testing.T) {
	s := setupHTTP(t)
	setTenantGateMode(t, s.tenant, "advisory")
	tests := `cases:
  - name: encrypted-wrong
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: dev, encrypted: false }
`
	tarBytes := gateTarball(t, gateJSONPathManifest, map[string]string{"t.yaml": tests})
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("advisory tenant must ACCEPT a red bundle; got %d body=%s", code, raw)
	}
	var up struct {
		ControlID      string `json:"control_id"`
		GateWarning    string `json:"gate_warning"`
		GateTestReport *struct {
			Failed int `json:"failed"`
		} `json:"gate_test_report"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if up.ControlID == "" {
		t.Fatalf("advisory upload must still persist the control; body=%s", raw)
	}
	if up.GateWarning == "" {
		t.Fatalf("advisory upload must carry a warning; body=%s", raw)
	}
	if up.GateTestReport == nil || up.GateTestReport.Failed != 1 {
		t.Fatalf("advisory upload must carry the red report; body=%s", raw)
	}
}

// TestGate_MandatoryTestsTenantRejectsNoTests proves slice 608 AC-4: a tenant
// set to mandatory_tests rejects a bundle that ships NO tests/, where the
// default tenant would accept it with a warning.
func TestGate_MandatoryTestsTenantRejectsNoTests(t *testing.T) {
	s := setupHTTP(t)
	setTenantGateMode(t, s.tenant, "mandatory_tests")
	tarBytes := gateTarball(t, gateJSONPathManifest, nil) // no tests
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusBadRequest {
		t.Fatalf("mandatory_tests tenant must reject a no-tests bundle; got %d body=%s", code, raw)
	}
}

// TestGate_TenantIsolation proves one tenant's policy does not affect another.
// Tenant A is set to advisory; tenant B has no policy row. Under each tenant's
// own RLS context the resolver returns the tenant's own policy: A → advisory,
// B → the strict default. RLS scopes the row read, so A's setting is invisible
// to B. Asserted at the control.Store.BundleGateMode resolver (the same code
// path the HTTP handler uses to resolve the per-tenant mode), mirroring the
// Store-level isolation proof in TestList_TenantIsolation.
func TestGate_TenantIsolation(t *testing.T) {
	t.Parallel()
	admin := adminPool(t)
	app := appPool(t)

	tenantA := freshTenantList(t, admin)
	tenantB := freshTenantList(t, admin)
	setTenantGateMode(t, tenantA, "advisory")
	// tenantB: deliberately no tenants row → strict default.

	store := control.NewStore(app)

	ctxA, err := tenancy.WithTenant(context.Background(), tenantA)
	if err != nil {
		t.Fatalf("WithTenant A: %v", err)
	}
	modeA, err := store.BundleGateMode(ctxA)
	if err != nil {
		t.Fatalf("BundleGateMode A: %v", err)
	}
	if modeA != "advisory" {
		t.Fatalf("tenant A resolved mode = %q; want advisory", modeA)
	}

	ctxB, err := tenancy.WithTenant(context.Background(), tenantB)
	if err != nil {
		t.Fatalf("WithTenant B: %v", err)
	}
	modeB, err := store.BundleGateMode(ctxB)
	if err != nil {
		t.Fatalf("BundleGateMode B: %v", err)
	}
	if modeB != control.DefaultBundleGateMode {
		t.Fatalf("tenant B resolved mode = %q; want the strict default (isolation: A's advisory must not leak to B)", modeB)
	}
}

func TestGate_PassingTestsAccepted(t *testing.T) {
	s := setupHTTP(t)
	tests := `cases:
  - name: encrypted-ok
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: prod, encrypted: true }
`
	tarBytes := gateTarball(t, gateJSONPathManifest, map[string]string{"t.yaml": tests})
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("expected the passing bundle to upload; got %d body=%s", code, raw)
	}
	var up struct {
		ControlID   string `json:"control_id"`
		GateWarning string `json:"gate_warning"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if up.ControlID == "" {
		t.Fatalf("expected a control_id; body=%s", raw)
	}
	if up.GateWarning != "" {
		t.Fatalf("a bundle WITH passing tests should carry no warning; got %q", up.GateWarning)
	}
}

func TestGate_FailingTestsRejected(t *testing.T) {
	s := setupHTTP(t)
	tests := `cases:
  - name: encrypted-wrong
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: dev, encrypted: false }
`
	tarBytes := gateTarball(t, gateJSONPathManifest, map[string]string{"t.yaml": tests})
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusBadRequest {
		t.Fatalf("expected 400 (gate block); got %d body=%s", code, raw)
	}
	var rej struct {
		Error  string `json:"error"`
		Report *struct {
			Failed int `json:"failed"`
			Cases  []struct {
				Name   string `json:"name"`
				Passed bool   `json:"passed"`
			} `json:"cases"`
		} `json:"test_report"`
	}
	if err := json.Unmarshal(raw, &rej); err != nil {
		t.Fatalf("decode rejection: %v body=%s", err, raw)
	}
	if rej.Report == nil || rej.Report.Failed != 1 {
		t.Fatalf("expected a per-case report with 1 failure; body=%s", raw)
	}
}

func TestGate_NoTestsAcceptedWithWarning(t *testing.T) {
	s := setupHTTP(t)
	tarBytes := gateTarball(t, gateJSONPathManifest, nil)
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("expected a no-tests bundle to upload; got %d body=%s", code, raw)
	}
	var up struct {
		GateWarning string `json:"gate_warning"`
	}
	if err := json.Unmarshal(raw, &up); err != nil {
		t.Fatalf("decode: %v body=%s", err, raw)
	}
	if up.GateWarning == "" {
		t.Fatalf("a no-tests bundle should carry a gate warning; body=%s", raw)
	}
}

// TestGate_SQLFixtureRunsOnUploadPath proves AC-4: a sql-language fixture runs
// on the upload path (the handler has a tenant tx). A passing SQL fixture
// uploads; a failing SQL fixture is rejected with the same 400.
func TestGate_SQLFixtureRunsOnUploadPath(t *testing.T) {
	s := setupHTTP(t)
	// A SQL query that returns 'pass' for any record set: the fixture asserts
	// pass and the query returns pass → the case PASSES → upload accepted.
	const sqlManifest = `bundle_schema_version: "1"
bundle_id: gate_it_sql
title: "SQL fixture (gate IT)"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: sql_pass
    language: sql
    expression: "SELECT 'pass'::text AS result"
`
	tests := `cases:
  - name: sql-pass
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { any: value }
`
	tarBytes := gateTarball(t, sqlManifest, map[string]string{"t.yaml": tests})
	code, raw := postGateTarball(t, s, tarBytes)
	if code != http.StatusCreated && code != http.StatusOK {
		t.Fatalf("expected the SQL-fixture bundle to upload (sql ran on the upload tx); got %d body=%s", code, raw)
	}

	// Now a SQL fixture whose expectation does not match: the query returns
	// 'pass' but the case asserts 'fail' → case FAILS → 400.
	const sqlManifestFail = `bundle_schema_version: "1"
bundle_id: gate_it_sql_fail
title: "SQL fixture fail (gate IT)"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: sql_pass
    language: sql
    expression: "SELECT 'pass'::text AS result"
`
	testsFail := `cases:
  - name: sql-mismatch
    expected_state: fail
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { any: value }
`
	tarBytes2 := gateTarball(t, sqlManifestFail, map[string]string{"t.yaml": testsFail})
	code2, raw2 := postGateTarball(t, s, tarBytes2)
	if code2 != http.StatusBadRequest {
		t.Fatalf("expected a SQL fixture mismatch to be rejected 400; got %d body=%s", code2, raw2)
	}
}
