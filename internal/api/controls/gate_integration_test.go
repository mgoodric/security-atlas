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
