package controls

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/control"
)

// jsonpathManifest is a control whose single jsonpath query resolves
// $.encrypted on each record's payload. JSON-path evaluates fully in-process,
// so the gate needs no database for these bundles.
const jsonpathManifest = `bundle_schema_version: "1"
bundle_id: gate_jsonpath_control
title: "Encrypted buckets"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: payload_encrypted
    language: jsonpath
    expression: "$.encrypted"
`

// passingTests asserts a record with encrypted=true yields pass — which the
// query produces, so the case PASSES the gate.
const passingTests = `cases:
  - name: encrypted-ok
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: prod, encrypted: true }
`

// failingTests asserts pass but the record is encrypted=false, so the query
// returns fail; actual != expected → the case FAILS the gate.
const failingTests = `cases:
  - name: encrypted-wrong
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
        payload: { bucket: dev, encrypted: false }
`

func buildBundleTarball(t *testing.T, manifest string, testFiles map[string]string) []byte {
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

func multipartUpload(t *testing.T, tarBytes []byte) (*http.Request, *httptest.ResponseRecorder) {
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
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", &body)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	req = req.WithContext(withAuthAndTenant(req.Context(), adminCred()))
	return req, httptest.NewRecorder()
}

// TestUpload_GateBlocksFailingBundle is the handler-level proof of AC-2: a
// bundle whose declared test FAILS is rejected with a 400 + the per-case
// report. The rejection returns BEFORE the persistence path, so a nil store is
// safe here.
func TestUpload_GateBlocksFailingBundle(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	tarBytes := buildBundleTarball(t, jsonpathManifest, map[string]string{"t.yaml": failingTests})
	req, rr := multipartUpload(t, tarBytes)
	h.UploadBundle(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 (gate block); got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp gateRejectionResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode rejection: %v (body=%s)", err, rr.Body.String())
	}
	if resp.Report == nil || resp.Report.Failed != 1 {
		t.Fatalf("expected a per-case report with 1 failure; got %+v", resp.Report)
	}
	if resp.Report.Cases[0].Name != "encrypted-wrong" {
		t.Fatalf("expected the failing case name in the report; got %+v", resp.Report.Cases)
	}
}

// TestRunGate_PassingBundleAllowed proves the gate ALLOWS a bundle whose tests
// pass (AC-1) — exercised at the gate-function level so no store is needed.
func TestRunGate_PassingBundleAllowed(t *testing.T) {
	t.Parallel()
	tarBytes := buildBundleTarball(t, jsonpathManifest, map[string]string{"t.yaml": passingTests})
	bundle, err := control.ParseTarball(bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, err := runGate(context.Background(), nil, bundle)
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if v.blocked {
		t.Fatalf("a passing bundle must not be blocked; report=%+v", v.report)
	}
	if v.warning != "" {
		t.Fatalf("a bundle WITH tests should carry no no-tests warning; got %q", v.warning)
	}
}

// TestRunGate_FailingBundleBlocked is the gate-function mirror of AC-2.
func TestRunGate_FailingBundleBlocked(t *testing.T) {
	t.Parallel()
	tarBytes := buildBundleTarball(t, jsonpathManifest, map[string]string{"t.yaml": failingTests})
	bundle, err := control.ParseTarball(bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, err := runGate(context.Background(), nil, bundle)
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if !v.blocked {
		t.Fatalf("a failing bundle must be blocked")
	}
	if v.report == nil || v.report.Failed != 1 {
		t.Fatalf("expected a report with 1 failure; got %+v", v.report)
	}
}

// TestRunGate_NoTestsAllowedWithWarning proves the no-tests policy: a bundle
// with no tests/ uploads (not blocked) and carries a warning (AC-3 policy).
func TestRunGate_NoTestsAllowedWithWarning(t *testing.T) {
	t.Parallel()
	tarBytes := buildBundleTarball(t, jsonpathManifest, nil) // no tests
	bundle, err := control.ParseTarball(bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	v, err := runGate(context.Background(), nil, bundle)
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if v.blocked {
		t.Fatalf("a no-tests bundle must be allowed under the v0 policy")
	}
	if !strings.Contains(v.warning, "no test cases") {
		t.Fatalf("expected a no-tests warning; got %q", v.warning)
	}
	if v.hasTests() {
		t.Fatalf("hasTests must be false for a no-tests bundle")
	}
}

// TestRunGate_SQLWithNoRunnerBlocks proves a SQL fixture with no tx runner
// surfaces as a per-case ERROR and the gate blocks — never a silent pass.
func TestRunGate_SQLWithNoRunnerBlocks(t *testing.T) {
	t.Parallel()
	const sqlManifest = `bundle_schema_version: "1"
bundle_id: gate_sql_control
title: "SQL control"
scf_anchor_id: IAC-06
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: sql_query
    language: sql
    expression: "SELECT 'pass'::text AS result"
`
	const tests = `cases:
  - name: c
    expected_state: pass
    records:
      - result: pass
`
	tarBytes := buildBundleTarball(t, sqlManifest, map[string]string{"t.yaml": tests})
	bundle, err := control.ParseTarball(bytes.NewReader(tarBytes))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	// nil runner → SQL fixture cannot run → per-case ERROR → block.
	v, err := runGate(context.Background(), nil, bundle)
	if err != nil {
		t.Fatalf("runGate: %v", err)
	}
	if !v.blocked {
		t.Fatalf("a SQL fixture with no DB must block (errored case), not pass")
	}
	if v.report.Errored != 1 {
		t.Fatalf("expected 1 errored case; got %+v", v.report)
	}
}
