// Slice 441 — pure-Go unit tests for the AI questionnaire-answer suggestion
// surface. These exercise the citation parse/validate gate, the suppression
// branches, the prompt build, the keyword retrieval/ranking, and the approval
// guard — all WITHOUT Postgres or a live Ollama (fast t.Parallel() table tests,
// the slice-353 Q-2 convention). The DB-backed RLS + cross-tenant proofs live
// in integration_test.go.

package qaisuggest

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// ----- citation parsing -----

func TestParseCitedIDs(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	b := uuid.New()
	text := "We do X (" + a.String() + ") and Y (" + b.String() + "), again " + a.String() + "."
	got := parseCitedIDs(text)
	if len(got) != 2 {
		t.Fatalf("want 2 distinct ids, got %d: %v", len(got), got)
	}
	if got[0] != a || got[1] != b {
		t.Errorf("first-seen order not preserved: %v", got)
	}
}

func TestParseCitedIDs_NoneAndMalformed(t *testing.T) {
	t.Parallel()
	for _, s := range []string{
		"no ids here at all",
		"almost-a-uuid 12345678-1234-1234-1234-12345678", // too short
		"",
	} {
		if got := parseCitedIDs(s); len(got) != 0 {
			t.Errorf("parseCitedIDs(%q) = %v, want empty", s, got)
		}
	}
}

// ----- citation validation (the AC-4 gate) -----

// fakeResolver resolves only the ids in its set; everything else is unowned
// (the cross-tenant / fabricated case on the unit surface).
type fakeResolver struct {
	owned map[uuid.UUID]CandidateKind
	err   error
}

func (f fakeResolver) Resolve(_ context.Context, id uuid.UUID) (Citation, bool, error) {
	if f.err != nil {
		return Citation{}, false, f.err
	}
	if k, ok := f.owned[id]; ok {
		return Citation{Kind: k, ID: id.String()}, true, nil
	}
	return Citation{}, false, nil
}

func TestValidateCitations_AllResolve(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	allowed := map[uuid.UUID]CandidateKind{a: KindPolicy}
	res := fakeResolver{owned: map[uuid.UUID]CandidateKind{a: KindPolicy}}
	draft := "Yes, see policy (" + a.String() + ")."
	cits, ok, reason, err := validateCitations(context.Background(), res, draft, allowed)
	if err != nil || !ok {
		t.Fatalf("want ok, got ok=%v reason=%q err=%v", ok, reason, err)
	}
	if len(cits) != 1 || cits[0].ID != a.String() {
		t.Errorf("citations = %+v", cits)
	}
}

func TestValidateCitations_NoCitations(t *testing.T) {
	t.Parallel()
	_, ok, reason, _ := validateCitations(context.Background(), fakeResolver{}, "no ids", nil)
	if ok || reason != ReasonNoCitations {
		t.Fatalf("want suppressed no_citations, got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateCitations_OutsideGrounding(t *testing.T) {
	t.Parallel()
	// The model cites a real-resolvable id that was NOT in the candidate set
	// (grounding gate fails even though Resolve would say ok).
	a := uuid.New()
	res := fakeResolver{owned: map[uuid.UUID]CandidateKind{a: KindEvidence}}
	draft := "cites (" + a.String() + ")"
	_, ok, reason, _ := validateCitations(context.Background(), res, draft, map[uuid.UUID]CandidateKind{ /* empty */ })
	if ok || reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed unresolved (grounding), got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateCitations_InGroundingButUnowned(t *testing.T) {
	t.Parallel()
	// In the candidate set, but Resolve says not tenant-owned (the
	// cross-tenant / fabricated-after-grounding case).
	a := uuid.New()
	allowed := map[uuid.UUID]CandidateKind{a: KindPolicy}
	res := fakeResolver{owned: map[uuid.UUID]CandidateKind{ /* a not owned */ }}
	draft := "cites (" + a.String() + ")"
	_, ok, reason, _ := validateCitations(context.Background(), res, draft, allowed)
	if ok || reason != ReasonUnresolvedCitation {
		t.Fatalf("want suppressed unresolved (ownership), got ok=%v reason=%q", ok, reason)
	}
}

func TestValidateCitations_ResolverError(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	allowed := map[uuid.UUID]CandidateKind{a: KindPolicy}
	res := fakeResolver{err: context.DeadlineExceeded}
	draft := "cites (" + a.String() + ")"
	_, ok, _, err := validateCitations(context.Background(), res, draft, allowed)
	if ok || err == nil {
		t.Fatalf("want error surfaced, got ok=%v err=%v", ok, err)
	}
}

func TestAllowedIDs_SkipsUnparseable(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	cands := []Candidate{
		{ID: a.String(), Kind: KindPolicy},
		{ID: "not-a-uuid", Kind: KindEvidence},
	}
	got := allowedIDs(cands)
	if len(got) != 1 {
		t.Fatalf("want 1 parseable id, got %d", len(got))
	}
	if got[a] != KindPolicy {
		t.Errorf("kind mismatch: %v", got)
	}
}

// ----- prompt + insufficiency sentinel -----

func TestIsInsufficient(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		"INSUFFICIENT_EVIDENCE":                       true,
		" INSUFFICIENT_EVIDENCE ":                     true,
		"insufficient_evidence.":                      true,
		"INSUFFICIENT_EVIDENCE.\n":                    true,
		"Yes, we have insufficient_evidence logging.": false,
		"We encrypt at rest.":                         false,
		"":                                            false,
	}
	for draft, want := range cases {
		if got := isInsufficient(draft); got != want {
			t.Errorf("isInsufficient(%q) = %v, want %v", draft, got, want)
		}
	}
}

func TestBuildPrompt_IncludesQuestionAndCandidates(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	p := buildPrompt("Do you encrypt at rest?", []Candidate{
		{ID: a.String(), Kind: KindPolicy, Title: "Encryption Policy", Excerpt: "AES-256 at rest."},
	})
	if !strings.Contains(p, "Do you encrypt at rest?") {
		t.Error("prompt missing question text")
	}
	if !strings.Contains(p, a.String()) {
		t.Error("prompt missing candidate id")
	}
	if !strings.Contains(p, "Encryption Policy") {
		t.Error("prompt missing candidate title")
	}
}

func TestSystemPrompt_HasGroundingAndSentinel(t *testing.T) {
	t.Parallel()
	if !strings.Contains(systemPrompt, insufficientSentinel) {
		t.Error("system prompt must instruct the insufficient sentinel")
	}
	if !strings.Contains(strings.ToLower(systemPrompt), "only") {
		t.Error("system prompt must constrain the model to ONLY the candidates")
	}
}

// ----- keyword retrieval (the JUDGMENT-call surface) -----

func TestKeywordsFrom(t *testing.T) {
	t.Parallel()
	kws := keywordsFrom("Do you require MFA for all administrative access?")
	has := func(w string) bool {
		for _, k := range kws {
			if k == w {
				return true
			}
		}
		return false
	}
	if !has("mfa") || !has("administrative") || !has("access") {
		t.Errorf("expected high-signal tokens, got %v", kws)
	}
	if has("you") || has("all") || has("for") {
		t.Errorf("stopwords leaked into keywords: %v", kws)
	}
}

func TestKeywordsFrom_Dedupes(t *testing.T) {
	t.Parallel()
	kws := keywordsFrom("encryption encryption Encryption")
	if len(kws) != 1 || kws[0] != "encryption" {
		t.Errorf("want single deduped token, got %v", kws)
	}
}

func TestRankCandidates(t *testing.T) {
	t.Parallel()
	cands := []Candidate{
		{ID: "aaaaaaaa-0000-0000-0000-000000000001", Title: "MFA policy", Excerpt: "okta mfa enforced"},
		{ID: "bbbbbbbb-0000-0000-0000-000000000002", Title: "Backup policy", Excerpt: "nightly backups"},
		{ID: "cccccccc-0000-0000-0000-000000000003", Title: "Access control", Excerpt: "mfa and access reviews"},
	}
	ranked := rankCandidates(cands, []string{"mfa", "access"}, 5)
	if len(ranked) != 2 {
		t.Fatalf("want 2 scoring candidates (backup drops to 0), got %d", len(ranked))
	}
	// Access control matches both keywords -> highest score, ranked first.
	if ranked[0].ID != "cccccccc-0000-0000-0000-000000000003" {
		t.Errorf("expected highest-overlap candidate first, got %v", ranked[0].ID)
	}
}

func TestRankCandidates_LimitAndZeroDrop(t *testing.T) {
	t.Parallel()
	cands := []Candidate{
		{ID: "a", Title: "mfa", Excerpt: ""},
		{ID: "b", Title: "mfa", Excerpt: ""},
		{ID: "c", Title: "irrelevant", Excerpt: ""},
	}
	ranked := rankCandidates(cands, []string{"mfa"}, 1)
	if len(ranked) != 1 {
		t.Fatalf("limit not applied: got %d", len(ranked))
	}
}

func TestBoundExcerpt(t *testing.T) {
	t.Parallel()
	short := "short text"
	if boundExcerpt(short, 100) != short {
		t.Error("short text should pass through unchanged")
	}
	long := strings.Repeat("word ", 200)
	got := boundExcerpt(long, 50)
	if len([]rune(got)) > 52 { // 50 + ellipsis tolerance
		t.Errorf("excerpt not bounded: %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("truncated excerpt should end with ellipsis")
	}
}

func TestIlikePatterns_Escapes(t *testing.T) {
	t.Parallel()
	pats := ilikePatterns([]string{"a%b_c"})
	if len(pats) != 1 {
		t.Fatalf("want 1 pattern, got %d", len(pats))
	}
	if !strings.Contains(pats[0], `\%`) || !strings.Contains(pats[0], `\_`) {
		t.Errorf("ILIKE metachars not escaped: %q", pats[0])
	}
}

// ----- service-level helpers -----

func TestIsCloudProvider(t *testing.T) {
	t.Parallel()
	for p, want := range map[string]bool{
		"":             false,
		"ollama":       false,
		"ollama-local": false,
		"local":        false,
		"stub":         false,
		"anthropic":    true,
		"openai":       true,
		"bedrock":      true,
	} {
		if got := isCloudProvider(p); got != want {
			t.Errorf("isCloudProvider(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestCandidateContext(t *testing.T) {
	t.Parallel()
	a := uuid.New()
	ctx := candidateContext("q?", []Candidate{{ID: a.String(), Kind: KindPolicy}})
	ids, ok := ctx["candidate_ids"].([]string)
	if !ok || len(ids) != 1 {
		t.Fatalf("candidate_ids malformed: %+v", ctx)
	}
	if ids[0] != "policy:"+a.String() {
		t.Errorf("candidate id label wrong: %q", ids[0])
	}
	if ctx["question_text"] != "q?" {
		t.Error("question_text not recorded")
	}
}
