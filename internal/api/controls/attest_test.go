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

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
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

func plainCred() credstore.Credential {
	return credstore.Credential{
		ID:       "key_plain",
		TenantID: "00000000-0000-0000-0000-000000000001",
		UserID:   "key_plain",
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

func TestCredential_HasOwnerRole(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		cred credstore.Credential
		role string
		want bool
	}{
		{"owner with matching role", ownerCred("control_owner"), "control_owner", true},
		{"owner without matching role", ownerCred("other_role"), "control_owner", false},
		{"plain credential", plainCred(), "control_owner", false},
		{"admin is wildcard", credstore.Credential{IsAdmin: true}, "control_owner", true},
		{"empty role rejects non-admin", ownerCred("control_owner"), "", false},
		{"empty role accepts admin", credstore.Credential{IsAdmin: true}, "", true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := tc.cred.HasOwnerRole(tc.role); got != tc.want {
				t.Fatalf("HasOwnerRole(%q) = %v; want %v", tc.role, got, tc.want)
			}
		})
	}
}
