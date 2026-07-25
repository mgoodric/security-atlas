// Pure-Go unit tests for the portfolio evidence-summary HTTP handler (no
// Postgres, no build tag — slice 750). They drive the cross-control wire-shape,
// BOTH bound labels, the rollup envelope, the non-binding disclosure, the
// suppression branch, the filter-param parsing, and the authz guard through a
// fake portfolioSummarizer. The full request path (real RLS, real model stub,
// cross-tenant) lives in the integration tier.
package evidencesummary

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakePortfolioSummarizer struct {
	sum evidencesummary.PortfolioSummary
	err error
}

func (f fakePortfolioSummarizer) PortfolioSummarize(_ context.Context, _ evidencesummary.PortfolioFilter) (evidencesummary.PortfolioSummary, error) {
	return f.sum, f.err
}

// portfolioRouterFor wires the route behind an owner credential + tenant ctx.
func portfolioRouterFor(svc portfolioSummarizer, tenant string, roles ...string) http.Handler {
	if roles == nil {
		roles = []string{"control_owner"}
	}
	h := newPortfolioHandlerWith(svc)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "owner-test",
				OwnerRoles: roles,
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/evidence-summary/portfolio", h.PortfolioEvidenceSummary)
	return r
}

func samplePortfolioSummary(text string, suppressed bool, reason string) evidencesummary.PortfolioSummary {
	c1 := uuid.New()
	ev1 := uuid.New()
	return evidencesummary.PortfolioSummary{
		Summary: evidencesummary.Summary{
			Text:          text,
			Suppressed:    suppressed,
			Reason:        reason,
			ModelName:     "llama3.1:8b-instruct-q5",
			ModelVersion:  "1",
			ModelProvider: "ollama-local",
		},
		PortfolioSet: evidencesummary.PortfolioSet{
			Filter: evidencesummary.PortfolioFilter{Family: "IAC"},
			Controls: []evidencesummary.ControlEvidence{
				{
					ControlID:    c1,
					ControlTitle: "Quarterly access review",
					TotalCount:   12,
					Records: []evidencesummary.EvidenceFact{
						{EvidenceID: ev1, EvidenceKind: "access_review.completion", Result: "pass"},
					},
				},
			},
			TotalControls: 30,
		},
		Rollup: evidencesummary.Rollup{
			ControlsInSummary:    1,
			TotalMatched:         30,
			ControlsWithEvidence: 1,
			TotalRecords:         1,
		},
	}
}

func TestPortfolioHandler_RendersBothBoundsAndDisclosure(t *testing.T) {
	tenant := uuid.NewString()
	sum := samplePortfolioSummary("A measured summary citing real ids.", false, "")
	code, body := doGet(t, portfolioRouterFor(fakePortfolioSummarizer{sum: sum}, tenant),
		"/v1/evidence-summary/portfolio?family=IAC")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	ev, ok := body["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence rollup must always be present (AC-7), got %T", body["evidence"])
	}
	// AC-5: BOTH bounds labeled. controls_per_summary AND records_per_control.
	if ev["controls_per_summary"].(float64) != float64(evidencesummary.MaxControlsPerSummary) {
		t.Errorf("controls_per_summary mislabeled: %v", ev["controls_per_summary"])
	}
	if ev["records_per_control"].(float64) != float64(evidencesummary.MaxRecordsPerControl) {
		t.Errorf("records_per_control mislabeled: %v", ev["records_per_control"])
	}
	// P0-502-5: live-only marker.
	if ev["live_only"] != true {
		t.Error("rollup must be marked live_only (P0-502-5)")
	}
	if ev["mode"] != "family" {
		t.Errorf("mode = %v, want family", ev["mode"])
	}
	rollup, ok := ev["rollup"].(map[string]any)
	if !ok {
		t.Fatalf("rollup envelope missing, got %T", ev["rollup"])
	}
	if rollup["total_matched"].(float64) != 30 || rollup["controls_in_summary"].(float64) != 1 {
		t.Errorf("rollup K-of-N mislabeled: %+v", rollup)
	}
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary should be an object, got %T", body["summary"])
	}
	// AC-5: non-binding; NO approve/publish/export affordance.
	if summary["binding"] != false {
		t.Error("summary must be non-binding")
	}
	for _, forbidden := range []string{"approve", "publish", "export", "approval_url", "publish_url"} {
		if _, present := summary[forbidden]; present {
			t.Errorf("summary must not carry a %q affordance (AC-5/P0-502-3)", forbidden)
		}
	}
}

func TestPortfolioHandler_SuppressedRendersRollupOnly(t *testing.T) {
	tenant := uuid.NewString()
	sum := samplePortfolioSummary("", true, evidencesummary.ReasonNumericMismatch)
	code, body := doGet(t, portfolioRouterFor(fakePortfolioSummarizer{sum: sum}, tenant),
		"/v1/evidence-summary/portfolio")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["evidence"] == nil {
		t.Fatal("rollup must still render on suppression (AC-7)")
	}
	if body["summary"] != nil {
		t.Fatalf("summary must be null on suppression, got %v", body["summary"])
	}
	if body["suppressed_reason"] != evidencesummary.ReasonNumericMismatch {
		t.Errorf("suppressed_reason = %v, want %q", body["suppressed_reason"], evidencesummary.ReasonNumericMismatch)
	}
}

func TestPortfolioHandler_ForbidsRoleWithoutRead(t *testing.T) {
	tenant := uuid.NewString()
	h := newPortfolioHandlerWith(fakePortfolioSummarizer{})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{TenantID: tenant, UserID: "viewer"})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/evidence-summary/portfolio", h.PortfolioEvidenceSummary)
	code, _ := doGet(t, r, "/v1/evidence-summary/portfolio")
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for role without read", code)
	}
}

func TestPortfolioHandler_BadFrameworkVersionID(t *testing.T) {
	tenant := uuid.NewString()
	code, _ := doGet(t, portfolioRouterFor(fakePortfolioSummarizer{}, tenant),
		"/v1/evidence-summary/portfolio?framework_version_id=not-a-uuid")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-uuid framework_version_id", code)
	}
}

func TestPortfolioHandler_FrameworkFilterAccepted(t *testing.T) {
	tenant := uuid.NewString()
	sum := samplePortfolioSummary("Framework-scoped summary.", false, "")
	fv := uuid.NewString()
	code, body := doGet(t, portfolioRouterFor(fakePortfolioSummarizer{sum: sum}, tenant),
		"/v1/evidence-summary/portfolio?framework_version_id="+fv+"&framework=SOC%202%20(2017)")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for framework filter", code)
	}
	if body["evidence"] == nil {
		t.Fatal("rollup must render for framework-filtered request")
	}
}

func TestPortfolioHandler_InternalErrorOnReadFailure(t *testing.T) {
	tenant := uuid.NewString()
	svc := fakePortfolioSummarizer{err: errors.New("db down")}
	code, _ := doGet(t, portfolioRouterFor(svc, tenant), "/v1/evidence-summary/portfolio")
	if code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 on read failure", code)
	}
}

func TestPortfolioHandler_TenantMissing(t *testing.T) {
	// Credential present (passes authz) but no tenant on the context.
	h := newPortfolioHandlerWith(fakePortfolioSummarizer{})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				UserID: "owner", OwnerRoles: []string{"control_owner"},
			})
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/evidence-summary/portfolio", h.PortfolioEvidenceSummary)
	code, _ := doGet(t, r, "/v1/evidence-summary/portfolio")
	if code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 when tenant context missing", code)
	}
}

// NewPortfolio is the production constructor; exercise it so the public surface is
// covered (it wraps a real *PortfolioService — nil service is fine for the smoke).
func TestNewPortfolio_Constructs(t *testing.T) {
	if NewPortfolio(nil) == nil {
		t.Fatal("NewPortfolio returned nil")
	}
}

func TestPortfolioHandler_WholeProgramNoFilter(t *testing.T) {
	tenant := uuid.NewString()
	sum := samplePortfolioSummary("Measured program-wide summary.", false, "")
	// No filter => whole-program mode; the fake ignores the filter but the
	// handler must accept the no-param request (200, not 400).
	code, body := doGet(t, portfolioRouterFor(fakePortfolioSummarizer{sum: sum}, tenant),
		"/v1/evidence-summary/portfolio")
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for whole-program rollup", code)
	}
	if body["evidence"] == nil {
		t.Fatal("rollup must render for whole-program request")
	}
}
