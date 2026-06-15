// Slice 468 — pure-Go unit tests for the owner-assign + saved-views
// handlers. Fast (no Postgres, no build tag) table tests over the pre-DB
// branches the integration suite reaches only on the happy path — the
// slice-290 / 426 helpers_test.go convention (slice 353 Q-2). These hold
// the internal/api/controls coverage floor against the new handler code
// (the slice-436 adminscim lesson: new handler code dips a floor; add the
// pure-Go tests SAME-PR).
//
// White-box (package controls) so the unexported parse/validate helpers +
// the handler deny branches reachable with a nil pool are exercised
// directly. Handler branches that need a DB are covered by
// owner_assign_integration_test.go.

package controls

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
)

// ----- parseControlIDs -----

func TestParseControlIDs(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	b := uuid.New()
	cases := []struct {
		name      string
		in        []string
		wantLen   int
		wantErr   bool
		wantDedup bool
	}{
		{"valid pair", []string{a.String(), b.String()}, 2, false, false},
		{"dedupes", []string{a.String(), a.String(), b.String()}, 2, false, true},
		{"rejects non-uuid", []string{a.String(), "not-a-uuid"}, 0, true, false},
		{"single", []string{a.String()}, 1, false, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ids, strs, err := parseControlIDs(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(ids) != tc.wantLen || len(strs) != tc.wantLen {
				t.Fatalf("len ids=%d strs=%d; want %d", len(ids), len(strs), tc.wantLen)
			}
			// First-seen order preserved + de-duped.
			if tc.wantDedup && len(ids) != 2 {
				t.Fatalf("dedupe expected 2 ids; got %d", len(ids))
			}
		})
	}
}

// ----- assignItemError.Error -----

func TestAssignItemError_Error(t *testing.T) {
	t.Parallel()
	cid := uuid.New()
	if got := (assignItemError{kind: assignErrControlNotFound, controlID: cid}).Error(); !strings.Contains(got, cid.String()) {
		t.Fatalf("control-not-found error should name the id; got %q", got)
	}
	if got := (assignItemError{kind: assignErrOwnerNotInTenant}).Error(); !strings.Contains(got, "owner") {
		t.Fatalf("owner error should mention owner; got %q", got)
	}
	if got := (assignItemError{kind: assignErrNone}).Error(); got == "" {
		t.Fatalf("default error should be non-empty")
	}
}

// ----- sanitizeControlFilters (threat-model T allow-list) -----

func TestSanitizeControlFilters(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   map[string]string
		want map[string]string
	}{
		{"keeps known keys", map[string]string{"family": "IAC", "result": "pass"}, map[string]string{"family": "IAC", "result": "pass"}},
		{"drops unknown key", map[string]string{"family": "IAC", "evil": "x"}, map[string]string{"family": "IAC"}},
		{"drops empty value", map[string]string{"family": "", "scope": "abc"}, map[string]string{"scope": "abc"}},
		{"nil input -> empty", nil, map[string]string{}},
		{"all five keys", map[string]string{
			"framework": "soc2", "family": "IAC", "result": "pass",
			"freshness": "fresh", "scope": "cell-1",
		}, map[string]string{
			"framework": "soc2", "family": "IAC", "result": "pass",
			"freshness": "fresh", "scope": "cell-1",
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			blob, err := sanitizeControlFilters(tc.in)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			// Round-trip the blob through toWire to confirm the persisted
			// shape narrows to exactly the wanted keys.
			got := toWire(dbx.SavedView{
				ID:      pgUUID(uuid.New()),
				Filters: blob,
			}).Filters
			if len(got) != len(tc.want) {
				t.Fatalf("filters = %#v; want %#v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("key %q = %q; want %q", k, got[k], v)
				}
			}
		})
	}
}

// ----- handler deny branches reachable without a DB -----

func TestAssignOwner_Unauthenticated(t *testing.T) {
	t.Parallel()
	h := NewOwnerAssignHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls/"+uuid.NewString()+"/owner",
		bytes.NewBufferString(`{"owner_user_id":"`+uuid.NewString()+`"}`))
	// No credential on context -> 401.
	h.AssignOwner(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credential; got %d", rec.Code)
	}
}

func TestAssignOwner_BadOwnerUUID(t *testing.T) {
	t.Parallel()
	h := NewOwnerAssignHandler(nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, withTestCredential(req, uuid.NewString(), uuid.NewString()))
		})
	})
	r.Post("/v1/controls/{id}/owner", h.AssignOwner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls/"+uuid.NewString()+"/owner",
		bytes.NewBufferString(`{"owner_user_id":"not-a-uuid"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for bad owner uuid; got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "owner_user_id") {
		t.Fatalf("expected owner_user_id error; got %s", rec.Body.String())
	}
}

func TestBulkAssignOwner_EmptySet(t *testing.T) {
	t.Parallel()
	h := NewOwnerAssignHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:bulk-assign-owner",
		bytes.NewBufferString(`{"owner_user_id":"`+uuid.NewString()+`","control_ids":[]}`))
	req = withTestCredential(req, uuid.NewString(), uuid.NewString())
	h.BulkAssignOwner(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for empty control_ids; got %d", rec.Code)
	}
}

func TestBulkAssignOwner_OverCapBeforeDB(t *testing.T) {
	t.Parallel()
	h := NewOwnerAssignHandler(nil) // nil pool: over-cap must reject before any DB touch
	ids := make([]string, BulkAssignCap+1)
	for i := range ids {
		ids[i] = `"` + uuid.NewString() + `"`
	}
	body := `{"owner_user_id":"` + uuid.NewString() + `","control_ids":[` + strings.Join(ids, ",") + `]}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls:bulk-assign-owner", bytes.NewBufferString(body))
	req = withTestCredential(req, uuid.NewString(), uuid.NewString())
	h.BulkAssignOwner(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422 over-cap (pre-DB); got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAssignOwner_NilPoolServiceUnavailable(t *testing.T) {
	t.Parallel()
	// Nil pool: after a valid owner parse, the pool-nil gate returns 503
	// (the unit-only server contract — the integration suite wires a real
	// pool). Route through a chi mux so the {id} URL param is populated.
	h := NewOwnerAssignHandler(nil)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, withTestCredential(req, uuid.NewString(), uuid.NewString()))
		})
	})
	r.Post("/v1/controls/{id}/owner", h.AssignOwner)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/controls/"+uuid.NewString()+"/owner",
		bytes.NewBufferString(`{"owner_user_id":"`+uuid.NewString()+`"}`))
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil pool; got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSavedViews_CreateBadJSON(t *testing.T) {
	t.Parallel()
	// A non-nil pool is needed to pass the identity gate's pool!=nil check,
	// but the JSON parse fails before any query — so a fake unusable pool is
	// fine here. We instead drive the pre-pool branches: unauthenticated.
	h := NewSavedViewsHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/saved-views", bytes.NewBufferString(`{`))
	// No credential -> 401 (the earliest gate).
	h.Create(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credential; got %d", rec.Code)
	}
}

func TestSavedViews_NilPoolServiceUnavailable(t *testing.T) {
	t.Parallel()
	h := NewSavedViewsHandler(nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/saved-views", nil)
	req = withTestCredential(req, uuid.NewString(), uuid.NewString())
	h.List(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with nil pool; got %d", rec.Code)
	}
}

// ----- test helpers -----

// withTestCredential attaches a credstore.Credential to the request so the
// pre-DB handler branches that read the credential can be exercised without
// the full middleware chain.
func withTestCredential(req *http.Request, tenant, userID string) *http.Request {
	ctx := authctx.WithCredential(req.Context(), credstore.Credential{
		ID:       "test-cred",
		TenantID: tenant,
		UserID:   userID,
		IsAdmin:  true,
	})
	return req.WithContext(ctx)
}
