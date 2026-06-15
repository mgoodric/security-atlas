// Slice 440 — pure-Go unit tests for the board-narrative AI v0 guardrails.
// No Postgres, no live Ollama: the four pre-operator gates (citation parsing,
// numeric-claim verification, section-shape enforcement, tone) are pure
// functions, and the Service suppression branches run against a fake rollup
// source + the llm.StubClient (the slice-498 CI seam). The integration tier
// (integration_test.go) proves the RLS + DB-guard + cross-tenant behavior.
package boardnarrative

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- ground-truth fixture -----

// fixtureRollup is a deterministic coverage rollup with known numbers, used as
// the ground truth across the numeric-verification + service tests.
//
//	coverage 84, freshness 91, delta -3 (so |delta|=3), flipped-out 3,
//	window 30, framework_count 2.
func fixtureRollup(t *testing.T) (Rollup, string, string) {
	t.Helper()
	ctrlID := uuid.NewString()
	evID := uuid.NewString()
	b := board.Brief{
		PeriodEnd: "2026-05-31",
		Frameworks: []board.FrameworkPosture{
			{Slug: "soc2", Name: "SOC 2", CoveragePct: 84, FreshnessPct: 91, Delta: -3},
			{Slug: "iso27001", Name: "ISO 27001", CoveragePct: 84, FreshnessPct: 91, Delta: -3},
		},
		Drift: board.DriftSummary{WindowDays: 30, Delta: -3, FlippedOutCount: 3},
	}
	excerpts := []Excerpt{
		{ID: ctrlID, Kind: KindControl, Title: "Access reviews", Excerpt: "quarterly access review"},
		{ID: evID, Kind: KindEvidence, Title: "MFA enforced", Excerpt: "okta mfa report"},
	}
	r, err := RollupFromBrief(b, excerpts)
	if err != nil {
		t.Fatalf("RollupFromBrief: %v", err)
	}
	return r, ctrlID, evID
}

// validDraft is a well-shaped, correctly-numbered, cited draft for the fixture.
func validDraft(ctrlID, evID string) string {
	return strings.Join([]string{
		"## Control coverage summary",
		"1. Program control coverage stands at 84% for the period.",
		"2. Evidence freshness within the 30-day window is 91%.",
		"3. Over the last 30 days the net drift was -3; 3 controls drifted out of passing.",
		"4. The program runs against 2 frameworks; coverage is grounded in controls (" + ctrlID + ") and evidence (" + evID + ").",
		"",
	}, "\n")
}

// ===== guardrail 5: numeric-claim verification (THE defining guardrail) =====

func TestVerifyNumbers(t *testing.T) {
	t.Parallel()
	r, ctrlID, evID := fixtureRollup(t)

	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "all-numbers-match-ground-truth",
			text: validDraft(ctrlID, evID),
			want: true,
		},
		{
			name: "fabricated-coverage-number-rejects",
			// 85 is NOT in the rollup (coverage is 84) — the classic
			// hallucinated statistic. MUST reject.
			text: "## Control coverage summary\n1. Coverage is 85%.\n2. Freshness 91%.\n3. Drift 3 over 30 days.\n4. 2 frameworks (" + ctrlID + ").",
			want: false,
		},
		{
			name: "fabricated-freshness-number-rejects",
			text: "1. 84% 2. 92% 3. 30 3 4. 2 " + ctrlID,
			want: false, // 92 != freshness 91
		},
		{
			name: "delta-magnitude-allowed",
			// "3 controls drifted out" — magnitude of delta is allowed.
			text: "84 91 3 30 2 " + ctrlID,
			want: true,
		},
		{
			name: "signed-delta-literal-allowed",
			text: "84 91 -3 30 2 " + ctrlID,
			want: true,
		},
		{
			name: "period-end-date-not-treated-as-statistic",
			// The period-end label digits (2026-05-31) must NOT be read as
			// fabricated statistics.
			text: "As of 2026-05-31: coverage 84%, freshness 91%, 2 frameworks.",
			want: true,
		},
		{
			name: "invented-date-rejects",
			// A date the model invented (not the period-end) — its digits are
			// not stripped, so they fail (the model must not invent dates).
			text: "As of 2027-01-01: coverage 84%.",
			want: false,
		},
		{
			name: "decimal-precision-rejects",
			// "84.5" -> 84 then 5; 5 is not a ground-truth value. The model
			// inventing precision the rollup does not have is a fabrication.
			text: "84 91 3 30 2 then 84.5 " + ctrlID,
			want: false,
		},
		{
			name: "zero-not-in-rollup-rejects",
			text: "Coverage 84, freshness 91, but 0 incidents.",
			want: false, // 0 is not a ground-truth value here
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := verifyNumbers(tc.text, r); got != tc.want {
				t.Fatalf("verifyNumbers(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

func TestExtractNumbers(t *testing.T) {
	t.Parallel()
	got := extractNumbers("coverage 84%, freshness 91%, delta -3, 0 left")
	want := []int{84, 91, -3, 0}
	if len(got) != len(want) {
		t.Fatalf("extractNumbers len = %d (%v), want %d (%v)", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("extractNumbers[%d] = %d, want %d", i, got[i], want[i])
		}
	}
}

func TestAllowedNumbers(t *testing.T) {
	t.Parallel()
	r, _, _ := fixtureRollup(t)
	allow := r.AllowedNumbers()
	for _, n := range []int{84, 91, 3, -3, 30, 2} {
		if !allow[n] {
			t.Errorf("AllowedNumbers missing %d", n)
		}
	}
	for _, n := range []int{85, 90, 1, 100} {
		if allow[n] {
			t.Errorf("AllowedNumbers should not contain %d", n)
		}
	}
}

// ===== guardrail 6: section-shape enforcement =====

func TestEnforceShape(t *testing.T) {
	t.Parallel()
	id := uuid.NewString()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"well-formed-four-items", validDraft(id, id), true},
		{
			name: "missing-heading-rejects",
			text: "1. a\n2. b\n3. c\n4. d",
			want: false,
		},
		{
			name: "five-items-rejects",
			text: "## Control coverage summary\n1. a\n2. b\n3. c\n4. d\n5. e",
			want: false,
		},
		{
			name: "three-items-rejects",
			text: "## Control coverage summary\n1. a\n2. b\n3. c",
			want: false,
		},
		{
			name: "out-of-order-rejects",
			text: "## Control coverage summary\n1. a\n3. b\n2. c\n4. d",
			want: false,
		},
		{
			name: "freestyle-prose-rejects",
			text: "This quarter our security posture improved across the board.",
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := enforceShape(tc.text); got != tc.want {
				t.Fatalf("enforceShape = %v, want %v", got, tc.want)
			}
		})
	}
}

// ===== guardrail 7: tone / banned-phrase enforcement =====

func TestContainsBannedPhrase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"clean-measured-text", "Coverage is 84%; two findings opened.", false},
		{"proud-to-report-rejects", "We are proud to report 84% coverage.", true},
		{"case-insensitive", "WORLD-CLASS controls everywhere.", true},
		{"best-in-class-rejects", "Our best-in-class program delivered.", true},
		{"unprompted-superlative-rejects", "An unprecedented quarter of progress.", true},
		{"seamlessly-rejects", "The connector seamlessly integrated.", true},
		{"apostrophe-variant-rejects", "We're proud to report strong numbers.", true},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsBannedPhrase(tc.text); got != tc.want {
				t.Fatalf("containsBannedPhrase(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// TestSystemPromptWiresBanList proves guardrail 7's prompt-side wiring: the
// banned-phrase list is embedded in the system prompt (the grep the
// orchestrator runs). A representative sample of phrases must appear.
func TestSystemPromptWiresBanList(t *testing.T) {
	t.Parallel()
	sp := buildSystemPrompt()
	for _, phrase := range []string{
		"we are proud to report",
		"industry-leading",
		"best-in-class",
		"world-class",
	} {
		if !strings.Contains(sp, phrase) {
			t.Errorf("system prompt missing banned phrase %q", phrase)
		}
	}
	// The grounding + numeric + shape instructions must be present too.
	for _, must := range []string{"## Control coverage summary", "canonical UUID", "ONLY numbers that appear"} {
		if !strings.Contains(sp, must) {
			t.Errorf("system prompt missing instruction %q", must)
		}
	}
}

// ===== guardrail 4: citation parsing =====

func TestParseCitedIDs(t *testing.T) {
	t.Parallel()
	a := uuid.NewString()
	b := uuid.NewString()
	text := "grounded in (" + a + ") and (" + b + ") and again (" + a + ")"
	got := parseCitedIDs(text)
	if len(got) != 2 {
		t.Fatalf("parseCitedIDs len = %d, want 2 (distinct)", len(got))
	}
	if got[0].String() != a || got[1].String() != b {
		t.Fatalf("parseCitedIDs order/content wrong: %v", got)
	}
}

func TestValidateCitations(t *testing.T) {
	t.Parallel()
	r, ctrlID, evID := fixtureRollup(t)
	allowed := r.allowedExcerptIDs()
	res := fakeResolver{owned: map[string]CitationKind{ctrlID: KindControl, evID: KindEvidence}}

	t.Run("all-resolve", func(t *testing.T) {
		t.Parallel()
		cites, ok, reason, err := validateCitations(context.Background(), res, "see ("+ctrlID+") ("+evID+")", allowed)
		if err != nil || !ok {
			t.Fatalf("validateCitations ok=%v reason=%q err=%v", ok, reason, err)
		}
		if len(cites) != 2 {
			t.Fatalf("want 2 citations, got %d", len(cites))
		}
	})

	t.Run("no-citations-rejects", func(t *testing.T) {
		t.Parallel()
		_, ok, reason, _ := validateCitations(context.Background(), res, "no ids here", allowed)
		if ok || reason != ReasonNoCitations {
			t.Fatalf("want suppressed/no_citations, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("ungrounded-id-rejects", func(t *testing.T) {
		t.Parallel()
		// A real UUID that is NOT in the grounding set — the model invented it.
		invented := uuid.NewString()
		_, ok, reason, _ := validateCitations(context.Background(), res, "see ("+invented+")", allowed)
		if ok || reason != ReasonUnresolvedCitation {
			t.Fatalf("want suppressed/unresolved, got ok=%v reason=%q", ok, reason)
		}
	})

	t.Run("grounded-but-not-tenant-owned-rejects", func(t *testing.T) {
		t.Parallel()
		// In-grounding but the resolver says it does not resolve (the
		// cross-tenant analogue on the unit surface).
		res2 := fakeResolver{owned: map[string]CitationKind{}}
		_, ok, reason, _ := validateCitations(context.Background(), res2, "see ("+ctrlID+")", allowed)
		if ok || reason != ReasonUnresolvedCitation {
			t.Fatalf("want suppressed/unresolved, got ok=%v reason=%q", ok, reason)
		}
	})
}

// ===== Service suppression branches (the gate ordering, end to end) =====

func TestService_Generate_Suppression(t *testing.T) {
	t.Parallel()
	r, ctrlID, evID := fixtureRollup(t)
	owned := map[string]CitationKind{ctrlID: KindControl, evID: KindEvidence}

	newSvc := func(draft string, genErr error) (*Service, *recordingStore) {
		store := &recordingStore{}
		stub := llm.NewStubClient()
		stub.Result = llm.GenerateResult{Text: draft, ModelName: "llama3.1", ModelVersion: "8b", ModelProvider: "ollama-local"}
		stub.Err = genErr
		svc := NewService(
			fakeRollups{r: r},
			stub,
			fakeResolver{owned: owned},
			nil, // audit sink optional on the unit surface
			store,
		)
		return svc, store
	}

	t.Run("valid-draft-persists", func(t *testing.T) {
		t.Parallel()
		svc, store := newSvc(validDraft(ctrlID, evID), nil)
		out, err := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31", AuthoredBy: "u1"})
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if out.Suppressed {
			t.Fatalf("valid draft suppressed: %q", out.Reason)
		}
		if out.RecordID == "" || store.persisted != 1 {
			t.Fatalf("valid draft not persisted (id=%q persisted=%d)", out.RecordID, store.persisted)
		}
		if len(out.Citations) != 2 {
			t.Fatalf("want 2 citations, got %d", len(out.Citations))
		}
		if out.CloudRouted {
			t.Fatalf("local provider must not set CloudRouted")
		}
	})

	t.Run("bad-shape-suppressed-nothing-persisted", func(t *testing.T) {
		t.Parallel()
		svc, store := newSvc("freestyle prose with 84 and ("+ctrlID+")", nil)
		out, _ := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31"})
		assertSuppressed(t, out, store, ReasonBadShape)
	})

	t.Run("banned-phrase-suppressed", func(t *testing.T) {
		t.Parallel()
		// Well-shaped + correctly-numbered but contains a banned phrase.
		bad := strings.Replace(validDraft(ctrlID, evID),
			"Program control coverage stands at 84%",
			"We are proud to report coverage at 84%", 1)
		svc, store := newSvc(bad, nil)
		out, _ := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31"})
		assertSuppressed(t, out, store, ReasonBannedPhrase)
	})

	t.Run("numeric-mismatch-suppressed", func(t *testing.T) {
		t.Parallel()
		bad := strings.Replace(validDraft(ctrlID, evID), "stands at 84%", "stands at 85%", 1)
		svc, store := newSvc(bad, nil)
		out, _ := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31"})
		assertSuppressed(t, out, store, ReasonNumericMismatch)
	})

	t.Run("ungrounded-citation-suppressed", func(t *testing.T) {
		t.Parallel()
		invented := uuid.NewString()
		bad := strings.Replace(validDraft(ctrlID, evID), evID, invented, 1)
		svc, store := newSvc(bad, nil)
		out, _ := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31"})
		assertSuppressed(t, out, store, ReasonUnresolvedCitation)
	})

	t.Run("backend-unavailable-suppressed", func(t *testing.T) {
		t.Parallel()
		svc, store := newSvc(validDraft(ctrlID, evID), llm.ErrBackend)
		out, _ := svc.Generate(context.Background(), GenerateParams{PeriodEnd: "2026-05-31"})
		assertSuppressed(t, out, store, ReasonGenerationUnavailable)
	})
}

func TestService_Approve_BlankApproverRejected(t *testing.T) {
	t.Parallel()
	svc := NewService(nil, nil, nil, nil, &recordingStore{})
	_, err := svc.Approve(context.Background(), ApproveParams{RecordID: uuid.New(), FinalText: "x", Approver: "   "})
	if err != ErrApproverRequired {
		t.Fatalf("blank approver: want ErrApproverRequired, got %v", err)
	}
}

func TestService_Approve_RecordsApprover(t *testing.T) {
	t.Parallel()
	store := &recordingStore{}
	svc := NewService(nil, nil, nil, nil, store)
	rid := uuid.New()
	out, err := svc.Approve(context.Background(), ApproveParams{RecordID: rid, FinalText: "final", Approver: "alice"})
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !out.HumanApproved || out.HumanApprover != "alice" {
		t.Fatalf("approval state wrong: %+v", out)
	}
	if store.approved != 1 {
		t.Fatalf("approve not called on store")
	}
}

func assertSuppressed(t *testing.T, out SectionResult, store *recordingStore, wantReason string) {
	t.Helper()
	if !out.Suppressed {
		t.Fatalf("expected suppressed, got drafted: %q", out.Draft)
	}
	if out.Reason != wantReason {
		t.Fatalf("reason = %q, want %q", out.Reason, wantReason)
	}
	if out.RecordID != "" || store.persisted != 0 {
		t.Fatalf("suppressed draft must persist nothing (id=%q persisted=%d)", out.RecordID, store.persisted)
	}
}

// ----- fakes -----

type fakeRollups struct {
	r   Rollup
	err error
}

func (f fakeRollups) CoverageRollup(_ context.Context, _ string) (Rollup, error) {
	return f.r, f.err
}

type fakeResolver struct {
	owned map[string]CitationKind
}

func (f fakeResolver) Resolve(_ context.Context, id uuid.UUID) (Citation, bool, error) {
	if k, ok := f.owned[id.String()]; ok {
		return Citation{Kind: k, ID: id.String()}, true, nil
	}
	return Citation{}, false, nil
}

type recordingStore struct {
	persisted int
	approved  int
}

func (s *recordingStore) PersistDraft(_ context.Context, _ SectionKey, _, _ string, _ []byte, _ Provenance) (string, error) {
	s.persisted++
	return uuid.NewString(), nil
}

func (s *recordingStore) Approve(_ context.Context, recordID uuid.UUID, finalText, approver string) (ApprovedSection, error) {
	s.approved++
	return ApprovedSection{
		RecordID:      recordID.String(),
		Section:       SectionControlCoverage,
		FinalText:     finalText,
		HumanApproved: true,
		HumanApprover: approver,
	}, nil
}
