package controlstate

// #2110 — POST /v1/controls/{id}/evaluate, pure-Go tier. The runner and the
// owner lookup are stubbed, so these cover the gate (per role and per
// credential kind), the single-flight, the timeout and the wire shape with
// no Postgres. The integration test runs the real engine.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/eval"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

const (
	evalTenantID  = "00000000-0000-4000-8000-000000002110"
	evalControlID = "00000000-0000-4000-8000-0000000021c0"
	evalOwnerID   = "00000000-0000-4000-8000-0000000021a0"
	evalOtherID   = "00000000-0000-4000-8000-0000000021b0"
)

type stubRunner struct {
	calls   atomic.Int32
	release chan struct{} // when non-nil, EvaluateControl blocks on it
	evalErr error
}

func (s *stubRunner) EvaluateControl(ctx context.Context, _ uuid.UUID, trigger string, _ time.Time) (int, error) {
	s.calls.Add(1)
	if trigger != eval.TriggerManual {
		return 0, context.Canceled
	}
	if s.release != nil {
		select {
		case <-s.release:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return 1, s.evalErr
}

func (s *stubRunner) ControlState(_ context.Context, _ uuid.UUID, _ string, _ time.Time) ([]eval.State, error) {
	return []eval.State{{
		Result:          eval.ResultPass,
		FreshnessStatus: "fresh",
		FreshnessClass:  "daily",
		Trigger:         eval.TriggerManual,
		EvaluatedAt:     time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC),
	}}, nil
}

func newEvalTestHandler(r *stubRunner, owner string) *EvaluateHandler {
	return &EvaluateHandler{
		runner:  r,
		ownerOf: func(context.Context, uuid.UUID) (string, error) { return owner, nil },
		timeout: evaluateTimeout,
	}
}

func doEvaluate(t *testing.T, h *EvaluateHandler, cred credstore.Credential) *httptest.ResponseRecorder {
	t.Helper()
	router := chi.NewRouter()
	router.Post("/v1/controls/{id}/evaluate", h.Evaluate)
	ctx, err := tenancy.WithTenant(context.Background(), evalTenantID)
	if err != nil {
		t.Fatalf("WithTenant: %v", err)
	}
	ctx = authctx.WithCredential(ctx, cred)
	req := httptest.NewRequest(http.MethodPost, "/v1/controls/"+evalControlID+"/evaluate", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestEvaluate_Gate(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want int
	}{
		{"admin flag", credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: evalTenantID, IsAdmin: true}, http.StatusOK},
		{"tenant admin role (JWT)", credstore.Credential{ID: "jwt:a", UserID: "user:" + evalOtherID, TenantID: evalTenantID, TenantRoles: []string{"admin"}}, http.StatusOK},
		{"owner via JWT session", credstore.Credential{ID: "jwt:o", UserID: "user:" + evalOwnerID, TenantID: evalTenantID, TenantRoles: []string{"control_owner"}}, http.StatusOK},
		{"owner via user-bound key", credstore.Credential{ID: "key_o", UserID: evalOwnerID, TenantID: evalTenantID, OwnerRoles: []string{"control_owner"}}, http.StatusOK},
		{"grc_engineer non-owner", credstore.Credential{ID: "jwt:g", UserID: "user:" + evalOtherID, TenantID: evalTenantID, TenantRoles: []string{"grc_engineer"}}, http.StatusForbidden},
		{"control_owner non-owner", credstore.Credential{ID: "jwt:c", UserID: "user:" + evalOtherID, TenantID: evalTenantID, TenantRoles: []string{"control_owner"}}, http.StatusForbidden},
		{"viewer", credstore.Credential{ID: "jwt:v", UserID: "user:" + evalOtherID, TenantID: evalTenantID, TenantRoles: []string{"viewer"}}, http.StatusForbidden},
		{"auditor", credstore.Credential{ID: "jwt:u", UserID: "user:" + evalOtherID, TenantID: evalTenantID, TenantRoles: []string{"auditor"}}, http.StatusForbidden},
		{"unbound key", credstore.Credential{ID: "key_u", UserID: "key_u", TenantID: evalTenantID, OwnerRoles: []string{"control_owner"}}, http.StatusForbidden},
		{"JWT role claim admin does not count", credstore.Credential{ID: "jwt:r", UserID: "user:" + evalOtherID, TenantID: evalTenantID, OwnerRoles: []string{"admin"}}, http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := &stubRunner{}
			rec := doEvaluate(t, newEvalTestHandler(r, evalOwnerID), tc.cred)
			if rec.Code != tc.want {
				t.Fatalf("status = %d; want %d; body %s", rec.Code, tc.want, rec.Body.String())
			}
			wantCalls := int32(0)
			if tc.want == http.StatusOK {
				wantCalls = 1
			}
			if got := r.calls.Load(); got != wantCalls {
				t.Fatalf("EvaluateControl calls = %d; want %d", got, wantCalls)
			}
		})
	}
}

func TestEvaluate_NoAssignedOwnerRefusesNonAdmin(t *testing.T) {
	t.Parallel()
	r := &stubRunner{}
	cred := credstore.Credential{ID: "jwt:o", UserID: "user:" + evalOwnerID, TenantID: evalTenantID, TenantRoles: []string{"control_owner"}}
	if rec := doEvaluate(t, newEvalTestHandler(r, ""), cred); rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d; want 403", rec.Code)
	}
}

func TestEvaluate_ReturnsNewState(t *testing.T) {
	t.Parallel()
	cred := credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: evalTenantID, IsAdmin: true}
	rec := doEvaluate(t, newEvalTestHandler(&stubRunner{}, ""), cred)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ControlID string      `json:"control_id"`
		Count     int         `json:"count"`
		States    []stateWire `json:"states"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ControlID != evalControlID || body.Count != 1 || body.States[0].Result != eval.ResultPass || body.States[0].Trigger != eval.TriggerManual {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestEvaluate_ConcurrentCallsShareOneEvaluation(t *testing.T) {
	t.Parallel()
	r := &stubRunner{release: make(chan struct{})}
	h := newEvalTestHandler(r, "")
	cred := credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: evalTenantID, IsAdmin: true}

	const n = 5
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = doEvaluate(t, h, cred).Code
		}(i)
	}
	// Let every caller join the in-flight call before it finishes.
	deadline := time.Now().Add(2 * time.Second)
	for r.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	close(r.release)
	wg.Wait()

	if got := r.calls.Load(); got != 1 {
		t.Fatalf("EvaluateControl calls = %d; want 1", got)
	}
	for i, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("caller %d status = %d; want 200", i, c)
		}
	}
}

func TestEvaluate_Timeout(t *testing.T) {
	t.Parallel()
	r := &stubRunner{release: make(chan struct{})}
	h := newEvalTestHandler(r, "")
	h.timeout = 20 * time.Millisecond
	cred := credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: evalTenantID, IsAdmin: true}
	if rec := doEvaluate(t, h, cred); rec.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d; want 504", rec.Code)
	}
}

func TestEvaluate_UnknownControlIs404(t *testing.T) {
	t.Parallel()
	r := &stubRunner{evalErr: eval.ErrControlNotFound}
	cred := credstore.Credential{ID: "key_admin", UserID: "key_admin", TenantID: evalTenantID, IsAdmin: true}
	if rec := doEvaluate(t, newEvalTestHandler(r, ""), cred); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d; want 404", rec.Code)
	}
}
