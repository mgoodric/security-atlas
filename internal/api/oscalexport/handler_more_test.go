// Load-bearing functions covered here:
//
//   - Handler.Export — the JSON request/response shell over
//     oscal.Exporter.Export, including:
//     • happy-path body marshalling (Bundle -> exportResponse JSON)
//     • RequestedBy fallback when no credential is in the request context
//     • RequestedBy preferring the credential id when authctx carries one
//     • empty-body acceptance (Content-Length == 0 short-circuit)
//   - writeExportError — error-sentinel-to-HTTP-status mapping:
//     ErrPeriodNotFrozen          -> 409 Conflict
//     ErrPeriodNotFound           -> 404 Not Found
//     ErrBridgeUnavailable        -> 502 Bad Gateway
//     ErrRoundTripFailed          -> 422 Unprocessable Entity
//     ErrSigningFailed            -> 500 Internal Server Error
//     unrecognized sentinel       -> 500 Internal Server Error (default)
//
// The handler's DB-backed happy path is exercised by the integration test
// at internal/oscal/integration_test.go (real Postgres + real Python
// bridge). The tests in this file inject a fakeExporter so each branch
// in writeExportError and the body-marshalling code in Export is hit
// without standing up the platform.

package oscalexport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/oscal"
)

// fakeExporter is an in-memory stand-in for *oscal.Exporter used by the
// unit tests in this file. It records the last call and returns whatever
// the test wired into bundle / err.
type fakeExporter struct {
	bundle *oscal.Bundle
	err    error

	lastInput oscal.ExportInput
	calls     int
}

func (f *fakeExporter) Export(_ context.Context, in oscal.ExportInput) (*oscal.Bundle, error) {
	f.calls++
	f.lastInput = in
	return f.bundle, f.err
}

// newRouterWith wires a chi router with a fakeExporter behind the same
// URL pattern the production route uses. Returning the fake lets the
// test inspect ExportInput after the call.
func newRouterWith(fake *fakeExporter) (*chi.Mux, *Handler) {
	h := &Handler{exporter: fake}
	r := chi.NewRouter()
	r.Post("/v1/audit-periods/{id}/oscal-export", h.Export)
	return r, h
}

// fixedPeriodID is a stable UUID used by every test in this file.
const fixedPeriodID = "11111111-1111-1111-1111-111111111111"

// newRequest constructs a POST to the export route with the given body.
// Pass an empty string for "no body".
func newRequest(t *testing.T, body string) *http.Request {
	t.Helper()
	url := "/v1/audit-periods/" + fixedPeriodID + "/oscal-export"
	if body == "" {
		// nil body + ContentLength 0 exercises the empty-body short-circuit.
		req := httptest.NewRequest(http.MethodPost, url, nil)
		return req
	}
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// --------------------- Error-mapping branches ---------------------

func TestExport_ErrorMapping(t *testing.T) {
	// One subtest per error sentinel + one "unrecognized" default branch.
	cases := []struct {
		name       string
		exporter   *fakeExporter
		wantStatus int
		wantSub    string // a substring that must appear in the JSON error
	}{
		{
			name:       "PeriodNotFrozen->409",
			exporter:   &fakeExporter{err: oscal.ErrPeriodNotFrozen},
			wantStatus: http.StatusConflict,
			wantSub:    "not frozen",
		},
		{
			name:       "PeriodNotFrozen_wrapped->409",
			exporter:   &fakeExporter{err: fmt.Errorf("aggregate: %w", oscal.ErrPeriodNotFrozen)},
			wantStatus: http.StatusConflict,
			wantSub:    "not frozen",
		},
		{
			name:       "PeriodNotFound->404",
			exporter:   &fakeExporter{err: oscal.ErrPeriodNotFound},
			wantStatus: http.StatusNotFound,
			wantSub:    "not found",
		},
		{
			name:       "BridgeUnavailable->502",
			exporter:   &fakeExporter{err: fmt.Errorf("dial: %w", oscal.ErrBridgeUnavailable)},
			wantStatus: http.StatusBadGateway,
			wantSub:    "oscal-bridge",
		},
		{
			name:       "RoundTripFailed->422",
			exporter:   &fakeExporter{err: fmt.Errorf("ssp.json: %w", oscal.ErrRoundTripFailed)},
			wantStatus: http.StatusUnprocessableEntity,
			wantSub:    "round-trip",
		},
		{
			name:       "SigningFailed->500",
			exporter:   &fakeExporter{err: fmt.Errorf("signer: %w", oscal.ErrSigningFailed)},
			wantStatus: http.StatusInternalServerError,
			wantSub:    "signing failed",
		},
		{
			name:       "Unrecognized->500_default",
			exporter:   &fakeExporter{err: errors.New("some unknown failure mode")},
			wantStatus: http.StatusInternalServerError,
			wantSub:    "oscal export failed",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			r, _ := newRouterWith(tc.exporter)
			req := newRequest(t, "")
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantSub) {
				t.Errorf("body %q missing substring %q", rec.Body.String(), tc.wantSub)
			}
			if tc.exporter.calls != 1 {
				t.Errorf("exporter calls = %d, want 1", tc.exporter.calls)
			}

			// Response body must be a JSON object with an "error" string.
			var got map[string]string
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("response is not JSON: %v (body=%q)", err, rec.Body.String())
			}
			if _, ok := got["error"]; !ok {
				t.Errorf("response JSON missing top-level \"error\" key: %v", got)
			}

			// Content-Type must be application/json for every error too.
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}

// --------------------- Happy path ---------------------

// newFakeBundle builds a Bundle with the four canonical members so the
// happy-path test can assert the JSON-marshalling code in Export hits
// every field on the response (including the per-member loop).
func newFakeBundle(t *testing.T) *oscal.Bundle {
	t.Helper()
	pid, err := uuid.Parse(fixedPeriodID)
	if err != nil {
		t.Fatalf("uuid parse: %v", err)
	}
	frozenAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339)
	generatedAt := time.Date(2026, 5, 26, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	return &oscal.Bundle{
		AuditPeriodID: pid,
		FrozenAt:      frozenAt,
		OSCALVersion:  oscal.OSCALVersion,
		GeneratedAt:   generatedAt,
		RequestedBy:   "test-credential",
		Members: []oscal.BundleMember{
			{Filename: "ssp.json", ModelType: "system-security-plan", JSON: []byte(`{"k":"ssp"}`), SHA256: "deadbeef01"},
			{Filename: "assessment-plan.json", ModelType: "assessment-plan", JSON: []byte(`{"k":"ap"}`), SHA256: "deadbeef02"},
			{Filename: "assessment-results.json", ModelType: "assessment-results", JSON: []byte(`{"k":"ar"}`), SHA256: "deadbeef03"},
			{Filename: "poam.json", ModelType: "plan-of-action-and-milestones", JSON: []byte(`{"k":"poam"}`), SHA256: "deadbeef04"},
		},
		Signature: oscal.Signature{
			Algorithm: "ed25519",
			PublicKey: "abc123",
			Digest:    "f00ba5",
			Signature: "deadbeef",
		},
	}
}

func TestExport_HappyPath_ReturnsBundleJSON_WithBody(t *testing.T) {
	bundle := newFakeBundle(t)
	fake := &fakeExporter{bundle: bundle}
	r, _ := newRouterWith(fake)

	body := `{
		"organization_name": "Acme",
		"system_name": "Atlas",
		"system_description": "test-system"
	}`
	req := newRequest(t, body)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	// Decode into the expected response shape.
	var got struct {
		AuditPeriodID string `json:"audit_period_id"`
		FrozenAt      string `json:"frozen_at"`
		OSCALVersion  string `json:"oscal_version"`
		GeneratedAt   string `json:"generated_at"`
		RequestedBy   string `json:"requested_by"`
		Signature     struct {
			Algorithm string `json:"algorithm"`
			Digest    string `json:"digest"`
		} `json:"signature"`
		Members []struct {
			Filename  string          `json:"filename"`
			ModelType string          `json:"model_type"`
			SHA256    string          `json:"sha256"`
			OSCAL     json.RawMessage `json:"oscal"`
		} `json:"members"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}

	if got.AuditPeriodID != fixedPeriodID {
		t.Errorf("audit_period_id = %q, want %q", got.AuditPeriodID, fixedPeriodID)
	}
	if got.OSCALVersion != oscal.OSCALVersion {
		t.Errorf("oscal_version = %q, want %q", got.OSCALVersion, oscal.OSCALVersion)
	}
	if got.Signature.Algorithm != "ed25519" || got.Signature.Digest == "" {
		t.Errorf("signature did not round-trip: %+v", got.Signature)
	}
	if len(got.Members) != 4 {
		t.Fatalf("members len = %d, want 4", len(got.Members))
	}

	// Spot-check the SSP member retained its OSCAL JSON as a raw payload.
	if !strings.Contains(string(got.Members[0].OSCAL), `"k":"ssp"`) {
		t.Errorf("first member OSCAL payload missing SSP marker: %s", string(got.Members[0].OSCAL))
	}
	if got.Members[0].ModelType != "system-security-plan" {
		t.Errorf("first member model_type = %q, want system-security-plan", got.Members[0].ModelType)
	}

	// The handler must forward body fields into ExportInput.
	if fake.lastInput.OrganizationName != "Acme" {
		t.Errorf("OrganizationName not forwarded: %+v", fake.lastInput)
	}
	if fake.lastInput.SystemName != "Atlas" {
		t.Errorf("SystemName not forwarded: %+v", fake.lastInput)
	}
	if fake.lastInput.SystemDescription != "test-system" {
		t.Errorf("SystemDescription not forwarded: %+v", fake.lastInput)
	}
	// The URL UUID must reach the exporter.
	if fake.lastInput.AuditPeriodID.String() != fixedPeriodID {
		t.Errorf("AuditPeriodID = %s, want %s", fake.lastInput.AuditPeriodID, fixedPeriodID)
	}
	// No credential in the request context -> RequestedBy falls back to "api".
	if fake.lastInput.RequestedBy != "api" {
		t.Errorf("RequestedBy = %q, want fallback %q", fake.lastInput.RequestedBy, "api")
	}
}

func TestExport_HappyPath_EmptyBody(t *testing.T) {
	// No body at all. The handler must not 400, must call the exporter,
	// and must succeed with the fake bundle.
	bundle := newFakeBundle(t)
	fake := &fakeExporter{bundle: bundle}
	r, _ := newRouterWith(fake)

	req := newRequest(t, "") // ContentLength=0 path
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with empty body; body=%s", rec.Code, rec.Body.String())
	}
	if fake.calls != 1 {
		t.Fatalf("exporter calls = %d, want 1", fake.calls)
	}
	if fake.lastInput.OrganizationName != "" || fake.lastInput.SystemName != "" {
		t.Errorf("empty body should leave org/system fields zero, got %+v", fake.lastInput)
	}
}

func TestExport_HappyPath_RequestedByFromCredential(t *testing.T) {
	// authctx-attached credential should override the "api" fallback.
	bundle := newFakeBundle(t)
	fake := &fakeExporter{bundle: bundle}
	r, _ := newRouterWith(fake)

	req := newRequest(t, "{}")
	cred := credstore.Credential{ID: "cred_42", TenantID: "tnt_test"}
	ctx := authctx.WithCredential(req.Context(), cred)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastInput.RequestedBy != "cred_42" {
		t.Errorf("RequestedBy = %q, want %q (credential id should override fallback)",
			fake.lastInput.RequestedBy, "cred_42")
	}
}

func TestExport_HappyPath_CredentialWithEmptyIDFallsBackToAPI(t *testing.T) {
	// authctx attaches a credential whose ID is empty — the handler
	// treats this as no-credential and uses the "api" fallback.
	bundle := newFakeBundle(t)
	fake := &fakeExporter{bundle: bundle}
	r, _ := newRouterWith(fake)

	req := newRequest(t, "{}")
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{ID: "", TenantID: "tnt_x"})
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if fake.lastInput.RequestedBy != "api" {
		t.Errorf("RequestedBy = %q, want fallback %q when credential id is empty",
			fake.lastInput.RequestedBy, "api")
	}
}
