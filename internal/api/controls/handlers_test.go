package controls

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// withAuthAndTenant mirrors what slice-033's tenancy.Middleware does in
// production: it attaches the credential AND sets the tenant GUC on the
// context. Unit tests that drive a handler directly (no chi router, so
// no middleware) need this manual wiring.
func withAuthAndTenant(ctx context.Context, cred credstore.Credential) context.Context {
	ctx = authctx.WithCredential(ctx, cred)
	out, err := tenancy.WithTenant(ctx, cred.TenantID)
	if err != nil {
		panic("test fixture: WithTenant: " + err.Error())
	}
	return out
}

// These tests exercise the request-shape branches that do NOT require a
// Postgres connection: missing auth, missing admin flag, malformed JSON
// body, missing tarball, malformed YAML. The supersession + persistence
// path lives behind the integration build tag.

func TestUpload_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.UploadBundle(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", rr.Code)
	}
}

func TestUpload_RequiresAdmin(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	// A non-admin HUMAN credential (UserID is a plain UUID — not a
	// machine-actor prefix) must be rejected. Slice 196 widened the
	// handler to admit machine-actor credentials too (the bootstrap
	// container's OAuth client_credentials JWT lands as UserID =
	// "oauth_client:..."); this test pins the human-non-admin path
	// still 403s.
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "user-001",
		TenantID: "00000000-0000-0000-0000-000000000001",
		UserID:   "11111111-2222-3333-4444-555555555555",
		IsAdmin:  false,
	})
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403; got %d", rr.Code)
	}
}

// TestUpload_AdmitsMachineActor pins the slice 196 widening: a
// machine-actor credential (UserID prefixed with "oauth_client:")
// passes the handler-level auth gate even with IsAdmin=false. The
// downstream path (tenancy + parse) is what fails here (we send a
// bogus body) — the load-bearing assertion is that the handler did
// NOT short-circuit at 403.
func TestUpload_AdmitsMachineActor(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "jwt:abc-123",
		TenantID: "00000000-0000-0000-0000-000000000001",
		UserID:   "oauth_client:0e3b4a7e-1111-2222-3333-444455556666",
		IsAdmin:  false,
	})
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code == http.StatusForbidden {
		t.Fatalf("machine-actor was rejected with 403; want admit and proceed past auth gate (body=%s)", rr.Body.String())
	}
}

func TestUpload_RejectsBadContentType(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", strings.NewReader(""))
	req.Header.Set("Content-Type", "text/plain")
	ctx := withAuthAndTenant(req.Context(), adminCred())
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d (body=%s)", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Content-Type") {
		t.Fatalf("error should mention Content-Type; got %s", rr.Body.String())
	}
}

func TestUpload_RejectsMissingManifestYAML(t *testing.T) {
	t.Parallel()
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"manifest_yaml": ""})
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := withAuthAndTenant(req.Context(), adminCred())
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d", rr.Code)
	}
}

func TestUpload_RejectsMissingScfAnchor(t *testing.T) {
	t.Parallel()
	// Inline YAML with no scf_anchor_id — canvas invariant 7 violation.
	yaml := `bundle_schema_version: "1"
bundle_id: no_anchor
title: "no anchor"
implementation_type: automated
`
	h := New(nil, nil)
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"manifest_yaml": yaml})
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := withAuthAndTenant(req.Context(), adminCred())
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "scf_anchor_id") {
		t.Fatalf("error must mention scf_anchor_id; got %s", rr.Body.String())
	}
}

func TestUpload_RejectsMultipartWithoutBundle(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	mp := multipart.NewWriter(&buf)
	// No file part.
	_ = mp.WriteField("unrelated", "value")
	_ = mp.Close()

	h := New(nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:upload-bundle", &buf)
	req.Header.Set("Content-Type", mp.FormDataContentType())
	ctx := withAuthAndTenant(req.Context(), adminCred())
	h.UploadBundle(rr, req.WithContext(ctx))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
	}
}

func adminCred() credstore.Credential {
	return credstore.Credential{
		ID:       "key_admin",
		TenantID: "00000000-0000-0000-0000-000000000001",
		IsAdmin:  true,
	}
}

// silence unused import warnings on platforms where io is unused above
var _ = io.Discard
