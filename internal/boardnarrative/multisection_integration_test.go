//go:build integration

// Slice 501 — integration tests for the FULL multi-section board narrative.
// Real Postgres + the real boardnarrative.Store (RLS-backed rollup + citation
// resolution + per-section draft persistence + approval) + the llm.StubClient
// (NO live Ollama — the slice-498 CI seam). The stub is per-section-aware so we
// can craft a VALID draft for each section, exercise the four-gate pipeline per
// section, prove per-section approval, prove the board pack ships only approved
// sections, and prove cross-tenant isolation across ALL sections.
//
// Run with:
//
//	go test -tags=integration -p 1 ./internal/boardnarrative/...
//
// Coverage (AC-9 / AC-12 / AC-13):
//
//	AC-9   a multi-section narrative generates; each section passes all gates
//	       and reaches per-section DRAFT state.
//	AC-12  a Tenant-B narrative cannot cite a Tenant-A control in ANY section.
//	AC-13  only APPROVED sections ship; an unapproved section is excluded.
package boardnarrative_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/boardnarrative"
	"github.com/mgoodric/security-atlas/internal/dbtest"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- per-section harness -----

// perSectionRollups returns the right rollup per section key (each section
// grounds on a different slice of the same Brief). The citable excerpt is the
// given control id, so every section's draft can cite it.
type perSectionRollups struct {
	rollups map[boardnarrative.SectionKey]boardnarrative.Rollup
}

func (p perSectionRollups) CoverageRollup(_ context.Context, _ string) (boardnarrative.Rollup, error) {
	return p.rollups[boardnarrative.SectionControlCoverage], nil
}

func (p perSectionRollups) SectionRollup(_ context.Context, section boardnarrative.SectionKey, _ string) (boardnarrative.Rollup, error) {
	r, ok := p.rollups[section]
	if !ok {
		return boardnarrative.Rollup{}, boardnarrative.ErrUnknownSection
	}
	return r, nil
}

// perSectionStub is a llm.Client that returns a different draft per section,
// keyed off the system prompt's section heading (the stub sees the assembled
// prompt, which contains the section's exact heading).
type perSectionStub struct {
	drafts map[string]string // heading -> draft
}

func (s perSectionStub) Generate(_ context.Context, req llm.GenerateRequest) (llm.GenerateResult, error) {
	if err := req.Validate(); err != nil {
		return llm.GenerateResult{}, err
	}
	for heading, draft := range s.drafts {
		if strings.Contains(req.SystemPrompt, heading) {
			return llm.GenerateResult{Text: draft, ModelName: "llama3.1", ModelVersion: "8b-instruct-q5", ModelProvider: "ollama-local"}, nil
		}
	}
	return llm.GenerateResult{Text: "## Unknown\n1. nothing", ModelName: "llama3.1", ModelVersion: "8b-instruct-q5", ModelProvider: "ollama-local"}, nil
}

// buildMultiSectionRollups builds a fixed rollup for each AI-drafted section,
// grounding every section's citable excerpt on ctrlID.
func buildMultiSectionRollups(periodEnd, ctrlID string) map[boardnarrative.SectionKey]boardnarrative.Rollup {
	brief := board.Brief{
		PeriodEnd:  periodEnd,
		Frameworks: []board.FrameworkPosture{{Slug: "soc2", Name: "SOC 2", CoveragePct: 84, FreshnessPct: 91, Delta: -3}},
		Drift:      board.DriftSummary{WindowDays: 30, Delta: -3, FlippedOutCount: 3},
		TopRisks: []board.RiskAging{
			{ID: uuid.NewString(), Title: "Stale access reviews", Category: "access", Treatment: "mitigate", ResidualSeverity: 12.0, AgeDays: 47},
			{ID: uuid.NewString(), Title: "Unpatched host", Category: "vuln", Treatment: "mitigate", ResidualSeverity: 8.0, AgeDays: 20},
		},
	}
	excerpts := []boardnarrative.Excerpt{{ID: ctrlID, Kind: boardnarrative.KindControl, Title: "Access reviews", Excerpt: "quarterly"}}
	cov, _ := boardnarrative.RollupForSection(brief, excerpts, boardnarrative.SectionControlCoverage)
	risk, _ := boardnarrative.RollupForSection(brief, excerpts, boardnarrative.SectionRiskPosture)
	drift, _ := boardnarrative.RollupForSection(brief, excerpts, boardnarrative.SectionDriftActivity)
	return map[boardnarrative.SectionKey]boardnarrative.Rollup{
		boardnarrative.SectionControlCoverage: cov,
		boardnarrative.SectionRiskPosture:     risk,
		boardnarrative.SectionDriftActivity:   drift,
	}
}

// validDrafts builds a valid draft per section (correct numbers, right shape,
// cites the real control id, clean tone), keyed by section heading.
func validDrafts(ctrlID string) map[string]string {
	return map[string]string{
		"## Control coverage summary": strings.Join([]string{
			"## Control coverage summary",
			"1. Program control coverage stands at 84% for the period.",
			"2. Evidence freshness within the 30-day window is 91%.",
			"3. Over the last 30 days the net drift was -3; 3 controls drifted out of passing.",
			"4. The program runs against 1 framework; coverage is grounded in control (" + ctrlID + ").",
		}, "\n"),
		"## Risk posture summary": strings.Join([]string{
			"## Risk posture summary",
			"1. There are 2 open risks aging in the register.",
			"2. The worst residual severity is 12; the oldest risk has been open 47 days.",
			"3. Posture is grounded in the access-review control (" + ctrlID + ").",
		}, "\n"),
		"## Control drift activity": strings.Join([]string{
			"## Control drift activity",
			"1. Over the 30-day window the net control drift was -3.",
			"2. In the window 3 controls drifted out of passing.",
			"3. The posture is grounded in the access-review control (" + ctrlID + ").",
		}, "\n"),
	}
}

func newMultiService(t *testing.T, appPool *pgxpool.Pool, rollups map[boardnarrative.SectionKey]boardnarrative.Rollup, drafts map[string]string) (*boardnarrative.Service, *boardnarrative.Store) {
	t.Helper()
	store := boardnarrative.NewStore(appPool, nil)
	stub := perSectionStub{drafts: drafts}
	audit := boardnarrative.NewAuditSink(llm.NewAuditWriter(appPool))
	svc := boardnarrative.NewService(perSectionRollups{rollups: rollups}, stub, store, audit, store)
	return svc, store
}

// ----- AC-9: multi-section narrative all reach draft state -----

func TestIntegration_MultiSection_AllDraft(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollups := buildMultiSectionRollups("2026-05-31", ctrlID)
	svc, store := newMultiService(t, app, rollups, validDrafts(ctrlID))

	results, err := svc.GenerateAll(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	if len(results) != len(boardnarrative.AIDraftedSections) {
		t.Fatalf("want %d sections, got %d", len(boardnarrative.AIDraftedSections), len(results))
	}
	for _, res := range results {
		if res.Suppressed {
			t.Fatalf("section %q suppressed: %q", res.Section, res.Reason)
		}
		if res.RecordID == "" {
			t.Fatalf("section %q has no record id", res.Section)
		}
		rec, err := store.GetSection(ctx, uuid.MustParse(res.RecordID))
		if err != nil {
			t.Fatalf("GetSection(%q): %v", res.Section, err)
		}
		if !rec.AiAssisted || rec.HumanApproved || rec.HumanApprover != nil {
			t.Fatalf("section %q must be ai_assisted + unapproved: %+v", res.Section, rec)
		}
		if rec.ModelProvider != "ollama-local" || rec.PromptVersion == "" {
			t.Fatalf("section %q provenance not persisted: %+v", res.Section, rec)
		}
	}
}

// TestIntegration_MultiSection_FabricatedNumberInOneSection proves a fabricated
// number in ONE section suppresses ONLY that section; the others still draft
// (AC-4 — every section consumes the library, independently).
func TestIntegration_MultiSection_FabricatedNumberInOneSection(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollups := buildMultiSectionRollups("2026-05-31", ctrlID)
	drafts := validDrafts(ctrlID)
	// Fabricate a severity in the risk section (99 not in the rollup).
	drafts["## Risk posture summary"] = strings.Replace(drafts["## Risk posture summary"], "severity is 12", "severity is 99", 1)
	svc, _ := newMultiService(t, app, rollups, drafts)

	results, err := svc.GenerateAll(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}
	var riskSuppressed, others int
	for _, res := range results {
		if res.Section == boardnarrative.SectionRiskPosture {
			if !res.Suppressed || res.Reason != boardnarrative.ReasonNumericMismatch {
				t.Fatalf("risk section with fabricated number must be suppressed/numeric_mismatch, got suppressed=%v reason=%q", res.Suppressed, res.Reason)
			}
			riskSuppressed++
			continue
		}
		if res.Suppressed {
			t.Fatalf("section %q must NOT be suppressed (only risk had a bad number): %q", res.Section, res.Reason)
		}
		others++
	}
	if riskSuppressed != 1 || others != len(boardnarrative.AIDraftedSections)-1 {
		t.Fatalf("expected exactly the risk section suppressed: riskSuppressed=%d others=%d", riskSuppressed, others)
	}
}

// ----- AC-12: cross-tenant isolation across ALL sections (P0-501-5) -----

func TestIntegration_MultiSection_CrossTenantIsolation(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenantA := freshTenant(t, admin)
	tenantB := freshTenant(t, admin)

	ctrlA, _ := seedControlWithEvidence(t, admin, tenantA, "A-only control")
	ctrlB, _ := seedControlWithEvidence(t, admin, tenantB, "B control")

	ctxB := tenantCtx(t, tenantB)
	// B's rollups ground on B's control, but EVERY section's draft cites A's id
	// instead — the worst case: a model that leaked a cross-tenant id into each
	// section. Under B's RLS context A's id is invisible, so EVERY section's
	// citation resolution fails and EVERY section is suppressed.
	rollupsB := buildMultiSectionRollups("2026-05-31", ctrlB)
	drafts := validDrafts(ctrlB)
	for heading := range drafts {
		drafts[heading] = strings.Replace(drafts[heading], ctrlB, ctrlA, 1)
	}
	svc, store := newMultiService(t, app, rollupsB, drafts)

	results, err := svc.GenerateAll(ctxB, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "b-user"})
	if err != nil {
		t.Fatalf("GenerateAll(B): %v", err)
	}
	for _, res := range results {
		if !res.Suppressed || res.Reason != boardnarrative.ReasonUnresolvedCitation {
			t.Fatalf("section %q citing tenant A must be suppressed/unresolved, got suppressed=%v reason=%q", res.Section, res.Suppressed, res.Reason)
		}
	}
	// No section persisted for tenant B (every one suppressed — P0-501-2).
	assertNoSections(t, admin, tenantB)

	// Independently prove the resolver: B cannot resolve A's id.
	if _, ok, _ := store.Resolve(ctxB, uuid.MustParse(ctrlA)); ok {
		t.Fatalf("Tenant B resolved Tenant A's control id — RLS isolation breached")
	}
}

// ----- AC-13: board pack ships only APPROVED sections -----

// TestIntegration_OnlyApprovedSectionsShip proves the assembled narrative ships
// only sections an operator approved; an unapproved section is excluded.
func TestIntegration_OnlyApprovedSectionsShip(t *testing.T) {
	app := dbtest.NewAppPool(t)
	admin := dbtest.NewMigratePool(t)
	tenant := freshTenant(t, admin)
	ctx := tenantCtx(t, tenant)

	ctrlID, _ := seedControlWithEvidence(t, admin, tenant, "Access reviews")
	rollups := buildMultiSectionRollups("2026-05-31", ctrlID)
	svc, store := newMultiService(t, app, rollups, validDrafts(ctrlID))

	results, err := svc.GenerateAll(ctx, boardnarrative.GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
	if err != nil {
		t.Fatalf("GenerateAll: %v", err)
	}

	// Approve ONLY the coverage + drift sections; leave risk unapproved.
	var approvedKeys []boardnarrative.SectionKey
	for _, res := range results {
		if res.Section == boardnarrative.SectionRiskPosture {
			continue // leave unapproved
		}
		if _, err := svc.Approve(ctx, boardnarrative.ApproveParams{
			RecordID:  uuid.MustParse(res.RecordID),
			FinalText: "Approved: " + string(res.Section),
			Approver:  "alice",
		}); err != nil {
			t.Fatalf("Approve(%q): %v", res.Section, err)
		}
		approvedKeys = append(approvedKeys, res.Section)
	}

	// The narrative ships ONLY approved sections.
	shipped, err := store.ApprovedNarrative(ctx, "2026-05-31")
	if err != nil {
		t.Fatalf("ApprovedNarrative: %v", err)
	}
	if len(shipped) != len(approvedKeys) {
		t.Fatalf("ships %d sections, want %d approved (risk must be excluded)", len(shipped), len(approvedKeys))
	}
	for _, sec := range shipped {
		if sec.Section == boardnarrative.SectionRiskPosture {
			t.Fatalf("unapproved risk section shipped (AC-13 violated)")
		}
		if !sec.HumanApproved || sec.HumanApprover != "alice" {
			t.Fatalf("shipped section %q not properly approved: %+v", sec.Section, sec)
		}
		if !strings.HasPrefix(sec.FinalText, "Approved: ") {
			t.Fatalf("shipped section %q text is not the approved final text: %q", sec.Section, sec.FinalText)
		}
	}
}
