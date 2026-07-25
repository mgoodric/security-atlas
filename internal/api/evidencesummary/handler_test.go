// Pure-Go unit tests for the evidence-summary HTTP handler (no Postgres, no
// build tag). They drive the wire-shape transformation + the authz guard
// through a fake summarizer, so the disclosure / non-binding / suppression
// branches record on the fast `go test ./...` surface. The full request path
// (real RLS, real model stub, cross-tenant) lives in the integration tier.
package evidencesummary

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
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakeSummarizer struct {
	sum evidencesummary.Summary
	err error
}

func (f fakeSummarizer) Summarize(_ context.Context, _ uuid.UUID) (evidencesummary.Summary, error) {
	return f.sum, f.err
}

// routerFor wires the single route behind a credential (owner role -> passes
// requireControlRead) and a tenant on the context (so the tenant gate passes).
func routerFor(svc summarizer, tenant string) http.Handler {
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
	r.Get("/v1/controls/{id}/evidence-summary", h.EvidenceSummary)
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

func TestHandler_RendersSummaryWithDisclosure(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	evID := uuid.New()
	sum := evidencesummary.Summary{
		EvidenceSet: evidencesummary.EvidenceSet{
			ControlID:    ctrlID,
			ControlTitle: "Quarterly access review",
			TotalCount:   12,
			Records: []evidencesummary.EvidenceFact{
				{EvidenceID: evID, EvidenceKind: "access_review.completion", Result: "pass"},
			},
		},
		Text:          "The evidence (" + evID.String() + ") shows the access review passed for control (" + ctrlID.String() + ").",
		Citations:     []evidencesummary.Citation{{Kind: evidencesummary.KindEvidence, ID: evID.String()}},
		ModelName:     "llama3.1:8b-instruct-q5",
		ModelVersion:  "1",
		ModelProvider: "ollama-local",
	}
	code, body := doGet(t, routerFor(fakeSummarizer{sum: sum}, tenant),
		"/v1/controls/"+ctrlID.String()+"/evidence-summary")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	ev, ok := body["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence set must always be present (AC-7), got %T", body["evidence"])
	}
	// P0-502-5: current live evidence, clearly labeled; P0-502-8: bounded.
	if ev["live_only"] != true {
		t.Error("evidence set must be marked live_only (P0-502-5)")
	}
	if ev["total"].(float64) != 12 || ev["showing"].(float64) != 1 {
		t.Errorf("bound mislabeled: showing=%v total=%v (want 1 of 12)", ev["showing"], ev["total"])
	}
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary should be an object, got %T", body["summary"])
	}
	// AC-6: visible non-audit-artifact disclosure naming the model.
	disc, _ := summary["disclosure"].(string)
	if disc == "" || !strings.Contains(disc, "not an audit artifact") || !strings.Contains(disc, "llama3.1") {
		t.Errorf("disclosure missing model or non-audit-artifact notice: %q", disc)
	}
	// AC-5: non-binding marker; NO approve/publish/export affordance.
	if summary["binding"] != false {
		t.Errorf("summary must be marked non-binding")
	}
	for _, forbidden := range []string{"approve", "publish", "export", "approval_url", "publish_url"} {
		if _, present := summary[forbidden]; present {
			t.Errorf("summary must not carry a %q affordance (AC-5/P0-502-3)", forbidden)
		}
	}
}

func TestHandler_SuppressedRendersEvidenceOnly(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	sum := evidencesummary.Summary{
		EvidenceSet: evidencesummary.EvidenceSet{ControlID: ctrlID, ControlTitle: "Quarterly access review", TotalCount: 3},
		Suppressed:  true,
		Reason:      evidencesummary.ReasonUnresolvedCitation,
	}
	code, body := doGet(t, routerFor(fakeSummarizer{sum: sum}, tenant),
		"/v1/controls/"+ctrlID.String()+"/evidence-summary")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["evidence"] == nil {
		t.Fatal("evidence set must still render on suppression (AC-7)")
	}
	if body["summary"] != nil {
		t.Fatalf("summary must be null on suppression, got %v", body["summary"])
	}
	if body["suppressed_reason"] != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("suppressed_reason = %v, want %q", body["suppressed_reason"], evidencesummary.ReasonUnresolvedCitation)
	}
}

func TestHandler_ForbidsRoleWithoutControlRead(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	h := newHandlerWith(fakeSummarizer{})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{TenantID: tenant, UserID: "viewer"})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/controls/{id}/evidence-summary", h.EvidenceSummary)
	code, _ := doGet(t, r, "/v1/controls/"+ctrlID.String()+"/evidence-summary")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for role without control-read", code)
	}
}

func TestHandler_BadControlID(t *testing.T) {
	tenant := uuid.NewString()
	code, _ := doGet(t, routerFor(fakeSummarizer{}, tenant), "/v1/controls/not-a-uuid/evidence-summary")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-uuid control id", code)
	}
}

func TestHandler_NotFoundControl(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	svc := fakeSummarizer{err: evidencesummary.ErrNoControl}
	code, _ := doGet(t, routerFor(svc, tenant), "/v1/controls/"+ctrlID.String()+"/evidence-summary")
	if code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when control not found", code)
	}
}
