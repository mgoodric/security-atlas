// Pure-Go unit tests for the slice-749 period-scoped evidence-summary HTTP
// handler (no Postgres, no build tag). They drive the wire-shape transformation
// + the audit-workspace authz guard through a fake periodSummarizer, so the
// frozen-as-of label / non-binding / suppression / not-frozen branches record on
// the fast `go test ./...` surface. The full request path (real RLS, real model
// stub, frozen-population integrity, cross-tenant) lives in the integration tier.
package evidencesummary

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/api/authctx"
	"github.com/mgoodric/security-atlas/internal/api/credstore"
	"github.com/mgoodric/security-atlas/internal/evidencesummary"
	"github.com/mgoodric/security-atlas/internal/tenancy"
)

type fakePeriodSummarizer struct {
	sum evidencesummary.PeriodSummary
	err error
}

func (f fakePeriodSummarizer) PeriodSummarize(_ context.Context, _, _ uuid.UUID) (evidencesummary.PeriodSummary, error) {
	return f.sum, f.err
}

// periodRouterFor wires the period route behind a grc_engineer owner role (passes
// requireAuditWorkspaceRead) and a tenant on the context.
func periodRouterFor(svc periodSummarizer, tenant string) http.Handler {
	h := newPeriodHandlerWith(svc)
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID:   tenant,
				UserID:     "grc-test",
				OwnerRoles: []string{"grc_engineer"},
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/audit-periods/{id}/controls/{controlID}/evidence-summary", h.PeriodEvidenceSummary)
	return r
}

func periodPath(periodID, controlID uuid.UUID) string {
	return "/v1/audit-periods/" + periodID.String() + "/controls/" + controlID.String() + "/evidence-summary"
}

func TestPeriodHandler_RendersSummaryWithFrozenLabel(t *testing.T) {
	tenant := uuid.NewString()
	periodID := uuid.New()
	ctrlID := uuid.New()
	evID := uuid.New()
	frozenAt := time.Date(2026, 3, 31, 23, 59, 59, 0, time.UTC)
	sum := evidencesummary.PeriodSummary{
		Summary: evidencesummary.Summary{
			EvidenceSet: evidencesummary.EvidenceSet{
				ControlID:    ctrlID,
				ControlTitle: "Quarterly access review",
				TotalCount:   9,
				Records: []evidencesummary.EvidenceFact{
					{EvidenceID: evID, EvidenceKind: "access_review.completion", Result: "pass", ObservedAt: frozenAt.Add(-48 * time.Hour)},
				},
			},
			Text:          "The evidence (" + evID.String() + ") shows the access review passed for control (" + ctrlID.String() + ").",
			Citations:     []evidencesummary.Citation{{Kind: evidencesummary.KindEvidence, ID: evID.String()}},
			ModelName:     "llama3.1:8b-instruct-q5",
			ModelVersion:  "1",
			ModelProvider: "ollama-local",
		},
		AuditPeriodID: periodID,
		FrozenAt:      frozenAt,
	}
	code, body := doGet(t, periodRouterFor(fakePeriodSummarizer{sum: sum}, tenant), periodPath(periodID, ctrlID))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	ev, ok := body["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("evidence set must always be present (AC-7), got %T", body["evidence"])
	}
	// AC-4 / P0-749-1: frozen audit-period population, clearly labeled + frozen-as-of.
	if ev["frozen"] != true {
		t.Error("evidence set must be marked frozen (P0-749-1, AC-4)")
	}
	if fa, _ := ev["frozen_at"].(string); !strings.HasPrefix(fa, "2026-03-31") {
		t.Errorf("frozen_at label missing/wrong: %q", ev["frozen_at"])
	}
	if ev["audit_period_id"] != periodID.String() {
		t.Errorf("audit_period_id = %v, want %s", ev["audit_period_id"], periodID)
	}
	if ev["total"].(float64) != 9 || ev["showing"].(float64) != 1 {
		t.Errorf("bound mislabeled: showing=%v total=%v (want 1 of 9)", ev["showing"], ev["total"])
	}
	// The frozen surface must NOT carry the live_only marker (it is not live).
	if _, present := ev["live_only"]; present {
		t.Error("period-scoped (frozen) evidence set must not carry the live_only marker")
	}
	summary, ok := body["summary"].(map[string]any)
	if !ok {
		t.Fatalf("summary should be an object, got %T", body["summary"])
	}
	disc, _ := summary["disclosure"].(string)
	if disc == "" || !strings.Contains(disc, "not an audit artifact") || !strings.Contains(disc, "llama3.1") {
		t.Errorf("disclosure missing model or non-audit-artifact notice: %q", disc)
	}
	// AC-4: non-binding marker; NO approve/publish/export affordance.
	if summary["binding"] != false {
		t.Errorf("summary must be marked non-binding")
	}
	for _, forbidden := range []string{"approve", "publish", "export", "approval_url", "publish_url"} {
		if _, present := summary[forbidden]; present {
			t.Errorf("summary must not carry a %q affordance (AC-4/P0-502-3)", forbidden)
		}
	}
}

func TestPeriodHandler_SuppressedRendersFrozenEvidenceOnly(t *testing.T) {
	tenant := uuid.NewString()
	periodID := uuid.New()
	ctrlID := uuid.New()
	sum := evidencesummary.PeriodSummary{
		Summary: evidencesummary.Summary{
			EvidenceSet: evidencesummary.EvidenceSet{ControlID: ctrlID, ControlTitle: "Quarterly access review", TotalCount: 3},
			Suppressed:  true,
			Reason:      evidencesummary.ReasonUnresolvedCitation,
		},
		AuditPeriodID: periodID,
		FrozenAt:      time.Now().UTC(),
	}
	code, body := doGet(t, periodRouterFor(fakePeriodSummarizer{sum: sum}, tenant), periodPath(periodID, ctrlID))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if body["evidence"] == nil {
		t.Fatal("frozen evidence set must still render on suppression (AC-7)")
	}
	if body["summary"] != nil {
		t.Fatalf("summary must be null on suppression, got %v", body["summary"])
	}
	if body["suppressed_reason"] != evidencesummary.ReasonUnresolvedCitation {
		t.Errorf("suppressed_reason = %v, want %q", body["suppressed_reason"], evidencesummary.ReasonUnresolvedCitation)
	}
}

func TestPeriodHandler_ForbidsRoleWithoutAuditWorkspaceRead(t *testing.T) {
	tenant := uuid.NewString()
	periodID := uuid.New()
	ctrlID := uuid.New()
	h := newPeriodHandlerWith(fakePeriodSummarizer{})
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			// control_owner is NOT sufficient for the audit-workspace read.
			ctx := authctx.WithCredential(req.Context(), credstore.Credential{
				TenantID: tenant, UserID: "viewer", OwnerRoles: []string{"control_owner"},
			})
			ctx, _ = tenancy.WithTenant(ctx, tenant)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	})
	r.Get("/v1/audit-periods/{id}/controls/{controlID}/evidence-summary", h.PeriodEvidenceSummary)
	code, _ := doGet(t, r, periodPath(periodID, ctrlID))
	if code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 for role without audit-workspace read", code)
	}
}

func TestPeriodHandler_BadIDs(t *testing.T) {
	tenant := uuid.NewString()
	ctrlID := uuid.New()
	periodID := uuid.New()
	// Bad period id.
	code, _ := doGet(t, periodRouterFor(fakePeriodSummarizer{}, tenant),
		"/v1/audit-periods/not-a-uuid/controls/"+ctrlID.String()+"/evidence-summary")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-uuid period id", code)
	}
	// Bad control id.
	code, _ = doGet(t, periodRouterFor(fakePeriodSummarizer{}, tenant),
		"/v1/audit-periods/"+periodID.String()+"/controls/not-a-uuid/evidence-summary")
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for non-uuid control id", code)
	}
}

func TestPeriodHandler_NotFoundAndNotFrozen(t *testing.T) {
	tenant := uuid.NewString()
	periodID := uuid.New()
	ctrlID := uuid.New()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{"period not found", evidencesummary.ErrNoPeriod, http.StatusNotFound},
		{"control not found", evidencesummary.ErrNoControl, http.StatusNotFound},
		{"period not frozen", evidencesummary.ErrPeriodNotFrozen, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := fakePeriodSummarizer{err: tc.err}
			code, _ := doGet(t, periodRouterFor(svc, tenant), periodPath(periodID, ctrlID))
			if code != tc.want {
				t.Fatalf("status = %d, want %d for %s", code, tc.want, tc.name)
			}
		})
	}
}
