// helpers_test.go — slice 426 pure-Go branch coverage for
// internal/api/decisions. Per the slice-353 Q-2 fast-loop convention: no
// Postgres, no `//go:build integration` tag, fast `t.Parallel()` table
// tests. Covers:
//
//   - splitCSV (the slice-067 ?constraints= parser: trim, empty-drop,
//     trailing/double comma)
//   - decisionWireFrom / linkageWireFrom / linkSliceWire (the wire mappers,
//     including the nil RevisitBy / SupersededBy nullable branches)
//   - hasProgramRead / requireProgramRead (the slice-067 defense-in-depth
//     role guard — deny on missing credential AND on a bare push credential,
//     allow on admin/approver/owner-role)
//   - writeStoreErr (the store-error → HTTP status mapping for every sentinel)
//   - the handler pre-store rejection branches reachable WITHOUT a DB: a
//     handler with a nil store still returns 401 when the request carries no
//     tenant/credential context, because tenantCredContext fails first. A nil
//     store proves no DB call is made on these paths (P0-426-4).
//
// The happy-path store calls (Create/List/Get/Update/Supersede/AddLink…)
// stay in filters_integration_test.go against real Postgres.
package decisions

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/decision"
)

func TestSplitCSV(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want []string
	}{
		{"time-pressure,cost", []string{"time-pressure", "cost"}},
		{"  spaced , values ", []string{"spaced", "values"}},
		{"trailing,", []string{"trailing"}},
		{"double,,comma", []string{"double", "comma"}},
		{",,,", []string{}},
		{"single", []string{"single"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got := splitCSV(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("splitCSV(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}

func TestDecisionWireFrom(t *testing.T) {
	t.Parallel()
	id := uuid.New()
	supersededID := uuid.New()
	decidedAt := time.Date(2026, 6, 4, 10, 0, 0, 0, time.UTC)
	revisitAt := time.Date(2027, 6, 4, 10, 0, 0, 0, time.UTC)

	t.Run("nullable fields present", func(t *testing.T) {
		t.Parallel()
		d := decision.Decision{
			ID:                   id,
			DecisionID:           "DL-2026-06-04-0001",
			Title:                "Adopt RLS at the DB layer",
			Narrative:            "n",
			Constraints:          []string{"time-pressure"},
			Tradeoffs:            "t",
			DecisionMaker:        "ciso",
			DecidedAt:            decidedAt,
			RevisitBy:            &revisitAt,
			Status:               "active",
			SupersededBy:         &supersededID,
			AuditNarrativeOptOut: true,
		}
		w := decisionWireFrom(d)
		if w.ID != id.String() || w.DecisionID != "DL-2026-06-04-0001" {
			t.Fatalf("id/decision_id not carried: %+v", w)
		}
		if w.RevisitBy == nil || !w.RevisitBy.Equal(revisitAt) {
			t.Fatalf("RevisitBy = %v, want %v", w.RevisitBy, revisitAt)
		}
		if w.SupersededBy == nil || *w.SupersededBy != supersededID.String() {
			t.Fatalf("SupersededBy = %v, want %v", w.SupersededBy, supersededID)
		}
		if !w.AuditNarrativeOptOut {
			t.Fatal("AuditNarrativeOptOut not carried")
		}
		// Constraints must be copied, not aliased.
		d.Constraints[0] = "mutated"
		if w.Constraints[0] == "mutated" {
			t.Fatal("decisionWireFrom aliased the Constraints slice")
		}
	})

	t.Run("nullable fields absent", func(t *testing.T) {
		t.Parallel()
		d := decision.Decision{ID: id, DecidedAt: decidedAt, Status: "active"}
		w := decisionWireFrom(d)
		if w.RevisitBy != nil {
			t.Fatalf("RevisitBy should be nil, got %v", w.RevisitBy)
		}
		if w.SupersededBy != nil {
			t.Fatalf("SupersededBy should be nil, got %v", w.SupersededBy)
		}
		if len(w.Constraints) != 0 {
			t.Fatalf("Constraints should be empty, got %v", w.Constraints)
		}
	})
}

func TestLinkageWireFrom(t *testing.T) {
	t.Parallel()
	target := uuid.New()
	created := time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC)
	lk := decision.Linkage{
		Risks:           []decision.Link{{Kind: decision.LinkRisk, TargetID: target, CreatedAt: created}},
		Controls:        []decision.Link{},
		Exceptions:      nil,
		ScopePredicates: []decision.Link{{Kind: decision.LinkScopePredicate, TargetID: target, CreatedAt: created}},
	}
	w := linkageWireFrom(lk)
	if len(w.Risks) != 1 || w.Risks[0].Kind != string(decision.LinkRisk) || w.Risks[0].TargetID != target.String() {
		t.Fatalf("Risks not mapped: %+v", w.Risks)
	}
	if len(w.Controls) != 0 {
		t.Fatalf("empty Controls should map to empty slice, got %v", w.Controls)
	}
	// nil input slice → non-nil empty output (linkSliceWire makes a sized slice).
	if w.Exceptions == nil {
		t.Fatal("nil Exceptions should map to a non-nil empty slice for stable JSON")
	}
	if len(w.ScopePredicates) != 1 {
		t.Fatalf("ScopePredicates not mapped: %+v", w.ScopePredicates)
	}
}

func TestHasProgramRead(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		cred credstore.Credential
		want bool
	}{
		{"admin", credstore.Credential{IsAdmin: true}, true},
		{"approver", credstore.Credential{IsApprover: true}, true},
		{"owner roles", credstore.Credential{OwnerRoles: []string{"control_owner"}}, true},
		{"bare push credential", credstore.Credential{ID: "k", TenantID: "t"}, false},
		{"empty", credstore.Credential{}, false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := hasProgramRead(tc.cred); got != tc.want {
				t.Fatalf("hasProgramRead(%s) = %v, want %v", tc.name, got, tc.want)
			}
		})
	}
}

func TestRequireProgramRead(t *testing.T) {
	t.Parallel()
	t.Run("no credential → 403", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil)
		if requireProgramRead(rec, r) {
			t.Fatal("requireProgramRead should deny a request with no credential")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("bare push credential → 403", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		ctx := authctx.WithCredential(httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).Context(),
			credstore.Credential{ID: "k", TenantID: "t"})
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).WithContext(ctx)
		if requireProgramRead(rec, r) {
			t.Fatal("requireProgramRead should deny a bare push credential")
		}
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403", rec.Code)
		}
	})
	t.Run("admin credential → allow", func(t *testing.T) {
		t.Parallel()
		rec := httptest.NewRecorder()
		ctx := authctx.WithCredential(httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).Context(),
			credstore.Credential{IsAdmin: true})
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).WithContext(ctx)
		if !requireProgramRead(rec, r) {
			t.Fatal("requireProgramRead should allow an admin credential")
		}
	})
}

func TestWriteStoreErr_StatusMapping(t *testing.T) {
	t.Parallel()
	h := New(nil) // nil store: writeStoreErr never touches it
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"not found → 404", decision.ErrNotFound, http.StatusNotFound},
		{"cross-tenant link → 404", decision.ErrCrossTenantLink, http.StatusNotFound},
		{"wrong state → 409", decision.ErrWrongState, http.StatusConflict},
		{"title required → 400", decision.ErrTitleRequired, http.StatusBadRequest},
		{"decision_maker required → 400", decision.ErrDecisionMakerRequired, http.StatusBadRequest},
		{"decided_at required → 400", decision.ErrDecidedAtRequired, http.StatusBadRequest},
		{"superseded_by required → 400", decision.ErrSupersededByRequired, http.StatusBadRequest},
		{"self supersede → 400", decision.ErrSelfSupersede, http.StatusBadRequest},
		{"invalid link kind → 400", decision.ErrInvalidLinkKind, http.StatusBadRequest},
		{"unknown error → 500", errors.New("boom"), http.StatusInternalServerError},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodGet, "/v1/decisions/x", nil)
			h.writeStoreErr(rec, r, "op", tc.err)
			if rec.Code != tc.want {
				t.Fatalf("writeStoreErr(%v) status = %d, want %d", tc.err, rec.Code, tc.want)
			}
		})
	}
}

// TestHandlers_NoAuthContext_Return401 drives every handler with a nil store
// and NO auth context. tenantCredContext fails first, so each returns 401
// without ever calling the store — proving the missing-context deny branch
// is enforced in the handler (defense-in-depth) and exercising it without a
// DB. ListDecisions is exercised via requireProgramRead above (it 403s
// before tenantCredContext when no credential is present).
func TestHandlers_NoAuthContext_Return401(t *testing.T) {
	t.Parallel()
	h := New(nil)
	handlers := map[string]http.HandlerFunc{
		"CreateDecision": h.CreateDecision,
		"Overdue":        h.Overdue,
		"GetDecision":    h.GetDecision,
		"AuditLog":       h.AuditLog,
		"UpdateDecision": h.UpdateDecision,
		"Supersede":      h.Supersede,
		"AddLink":        h.AddLink,
		"RemoveLink":     h.RemoveLink,
	}
	for name, hf := range handlers {
		name, hf := name, hf
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			r := httptest.NewRequest(http.MethodPost, "/v1/decisions", strings.NewReader("{}"))
			hf(rec, r)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s with no auth context = %d, want 401", name, rec.Code)
			}
		})
	}
}

func TestTenantCredContext_MissingPieces(t *testing.T) {
	t.Parallel()
	h := New(nil)
	t.Run("no credential", func(t *testing.T) {
		t.Parallel()
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("tenantCredContext should fail with no credential")
		}
	})
	t.Run("credential without tenant id", func(t *testing.T) {
		t.Parallel()
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).Context(),
			credstore.Credential{ID: "k"}, // TenantID empty
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).WithContext(ctx)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("tenantCredContext should fail when TenantID is empty")
		}
	})
	t.Run("credential with tenant id but no tenancy context", func(t *testing.T) {
		t.Parallel()
		// Credential present + TenantID set, but no tenancy.WithTenant applied:
		// the tenancy.TenantFromContext guard must still fail.
		ctx := authctx.WithCredential(
			httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).Context(),
			credstore.Credential{ID: "k", TenantID: uuid.NewString()},
		)
		r := httptest.NewRequest(http.MethodGet, "/v1/decisions", nil).WithContext(ctx)
		if _, _, ok := h.tenantCredContext(r); ok {
			t.Fatal("tenantCredContext should fail without a tenancy context")
		}
	})
}
