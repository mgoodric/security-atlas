// helpers_test.go — slice 426 pure-Go branch coverage for
// internal/api/policies. Per the slice-353 Q-2 fast-loop convention: no
// Postgres, no `//go:build integration` tag, fast `t.Parallel()` table
// tests. Complements the existing ack_rate_unit_test.go + export_test.go by
// covering the remaining pure-Go residue:
//
//   - parseUUIDs (trim, empty-drop, invalid-UUID error) + uuidsToStrings
//   - pgUUIDsToStrings (skips invalid entries) + timestamptzPtr
//   - wireFromPolicy / wireFromAckRateBaseColumns nullable + orphan-warning
//     branches
//   - the handler error mappers: writeCreateErr / writeTransitionErr /
//     writePublishErr (every sentinel → its HTTP status)
//   - the handler authz/identity DENY branches reachable WITHOUT a DB: a
//     handler with a nil store returns 401 on missing tenant context, and
//     Approve/Publish return 403 to a non-approver — both before any store
//     call (P0-426-3 asserts the deny branch, P0-426-4 a nil store proves no
//     DB is touched)
//   - tenantCredContext deny branches
//
// The happy-path store transitions stay in the *_integration_test.go suites.
package policies

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/db/dbx"
	"github.com/mgoodric/security-atlas/internal/policy"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

func TestParseUUIDs(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	t.Run("valid with trim and empty-drop", func(t *testing.T) {
		t.Parallel()
		got, err := parseUUIDs([]string{" " + a.String() + " ", "", b.String()})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if len(got) != 2 || got[0] != a || got[1] != b {
			t.Fatalf("parseUUIDs result = %v", got)
		}
	})
	t.Run("invalid uuid errors", func(t *testing.T) {
		t.Parallel()
		if _, err := parseUUIDs([]string{"not-a-uuid"}); err == nil {
			t.Fatal("expected error for invalid uuid")
		}
	})
	t.Run("nil input → empty slice", func(t *testing.T) {
		t.Parallel()
		got, err := parseUUIDs(nil)
		if err != nil || len(got) != 0 {
			t.Fatalf("parseUUIDs(nil) = (%v, %v)", got, err)
		}
	})
}

func TestUuidsToStrings(t *testing.T) {
	t.Parallel()
	a, b := uuid.New(), uuid.New()
	got := uuidsToStrings([]uuid.UUID{a, b})
	want := []string{a.String(), b.String()}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("uuidsToStrings = %v, want %v", got, want)
	}
}

func TestPgUUIDsToStrings_SkipsInvalid(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	in := []pgtype.UUID{
		{Bytes: a, Valid: true},
		{Valid: false}, // must be skipped
	}
	got := pgUUIDsToStrings(in)
	if len(got) != 1 || got[0] != a.String() {
		t.Fatalf("pgUUIDsToStrings = %v, want [%s]", got, a)
	}
	if len(pgUUIDsToStrings(nil)) != 0 {
		t.Fatal("pgUUIDsToStrings(nil) should be empty")
	}
}

func TestTimestamptzPtr(t *testing.T) {
	t.Parallel()
	if timestamptzPtr(pgtype.Timestamptz{}) != nil {
		t.Fatal("invalid timestamptz should map to nil")
	}
	ts := time.Now().UTC()
	got := timestamptzPtr(pgtype.Timestamptz{Time: ts, Valid: true})
	if got == nil || !got.Equal(ts) {
		t.Fatalf("timestamptzPtr = %v, want %v", got, ts)
	}
}

func TestWireFromPolicy_OrphanWarningAndNullables(t *testing.T) {
	t.Parallel()
	pred := uuid.New()
	eff := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	t.Run("orphan + nullables set", func(t *testing.T) {
		t.Parallel()
		p := policy.Policy{
			ID:            uuid.New(),
			Title:         "T",
			Status:        policy.StateDraft,
			PredecessorID: &pred,
			EffectiveDate: &eff,
			// no linked controls → orphan
		}
		w := wireFromPolicy(p)
		if len(w.Warnings) == 0 || w.Warnings[0] != policy.WarningOrphanPolicy {
			t.Fatalf("expected orphan warning, got %v", w.Warnings)
		}
		if w.PredecessorID == nil || *w.PredecessorID != pred.String() {
			t.Fatalf("PredecessorID not carried: %v", w.PredecessorID)
		}
		if w.EffectiveDate == nil || *w.EffectiveDate != "2026-06-04" {
			t.Fatalf("EffectiveDate = %v, want 2026-06-04", w.EffectiveDate)
		}
	})
	t.Run("non-orphan, no nullables", func(t *testing.T) {
		t.Parallel()
		p := policy.Policy{
			ID:               uuid.New(),
			Title:            "T",
			Status:           policy.StatePublished,
			LinkedControlIDs: []uuid.UUID{uuid.New()},
		}
		w := wireFromPolicy(p)
		for _, warn := range w.Warnings {
			if warn == policy.WarningOrphanPolicy {
				t.Fatal("non-orphan policy should not carry the orphan warning")
			}
		}
		if w.PredecessorID != nil || w.EffectiveDate != nil {
			t.Fatal("absent nullables should map to nil")
		}
	})
}

func TestWireFromAckRateBaseColumns_OrphanWarning(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	t.Run("zero linked controls → orphan warning", func(t *testing.T) {
		t.Parallel()
		row := dbx.ListPoliciesWithAckRateRow{
			ID:     pgtype.UUID{Bytes: id, Valid: true},
			Title:  "T",
			Status: policy.StateDraft,
		}
		w := wireFromAckRateBaseColumns(row)
		if len(w.Warnings) == 0 || w.Warnings[0] != policy.WarningOrphanPolicy {
			t.Fatalf("expected orphan warning, got %v", w.Warnings)
		}
	})
	t.Run("with linked control → no orphan warning + predecessor/date", func(t *testing.T) {
		t.Parallel()
		pred := uuid.New()
		eff := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
		row := dbx.ListPoliciesWithAckRateRow{
			ID:               pgtype.UUID{Bytes: id, Valid: true},
			Title:            "T",
			Status:           policy.StatePublished,
			LinkedControlIds: []pgtype.UUID{{Bytes: uuid.New(), Valid: true}},
			PredecessorID:    pgtype.UUID{Bytes: pred, Valid: true},
			EffectiveDate:    pgtype.Date{Time: eff, Valid: true},
		}
		w := wireFromAckRateBaseColumns(row)
		for _, warn := range w.Warnings {
			if warn == policy.WarningOrphanPolicy {
				t.Fatal("policy with a linked control should not be orphan")
			}
		}
		if w.PredecessorID == nil || *w.PredecessorID != pred.String() {
			t.Fatalf("PredecessorID not carried: %v", w.PredecessorID)
		}
		if w.EffectiveDate == nil || *w.EffectiveDate != "2026-06-04" {
			t.Fatalf("EffectiveDate = %v", w.EffectiveDate)
		}
	})
}

func TestWriteCreateErr(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"title required", policy.ErrTitleRequired, http.StatusBadRequest},
		{"version required", policy.ErrVersionRequired, http.StatusBadRequest},
		{"body required", policy.ErrBodyRequired, http.StatusBadRequest},
		{"owner role required", policy.ErrOwnerRoleRequired, http.StatusBadRequest},
		{"approver role required", policy.ErrApproverRoleRequired, http.StatusBadRequest},
		{"created_by required", policy.ErrCreatedByRequired, http.StatusBadRequest},
		{"unknown → 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.writeCreateErr(rec, httptest.NewRequest(http.MethodPost, "/v1/policies", nil), tc.err)
			if rec.Code != tc.want {
				t.Fatalf("writeCreateErr(%v) = %d, want %d", tc.err, rec.Code, tc.want)
			}
		})
	}
}

func TestWriteTransitionErr(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cases := []struct {
		err  error
		want int
	}{
		{policy.ErrNotFound, http.StatusNotFound},
		{policy.ErrWrongState, http.StatusConflict},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.writeTransitionErr(rec, httptest.NewRequest(http.MethodPost, "/x", nil), tc.err)
			if rec.Code != tc.want {
				t.Fatalf("writeTransitionErr(%v) = %d, want %d", tc.err, rec.Code, tc.want)
			}
		})
	}
}

func TestWritePublishErr(t *testing.T) {
	t.Parallel()
	h := New(nil)
	cases := []struct {
		err  error
		want int
	}{
		{policy.ErrOrphanPublish, http.StatusConflict},
		{policy.ErrNotFound, http.StatusNotFound},
		{policy.ErrWrongState, http.StatusConflict},
		{policy.ErrInvalidVersion, http.StatusBadRequest},
		{errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			h.writePublishErr(rec, httptest.NewRequest(http.MethodPost, "/x", nil), tc.err)
			if rec.Code != tc.want {
				t.Fatalf("writePublishErr(%v) = %d, want %d", tc.err, rec.Code, tc.want)
			}
		})
	}
}

func TestTenantCredContext_DenyBranches(t *testing.T) {
	t.Parallel()
	h := New(nil)
	t.Run("no credential", func(t *testing.T) {
		t.Parallel()
		r := httptest.NewRequest(http.MethodGet, "/v1/policies", nil)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("should deny with no credential")
		}
	})
	t.Run("empty tenant id", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/policies", nil).Context(),
			credstore.Credential{ID: "k"},
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/policies", nil).WithContext(ctx)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("should deny with empty TenantID")
		}
	})
	t.Run("no tenancy context", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/policies", nil).Context(),
			credstore.Credential{ID: "k", TenantID: uuid.NewString()},
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/policies", nil).WithContext(ctx)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("should deny without a tenancy context")
		}
	})
}

// TestHandlers_NoAuthContext_Return401 drives the read/write handlers with a
// nil store and no auth context: each returns 401 before any store call.
func TestHandlers_NoAuthContext_Return401(t *testing.T) {
	t.Parallel()
	h := New(nil)
	handlers := map[string]http.HandlerFunc{
		"CreatePolicy": h.CreatePolicy,
		"ListPolicies": h.ListPolicies,
		"GetPolicy":    h.GetPolicy,
		"Submit":       h.Submit,
		"Approve":      h.Approve,
		"Publish":      h.Publish,
	}
	for name, hf := range handlers {
		name, hf := name, hf
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/policies", strings.NewReader("{}"))
			hf(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s with no auth context = %d, want 401", name, rec.Code)
			}
		})
	}
}

// TestApprovePublish_NonApprover_Return403 asserts the audit-binding
// transitions reject a non-approver credential with 403 — the elevation
// deny branch P0-426-3 names — before any store call (nil store).
func TestApprovePublish_NonApprover_Return403(t *testing.T) {
	t.Parallel()
	h := New(nil)
	tenant := uuid.NewString()
	build := func() *http.Request {
		base := httptest.NewRequest(http.MethodPost, "/v1/policies/x/approve", nil)
		ctx := authctx.WithCredential(base.Context(),
			credstore.Credential{ID: "k", TenantID: tenant}) // not approver, not admin
		ctx, err := tenancy.WithTenant(ctx, tenant)
		if err != nil {
			t.Fatalf("WithTenant: %v", err)
		}
		// attach a chi route ctx so URLParam("id") resolves (unused on the
		// 403 path, but keeps the request well-formed).
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", uuid.NewString())
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
		return base.WithContext(ctx)
	}
	for name, hf := range map[string]http.HandlerFunc{"Approve": h.Approve, "Publish": h.Publish} {
		name, hf := name, hf
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			hf(rec, build())
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s with non-approver = %d, want 403", name, rec.Code)
			}
		})
	}
}
