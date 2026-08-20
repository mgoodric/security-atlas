// Slice 751 — pure-Go unit branches for the exception-status section (slice 353
// pure-Go pre-DB convention). No Postgres, no Ollama: the section's rollup
// projection, its permitted-number set, and its passage through all four
// pre-operator gates are deterministic functions plus a Service wired to
// in-memory seams.
//
// The load-bearing test in this file is
// TestExceptionSection_FabricatedNumberAutoRejected: it proves that a draft
// stating an exception number the deterministic aggregate did not produce is
// suppressed BEFORE the operator sees it and persists nothing. That is the
// whole reason this section could not ship in slice 501 — with no aggregate,
// every number in it would have been unverifiable, and guardrail 5 would have
// had nothing to check against.

package boardnarrative

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/mgoodric/security-atlas/internal/board"
	"github.com/mgoodric/security-atlas/internal/llm"
)

// ----- fixtures -----

const (
	testPeriodEnd = "2026-05-31"
	// The ground-truth aggregate every test in this file grounds on.
	wantActive   = 4
	wantPastDue  = 1
	wantOldestAg = 210
)

// exceptionBrief builds a Brief whose exceptions aggregate is the fixture above.
func exceptionBrief() board.Brief {
	return board.Brief{
		PeriodEnd:  testPeriodEnd,
		Frameworks: []board.FrameworkPosture{{Slug: "soc2", Name: "SOC 2", CoveragePct: 84, FreshnessPct: 91}},
		Drift:      board.DriftSummary{WindowDays: 30, Delta: -3, FlippedOutCount: 3},
		Exceptions: board.ExceptionSummary{
			ActiveCount:         wantActive,
			PastDueCount:        wantPastDue,
			OldestActiveAgeDays: wantOldestAg,
		},
	}
}

// validExceptionDraft is a draft that passes every gate: correct heading,
// exactly three numbered items, only ground-truth numbers, a cited id from the
// grounding set, measured tone.
func validExceptionDraft(ctrlID string) string {
	return strings.Join([]string{
		exceptionHeading,
		"1. As of 2026-05-31 the program carries 4 exceptions in force.",
		"2. Of those, 1 is past its expiry date; the longest-standing exception has been in force 210 days.",
		"3. The compensating control the program relies on is recorded as control (" + ctrlID + ").",
	}, "\n")
}

// ----- rollup projection -----

func TestExceptionRollupFromBrief(t *testing.T) {
	t.Parallel()
	excerpts := []Excerpt{{ID: uuid.NewString(), Kind: KindControl, Title: "Access reviews"}}

	r, err := exceptionRollupFromBrief(exceptionBrief(), excerpts)
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	if r.PeriodEnd != testPeriodEnd {
		t.Errorf("PeriodEnd = %q, want %q", r.PeriodEnd, testPeriodEnd)
	}
	if r.ExceptionsActive != wantActive || r.ExceptionsPastDue != wantPastDue || r.OldestExceptionAgeDays != wantOldestAg {
		t.Errorf("rollup = (%d, %d, %d), want (%d, %d, %d)",
			r.ExceptionsActive, r.ExceptionsPastDue, r.OldestExceptionAgeDays,
			wantActive, wantPastDue, wantOldestAg)
	}
	if !r.exceptionOnly {
		t.Error("exception rollup must set the exceptionOnly discriminator")
	}
	if len(r.Excerpts) != 1 {
		t.Errorf("Excerpts = %d, want 1", len(r.Excerpts))
	}
}

// An empty exception register is a valid posture reported as zero — NOT an
// error. Only a Brief with no framework posture is "nothing to summarize".
func TestExceptionRollupFromBrief_EmptyRegisterIsZeroNotError(t *testing.T) {
	t.Parallel()
	b := exceptionBrief()
	b.Exceptions = board.ExceptionSummary{}

	r, err := exceptionRollupFromBrief(b, nil)
	if err != nil {
		t.Fatalf("empty exception register must not error: %v", err)
	}
	if r.ExceptionsActive != 0 || r.ExceptionsPastDue != 0 || r.OldestExceptionAgeDays != 0 {
		t.Errorf("empty register rollup = %+v, want all zero", r)
	}
}

func TestExceptionRollupFromBrief_NoBriefData(t *testing.T) {
	t.Parallel()
	b := exceptionBrief()
	b.Frameworks = nil

	if _, err := exceptionRollupFromBrief(b, nil); err != ErrNoBriefData {
		t.Errorf("err = %v, want ErrNoBriefData", err)
	}
}

// The excerpt set feeding the prompt is bounded like every other section's
// (guardrail 1's "bounded cited material").
func TestExceptionRollupFromBrief_BoundsExcerpts(t *testing.T) {
	t.Parallel()
	excerpts := make([]Excerpt, maxCitedExcerpts+5)
	for i := range excerpts {
		excerpts[i] = Excerpt{ID: uuid.NewString(), Kind: KindControl}
	}
	r, err := exceptionRollupFromBrief(exceptionBrief(), excerpts)
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	if len(r.Excerpts) != maxCitedExcerpts {
		t.Errorf("Excerpts = %d, want the bound %d", len(r.Excerpts), maxCitedExcerpts)
	}
}

// ----- the permitted-number set (guardrail 5's ground truth) -----

// The exception section's allowed set is EXACTLY the three aggregate integers.
// Nothing else carried on the Rollup struct (coverage %, drift delta, framework
// count) may validate a number in an exception draft.
func TestExceptionRollup_AllowedNumbersAreOnlyTheAggregate(t *testing.T) {
	t.Parallel()
	r, err := exceptionRollupFromBrief(exceptionBrief(), nil)
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	// Contaminate the coverage/drift fields — an exception draft must still not
	// be able to state them.
	r.CoveragePct = 84
	r.FreshnessPct = 91
	r.FrameworkCount = 7

	allowed := r.AllowedNumbers()
	for _, n := range []int{wantActive, wantPastDue, wantOldestAg} {
		if !allowed[n] {
			t.Errorf("ground-truth value %d is not allowed", n)
		}
	}
	for _, n := range []int{84, 91, 7, 3, 30, 99} {
		if allowed[n] {
			t.Errorf("value %d is NOT in the exceptions aggregate but was allowed", n)
		}
	}
}

// ----- section registration (all four gates run because the pipeline is shared) -----

func TestExceptionSection_IsRegistered(t *testing.T) {
	t.Parallel()
	def, ok := sectionDef(SectionExceptionStatus)
	if !ok {
		t.Fatal("exception_status_summary has no SectionDef — the Service cannot draft it")
	}
	if def.Heading != exceptionHeading || def.ExpectedItems != exceptionExpectedItems {
		t.Errorf("SectionDef shape = (%q, %d), want (%q, %d)", def.Heading, def.ExpectedItems, exceptionHeading, exceptionExpectedItems)
	}
	if def.PromptVersion == "" {
		t.Error("SectionDef must carry a prompt version (slice-182 audit contract)")
	}
	var found bool
	for _, k := range AIDraftedSections {
		if k == SectionExceptionStatus {
			found = true
		}
	}
	if !found {
		t.Error("exception_status_summary is not in AIDraftedSections — GenerateAll would skip it")
	}
}

// The banned-phrase list is embedded in this section's system prompt exactly as
// it is in every other section's (the anti-criterion: no section bypasses it).
func TestExceptionSystemPrompt_EmbedsBannedPhraseList(t *testing.T) {
	t.Parallel()
	prompt := buildExceptionSystemPrompt()
	if strings.Contains(prompt, bannedPhrasesPlaceholder) {
		t.Fatal("banned-phrase placeholder was not substituted")
	}
	if !strings.Contains(prompt, BannedPhraseListForPrompt()) {
		t.Fatal("exception section's system prompt does not embed the banned-phrase list")
	}
	if !strings.Contains(prompt, exceptionHeading) {
		t.Error("system prompt does not state the required section heading")
	}
}

// The forensic audit row records the exact aggregate values this section
// grounded on (guardrail 3 — a suppressed draft must be reconstructable).
func TestExceptionSection_ContextInputs(t *testing.T) {
	t.Parallel()
	r, err := exceptionRollupFromBrief(exceptionBrief(), []Excerpt{{ID: "id-1", Kind: KindControl}})
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	ctx := sectionContextInputs(SectionExceptionStatus, r)
	if ctx["section"] != string(SectionExceptionStatus) {
		t.Errorf("section = %v, want %q", ctx["section"], SectionExceptionStatus)
	}
	for key, want := range map[string]int{
		"exceptions_in_force":       wantActive,
		"exceptions_past_expiry":    wantPastDue,
		"oldest_exception_age_days": wantOldestAg,
	} {
		if ctx[key] != want {
			t.Errorf("context[%q] = %v, want %d", key, ctx[key], want)
		}
	}
}

// ----- guardrail 5 in isolation: the reusable numeric library (AC-3) -----

func TestExceptionSection_VerifyNumbers(t *testing.T) {
	t.Parallel()
	r, err := exceptionRollupFromBrief(exceptionBrief(), nil)
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	allowed := r.AllowedNumbers()

	cases := []struct {
		name string
		text string
		want bool
	}{
		{
			name: "every number is ground truth",
			text: "1. 4 exceptions are in force.\n2. 1 is past expiry; the oldest has stood 210 days.",
			want: true,
		},
		{
			name: "fabricated active count",
			text: "1. 12 exceptions are in force.\n2. 1 is past expiry; the oldest has stood 210 days.",
			want: false,
		},
		{
			name: "fabricated past-due count",
			text: "1. 4 exceptions are in force.\n2. 3 are past expiry; the oldest has stood 210 days.",
			want: false,
		},
		{
			name: "fabricated age",
			text: "1. 4 exceptions are in force.\n2. 1 is past expiry; the oldest has stood 365 days.",
			want: false,
		},
		{
			name: "derived ratio the aggregate never produced",
			text: "1. 4 exceptions are in force, 25% of the register.",
			want: false,
		},
		{
			name: "invented precision on the age",
			text: "1. 4 exceptions are in force.\n2. The oldest has stood 210.5 days.",
			want: false,
		},
		{
			name: "the period-end label is not a fabricated statistic",
			text: "1. As of " + testPeriodEnd + ", 4 exceptions are in force.",
			want: true,
		},
		{
			name: "an invented date IS a fabrication",
			text: "1. As of 2026-06-30, 4 exceptions are in force.",
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := VerifyNumbers(tc.text, allowed, testPeriodEnd); got != tc.want {
				t.Errorf("VerifyNumbers(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// ----- guardrail 6 in isolation: the section shape -----

func TestExceptionSection_EnforceShape(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	cases := []struct {
		name string
		text string
		want bool
	}{
		{"conforming draft", validExceptionDraft(ctrlID), true},
		{
			name: "missing heading is freestyle",
			text: strings.TrimPrefix(validExceptionDraft(ctrlID), exceptionHeading+"\n"),
			want: false,
		},
		{
			name: "an extra numbered item is rejected",
			text: validExceptionDraft(ctrlID) + "\n4. In summary, the program is in good shape.",
			want: false,
		},
		{
			name: "a dropped item is rejected",
			text: exceptionHeading + "\n1. There are 4 exceptions in force.\n2. 1 is past expiry.",
			want: false,
		},
		{
			name: "out-of-order items are rejected",
			text: exceptionHeading + "\n1. a\n3. b\n2. c",
			want: false,
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := enforceShapeFor(tc.text, exceptionHeading, exceptionExpectedItems); got != tc.want {
				t.Errorf("enforceShapeFor(%q) = %v, want %v", tc.text, got, tc.want)
			}
		})
	}
}

// ----- the full four-gate pipeline, in-memory -----

// exceptionAuditSpy records every generation the Service audits (guardrail 3 —
// the forensic row is written even when a gate suppresses the draft). The other
// seams (fakeRollups / fakeResolver / recordingStore / llm.StubClient) are the
// package's existing unit fakes, reused unchanged.
type exceptionAuditSpy struct{ writes []llm.Generation }

func (a *exceptionAuditSpy) Write(_ context.Context, g llm.Generation) error {
	a.writes = append(a.writes, g)
	return nil
}

// newExceptionService wires the exception section's four-gate pipeline over
// in-memory seams and returns the service plus the audit + store spies. The
// rollup is built by the REAL exceptionRollupFromBrief, so the ground truth the
// gates check against is the same projection production uses.
func newExceptionService(t *testing.T, draft string, ownedIDs ...string) (*Service, *exceptionAuditSpy, *recordingStore) {
	t.Helper()
	owned := make(map[string]CitationKind, len(ownedIDs))
	excerpts := make([]Excerpt, 0, len(ownedIDs))
	for _, id := range ownedIDs {
		owned[id] = KindControl
		excerpts = append(excerpts, Excerpt{ID: id, Kind: KindControl, Title: "Access reviews"})
	}
	r, err := exceptionRollupFromBrief(exceptionBrief(), excerpts)
	if err != nil {
		t.Fatalf("exceptionRollupFromBrief: %v", err)
	}
	stub := llm.NewStubClient()
	stub.Result = llm.GenerateResult{
		Text:          draft,
		ModelName:     "llama3.1",
		ModelVersion:  "8b-instruct-q5",
		ModelProvider: "ollama-local",
	}
	audit := &exceptionAuditSpy{}
	store := &recordingStore{}
	svc := NewService(fakeRollups{r: r}, stub, fakeResolver{owned: owned}, audit, store)
	return svc, audit, store
}

func generateException(t *testing.T, svc *Service) SectionResult {
	t.Helper()
	res, err := svc.GenerateSection(context.Background(), SectionExceptionStatus, GenerateParams{
		PeriodEnd:  testPeriodEnd,
		AuthoredBy: "operator-1",
	})
	if err != nil {
		t.Fatalf("GenerateSection: %v", err)
	}
	return res
}

// A well-formed, ground-truth draft passes all four gates and is persisted as an
// UNAPPROVED draft — never auto-approved.
func TestExceptionSection_ValidDraftPassesAllGates(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	svc, audit, store := newExceptionService(t, validExceptionDraft(ctrlID), ctrlID)

	res := generateException(t, svc)
	if res.Suppressed {
		t.Fatalf("valid draft was suppressed: %q", res.Reason)
	}
	if res.RecordID == "" || store.persisted != 1 {
		t.Fatalf("valid draft must persist exactly one record, got persisted=%d", store.persisted)
	}
	if len(res.Citations) != 1 || res.Citations[0].ID != ctrlID {
		t.Errorf("citations = %+v, want the one grounded control id", res.Citations)
	}
	if len(audit.writes) != 1 {
		t.Fatalf("audit writes = %d, want 1", len(audit.writes))
	}
	if audit.writes[0].PromptVersion != "boardnarrative-exception-v0" {
		t.Errorf("audit prompt version = %q, want the exception section's", audit.writes[0].PromptVersion)
	}
	if res.CloudRouted {
		t.Error("local Ollama generation must not set the cloud-routing banner")
	}
}

// THE test the slice exists for (AC-3 / issue Do-6): a draft stating an
// exception number the deterministic aggregate did not produce is AUTO-REJECTED
// before the operator sees it, and NOTHING is persisted.
func TestExceptionSection_FabricatedNumberAutoRejected(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	// The aggregate says 4 in force; the model claims 12.
	bad := strings.Replace(validExceptionDraft(ctrlID), "carries 4 exceptions", "carries 12 exceptions", 1)
	svc, audit, store := newExceptionService(t, bad, ctrlID)

	res := generateException(t, svc)
	if !res.Suppressed {
		t.Fatal("a draft with a number absent from the aggregate reached the operator")
	}
	if res.Reason != ReasonNumericMismatch {
		t.Errorf("reason = %q, want %q", res.Reason, ReasonNumericMismatch)
	}
	if res.Draft != "" || res.RecordID != "" {
		t.Errorf("suppressed result leaked draft text or a record id: %+v", res)
	}
	if store.persisted != 0 {
		t.Errorf("suppressed draft was persisted (%d rows) — it must never reach the operator", store.persisted)
	}
	// The forensic record IS still written: a suppressed draft is exactly what
	// an auditor wants reconstructable (guardrail 3).
	if len(audit.writes) != 1 {
		t.Errorf("audit writes = %d, want 1 even on suppression", len(audit.writes))
	}
}

// Every one of the three aggregate numbers is independently pinned — a
// fabrication in any of them rejects the whole draft.
func TestExceptionSection_EachAggregateNumberIsPinned(t *testing.T) {
	t.Parallel()
	cases := []struct{ name, from, to string }{
		{"active count", "carries 4 exceptions", "carries 9 exceptions"},
		{"past-due count", "1 is past its expiry date", "2 is past its expiry date"},
		{"oldest age", "in force 210 days", "in force 45 days"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrlID := uuid.NewString()
			bad := strings.Replace(validExceptionDraft(ctrlID), tc.from, tc.to, 1)
			if bad == validExceptionDraft(ctrlID) {
				t.Fatalf("fixture drift: %q not found in the valid draft", tc.from)
			}
			svc, _, store := newExceptionService(t, bad, ctrlID)
			res := generateException(t, svc)
			if !res.Suppressed || res.Reason != ReasonNumericMismatch {
				t.Errorf("fabricated %s: suppressed=%v reason=%q, want numeric_mismatch", tc.name, res.Suppressed, res.Reason)
			}
			if store.persisted != 0 {
				t.Errorf("fabricated %s was persisted", tc.name)
			}
		})
	}
}

// Guardrail 4 on this section: a cited id that is not in the grounding set (here,
// an id no tenant-visible row backs) rejects the draft.
func TestExceptionSection_UnresolvedCitationRejected(t *testing.T) {
	t.Parallel()
	ownedID := uuid.NewString()
	foreignID := uuid.NewString()
	// The rollup grounds on ownedID; the draft cites a different id entirely.
	svc, _, store := newExceptionService(t, validExceptionDraft(foreignID), ownedID)

	res := generateException(t, svc)
	if !res.Suppressed || res.Reason != ReasonUnresolvedCitation {
		t.Errorf("suppressed=%v reason=%q, want unresolved_citation", res.Suppressed, res.Reason)
	}
	if store.persisted != 0 {
		t.Error("a draft with an unresolved citation was persisted")
	}
}

// A draft that cites nothing at all is rejected — an exception claim with no
// grounding is not a cited claim.
func TestExceptionSection_NoCitationRejected(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	uncited := strings.Join([]string{
		exceptionHeading,
		"1. As of 2026-05-31 the program carries 4 exceptions in force.",
		"2. Of those, 1 is past its expiry date; the longest-standing exception has been in force 210 days.",
		"3. The program relies on a compensating control.",
	}, "\n")
	svc, _, store := newExceptionService(t, uncited, ctrlID)

	res := generateException(t, svc)
	if !res.Suppressed || res.Reason != ReasonNoCitations {
		t.Errorf("suppressed=%v reason=%q, want no_citations", res.Suppressed, res.Reason)
	}
	if store.persisted != 0 {
		t.Error("an uncited draft was persisted")
	}
}

// The banned-phrase list is enforced on this section like any other — no
// section-specific bypass (issue boundary: "Do NOT bypass the banned-phrase
// list for this section").
func TestExceptionSection_BannedPhraseRejected(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	marketing := strings.Replace(
		validExceptionDraft(ctrlID),
		"As of 2026-05-31 the program carries 4 exceptions in force.",
		"We are proud to report the program carries 4 exceptions in force.",
		1,
	)
	svc, _, store := newExceptionService(t, marketing, ctrlID)

	res := generateException(t, svc)
	if !res.Suppressed || res.Reason != ReasonBannedPhrase {
		t.Errorf("suppressed=%v reason=%q, want banned_phrase", res.Suppressed, res.Reason)
	}
	if store.persisted != 0 {
		t.Error("a draft with a banned phrase was persisted")
	}
}

// Freestyle output is rejected on this section too (guardrail 6).
func TestExceptionSection_BadShapeRejected(t *testing.T) {
	t.Parallel()
	ctrlID := uuid.NewString()
	freestyle := "The exception register looks fine this quarter. See control (" + ctrlID + ")."
	svc, _, store := newExceptionService(t, freestyle, ctrlID)

	res := generateException(t, svc)
	if !res.Suppressed || res.Reason != ReasonBadShape {
		t.Errorf("suppressed=%v reason=%q, want section_shape_violation", res.Suppressed, res.Reason)
	}
	if store.persisted != 0 {
		t.Error("a freestyle draft was persisted")
	}
}
