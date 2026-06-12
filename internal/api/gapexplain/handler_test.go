// Pure-Go unit tests for the gap-explanation HTTP handler (no Postgres, no
// build tag). They drive the wire-shape transformation + the authz guard
// through a fake explainer, so the disclosure / non-binding / suppression
// branches record on the fast `go test ./...` surface. The full request path
// (real RLS, real model stub, cross-tenant) lives in the integration tier.
package gapexplain

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
	"github.com/mgoodric/security-atlas/internal/gapexplain"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeExplainer struct {
	exp gapexplain.Explanation
	err error
}

func (f fakeExplainer) Explain(_ context.Context, _ uuid.UUID) (gapexplain.Explanation, error) {
	return f.exp, f.err
}

// routerFor wires the single route behind a credential (owner role -> passes
// requireControlRead) and a tenant on the context (so the tenant gate passes).
func routerFor(svc explainer, tenant string) http.Handler {
	h := newHandlerWith(svc)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "owner-test",
				OwnerRoles: []string{"control_owner"},
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/controls/{id}/gap-explanation", h.GapExplanation)
	return r
}

func doGet(t *testing.T, h http.Handler, path string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var body map[string]any
	_ = json.NewDecoder(rec.Body).Decode(&body)
	return rec.Code, body
}

func TestHandler_RendersExplanationWithDisclosure(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	exp := gapexplain.Explanation{
		Rollup:        gapexplain.Rollup{ControlID: ctrlID, ControlTitle: "Quarterly access review", IsStale: true, EvidenceCount: 2},
		Text:          "The control is stale (" + ctrlID.String() + ").",
		Citations:     []gapexplain.Citation{{Kind: gapexplain.KindControl, ID: ctrlID.String()}},
		ModelName:     "llama3.1:8b-instruct-q5",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}
	code, body := doGet(t, routerFor(fakeExplainer{exp: exp}, tenant),
		"/v1/controls/"+ctrlID.String()+"/gap-explanation")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["rollup"] == nil {
		t.Fatal("rollup must always be present (AC-7)")
	}
	explanation, ok := body["explanation"].(map[string]any)
	if !ok {
		t.Fatalf("explanation should be an object, got %T", body["explanation"])
	}
	// AC-6: visible non-audit-artifact disclosure naming the model.
	disc, _ := explanation["disclosure"].(string)
	if disc == "" || !strings.Contains(disc, "not an audit artifact") || !strings.Contains(disc, "llama3.1") {
		t.Errorf("disclosure missing model or non-audit-artifact notice: %q", disc)
	}
	// AC-5: non-binding marker; NO approve/publish/export affordance.
	if explanation["binding"] != false {
		t.Errorf("explanation must be marked non-binding")
	}
	for _, forbidden := range []string{"approve", "publish", "export", "approval_url", "publish_url"} {
		if _, present := explanation[forbidden]; present {
			t.Errorf("explanation must not carry a %q affordance (AC-5/P0-444-3)", forbidden)
		}
	}
}

func TestHandler_SuppressedRendersRollupOnly(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	exp := gapexplain.Explanation{
		Rollup:     gapexplain.Rollup{ControlID: ctrlID, ControlTitle: "Quarterly access review", IsStale: true},
		Suppressed: true,
		Reason:     gapexplain.ReasonUnresolvedCitation,
	}
	code, body := doGet(t, routerFor(fakeExplainer{exp: exp}, tenant),
		"/v1/controls/"+ctrlID.String()+"/gap-explanation")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["rollup"] == nil {
		t.Fatal("rollup must still render on suppression (AC-7)")
	}
	if body["explanation"] != nil {
		t.Fatalf("explanation must be null on suppression, got %v", body["explanation"])
	}
	if body["suppressed_reason"] != gapexplain.ReasonUnresolvedCitation {
		t.Errorf("suppressed_reason = %v, want %q", body["suppressed_reason"], gapexplain.ReasonUnresolvedCitation)
	}
}

func TestHandler_ForbidsRoleWithoutControlRead(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	// No control-read role: build a router whose credential carries no signal.
	h := newHandlerWith(fakeExplainer{})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{TenantID: tenant, UserID: "viewer"})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/controls/{id}/gap-explanation", h.GapExplanation)
	code, _ := doGet(t, r, "/v1/controls/"+ctrlID.String()+"/gap-explanation")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for role without control-read", code)
	}
}

func TestHandler_BadControlID(t *testing.T) {
	tenant := uuid.NewString()
	code, _ := doGet(t, routerFor(fakeExplainer{}, tenant), "/v1/controls/not-a-uuid/gap-explanation")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-uuid control id", code)
	}
}
