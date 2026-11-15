package controldetail

// #2137 — unit tests for GET /v1/evidence/{id}. A fixed-row stub stands in
// for the store; the RLS and tenant-isolation half lives in
// evidence_detail_integration_test.go.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/demoprov"
	"github.com/mgoodric/security-atlas/internal/evidence/redact"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type stubDetailReader struct {
	rec dbx.EvidenceRecord
	err error
}

func (s stubDetailReader) EvidenceByID(_ context.Context, _ uuid.UUID) (dbx.EvidenceRecord, error) {
	return s.rec, s.err
}

func (s stubDetailReader) DemoEvidence(_ context.Context, _ []uuid.UUID) (demoprov.Set, error) {
	return demoprov.Set{}, nil
}

type stubRedaction struct{ rules []string }

func (s stubRedaction) RedactionRulesFor(_ context.Context, _, _, _ string) ([]string, error) {
	return s.rules, nil
}

func attestationRecord() dbx.EvidenceRecord {
	return dbx.EvidenceRecord{
		ID:            pgID(contractEvidenceID),
		TenantID:      pgID(contractTenantID),
		ControlID:     pgID(contractControlID),
		ControlRef:    contractControlID,
		ObservedAt:    pgTS(mustTime("2026-05-14T08:00:00Z")),
		IngestedAt:    pgTS(mustTime("2026-05-14T08:00:01Z")),
		EvidenceKind:  strptr("manual.attestation.v1"),
		SchemaVersion: strptr("1.1.0"),
		Payload:       []byte(`{"attestor":"alice@example.test","statement":"Quarterly access review done","supporting_uri":"https://example.test/r/1"}`),
		Provenance:    []byte(`{"connector":"manual"}`),
		Hash:          "sha256:abc",
		Result:        dbx.EvidenceResult("pass"),
		IngestionPath: "manual",
	}
}

func serveDetail(t *testing.T, h *Handler, id string, cred *credstore.Credential) *httptest.ResponseRecorder {
	t.Helper()
	r := chi.NewRouter()
	r.Get("/v1/evidence/{id}", h.EvidenceDetail)
	req := httptest.NewRequest(http.MethodGet, "/v1/evidence/"+id, nil)
	ctx := req.Context()
	if cred != nil {
		ctx = authctx.WithCredential(ctx, *cred)
	}
	ctx, err := tenancy.WithTenant(ctx, contractTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func reader() *credstore.Credential {
	return &credstore.Credential{ID: "key_2137", TenantID: contractTenantID, IsApprover: true}
}

func decodeDetail(t *testing.T, rec *httptest.ResponseRecorder) evidenceDetailWire {
	t.Helper()
	var body struct {
		Evidence evidenceDetailWire `json:"evidence"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	return body.Evidence
}

func TestEvidenceDetail_ReturnsPayload(t *testing.T) {
	t.Parallel()
	h := &Handler{detail: stubDetailReader{rec: attestationRecord()}}
	rec := serveDetail(t, h, contractEvidenceID, reader())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
	}
	got := decodeDetail(t, rec)
	var payload map[string]any
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["statement"] != "Quarterly access review done" || payload["attestor"] != "alice@example.test" {
		t.Fatalf("payload = %v", payload)
	}
	if got.Result != "pass" || got.EvidenceID != contractEvidenceID || got.IngestionPath != "manual" {
		t.Fatalf("record = %+v", got)
	}
	if got.ControlID == nil || *got.ControlID != contractControlID {
		t.Fatalf("control_id = %v", got.ControlID)
	}
}

func TestEvidenceDetail_RedactsFieldOnRead(t *testing.T) {
	t.Parallel()
	h := (&Handler{detail: stubDetailReader{rec: attestationRecord()}}).
		WithRedaction(stubRedaction{rules: []string{"$.attestor"}})
	rec := serveDetail(t, h, contractEvidenceID, reader())
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "alice@example.test") {
		t.Fatalf("redacted value leaked: %s", rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(decodeDetail(t, rec).Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload["attestor"] != redact.Marker {
		t.Fatalf("attestor = %v, want %q", payload["attestor"], redact.Marker)
	}
	if payload["statement"] != "Quarterly access review done" {
		t.Fatalf("unredacted field changed: %v", payload["statement"])
	}
}

func TestEvidenceDetail_NotFoundCases(t *testing.T) {
	t.Parallel()
	found := &Handler{detail: stubDetailReader{rec: attestationRecord()}}
	missing := &Handler{detail: stubDetailReader{err: errEvidenceNotFound}}
	viewer := &credstore.Credential{ID: "key_viewer", TenantID: contractTenantID}
	cases := []struct {
		name string
		h    *Handler
		id   string
		cred *credstore.Credential
	}{
		{"no credential", found, contractEvidenceID, nil},
		{"role without evidence read", found, contractEvidenceID, viewer},
		{"unknown id", missing, uuid.NewString(), reader()},
		{"malformed id", found, "not-a-uuid", reader()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := serveDetail(t, tc.h, tc.id, tc.cred)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404; body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "Quarterly") {
				t.Fatalf("payload leaked on denial: %s", rec.Body.String())
			}
		})
	}
}
