package controls

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

// withChiID injects {id} into chi's route context so handlers calling
// chi.URLParam(r, "id") see the value even when tests bypass the
// router (chi's URLParam reads its own context key, not the URL path).
func withChiID(r *http.Request, id string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// Slice 011 — unit tests for the attestation handler's request-shape
// branches. These run without a Postgres dependency; the persistence
// path (DB-backed control lookup + ingest.Service.Process write) lives
// behind the //go:build integration tag.

func ownerCred(roles ...string) credstore.Credential {
	return credstore.Credential{
		ID:         "key_owner",
		TenantID:   "00000000-0000-0000-0000-000000000001",
		UserID:     "key_owner",
		OwnerRoles: append([]string(nil), roles...),
	}
}

func TestAttestForm_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/controls/00000000-0000-0000-0000-000000000010/attest-form", nil)
	h.AttestForm(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", rr.Code)
	}
}

func TestAttestSubmit_RequiresAuth(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/v1/controls/00000000-0000-0000-0000-000000000010/attestations",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	h.Submit(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401; got %d", rr.Code)
	}
}

func TestAttestSubmit_RejectsBadUUID(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/v1/controls/not-a-uuid/attestations",
		strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	ctx := authctx.WithCredential(req.Context(), ownerCred("control_owner"))
	// chi extracts {id} via URLParam; tests bypass the router so we
	// inject the id via the same chi context the router would set up.
	h.Submit(rr, withChiID(req.WithContext(ctx), "not-a-uuid"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "uuid") {
		t.Fatalf("error must mention uuid; got %s", rr.Body.String())
	}
}

func TestAttestSubmit_RejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/v1/controls/00000000-0000-0000-0000-000000000010/attestations",
		strings.NewReader(`{not json`))
	req.Header.Set("Content-Type", "application/json")
	ctx := authctx.WithCredential(req.Context(), ownerCred("control_owner"))
	h.Submit(rr, withChiID(req.WithContext(ctx), "00000000-0000-0000-0000-000000000010"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestAttestSubmit_RejectsMissingFields(t *testing.T) {
	t.Parallel()
	h := NewAttestHandler(nil, nil, nil)
	rr := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]any{
		// statement omitted on purpose
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/controls/00000000-0000-0000-0000-000000000010/attestations",
		bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := authctx.WithCredential(req.Context(), ownerCred("control_owner"))
	h.Submit(rr, withChiID(req.WithContext(ctx), "00000000-0000-0000-0000-000000000010"))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "statement") {
		t.Fatalf("error must mention statement; got %s", rr.Body.String())
	}
}

// #2111: a fail needs a fail_reason, and result is pass or fail only. Both
// are refused before the control is read.
func TestAttestSubmit_RejectsBadResult(t *testing.T) {
	t.Parallel()
	cases := map[string]map[string]any{
		"fail without reason":    {"statement": "s", "result": "fail"},
		"fail with blank reason": {"statement": "s", "result": "fail", "fail_reason": "   "},
		"unknown result":         {"statement": "s", "result": "na"},
	}
	for name, payload := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := NewAttestHandler(nil, nil, nil)
			rr := httptest.NewRecorder()
			body, _ := json.Marshal(payload)
			req := httptest.NewRequest(http.MethodPost,
				"/v1/controls/00000000-0000-0000-0000-000000000010/attestations",
				bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			ctx := authctx.WithCredential(req.Context(), ownerCred("control_owner"))
			h.Submit(rr, withChiID(req.WithContext(ctx), "00000000-0000-0000-0000-000000000010"))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected 400; got %d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

// #2509: observed_at more than attestMaxFutureSkew ahead is refused with a
// 422 before the control is read. A small skew passes this check (and
// reaches the nil reader's 503).
func TestAttestSubmit_RejectsFutureObservedAt(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		ahead time.Duration
		want  int
	}{
		"a day ahead":     {24 * time.Hour, http.StatusUnprocessableEntity},
		"six minutes":     {6 * time.Minute, http.StatusUnprocessableEntity},
		"four minutes":    {4 * time.Minute, http.StatusServiceUnavailable},
		"an hour earlier": {-time.Hour, http.StatusServiceUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := NewAttestHandler(nil, nil, nil)
			rr := httptest.NewRecorder()
			body, _ := json.Marshal(map[string]any{
				"statement":   "s",
				"observed_at": time.Now().UTC().Add(tc.ahead).Format(time.RFC3339Nano),
			})
			req := httptest.NewRequest(http.MethodPost,
				"/v1/controls/00000000-0000-0000-0000-000000000010/attestations",
				bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			ctx := authctx.WithCredential(req.Context(), ownerCred("control_owner"))
			h.Submit(rr, withChiID(req.WithContext(ctx), "00000000-0000-0000-0000-000000000010"))
			if rr.Code != tc.want {
				t.Fatalf("expected %d; got %d body=%s", tc.want, rr.Code, rr.Body.String())
			}
		})
	}
}

// supersededKeyReader is a control reader whose idempotency lookup finds a
// record under every key, each overtaken by a newer attestation, so a
// keyless request walks the whole key chain (#2520).
type supersededKeyReader struct {
	stubControlReader
	lookups int
}

func (s *supersededKeyReader) AttestationByIdempotencyKey(_ context.Context, _ string, _ uuid.UUID, _ string) (priorAttestation, bool, error) {
	s.lookups++
	return priorAttestation{observedAt: time.Now().UTC(), superseded: true}, true, nil
}

// #2520: a keyless caller that exhausts the key chain gets a 409 telling it
// to send an idempotency_key, not a 500.
func TestAttestSubmit_KeyChainExhausted_Conflict(t *testing.T) {
	t.Parallel()
	reader := &supersededKeyReader{stubControlReader: stubControlReader{row: manualControlRow()}}
	h := newAttestHandlerWithReader(reader)
	body, _ := json.Marshal(map[string]any{
		"statement": "Quarterly review complete.",
		"attestation_data": map[string]any{
			"reviewer":     "sample-user@example.com",
			"completed_on": "2026-11-01",
		},
	})
	req := httptest.NewRequest(http.MethodPost,
		"/v1/controls/"+attestControlID+"/attestations", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := authctx.WithCredential(req.Context(), ownerCred(attestOwnerRole))
	ctx, err := tenancy.WithTenant(ctx, attestTenantID)
	if err != nil {
		t.Fatalf("with tenant: %v", err)
	}
	rr := httptest.NewRecorder()
	h.Submit(rr, withChiID(req.WithContext(ctx), attestControlID))

	if rr.Code != http.StatusConflict {
		t.Fatalf("expected 409; got %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "send an idempotency_key") {
		t.Fatalf("409 must tell the caller to send an idempotency_key; body=%s", rr.Body.String())
	}
	if reader.lookups != attestMaxKeyGenerations {
		t.Fatalf("expected %d key lookups; got %d", attestMaxKeyGenerations, reader.lookups)
	}
}

func TestAttestResult(t *testing.T) {
	t.Parallel()
	cases := []struct {
		result, reason         string
		wantResult, wantReason string
		wantErr                bool
	}{
		{"", "", "pass", "", false},
		{"pass", "", "pass", "", false},
		{"pass", "ignored", "pass", "", false},
		{"fail", " broken ", "fail", "broken", false},
		{"fail", "", "", "", true},
		{"FAIL", "x", "", "", true},
	}
	for _, tc := range cases {
		gotResult, gotReason, err := attestResult(tc.result, tc.reason)
		if (err != nil) != tc.wantErr || gotResult != tc.wantResult || gotReason != tc.wantReason {
			t.Errorf("attestResult(%q, %q) = (%q, %q, %v)", tc.result, tc.reason, gotResult, gotReason, err)
		}
	}
}
