// Slice 440 — pure-Go unit branches for the board-narrative helpers (slice 353
// pure-Go pre-DB convention). Fast table tests for the validators / formatters
// that do not need Postgres; the Store's DB methods are covered by the
// integration tier.
package boardnarrative

import (
	"strings"
	"testing"

	"github.com/mgoodric/security-atlas/internal/board"
)

func TestBoundExcerpt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		n    int
		want string
	}{
		{"under-limit-unchanged", "short", 240, "short"},
		{"exact-limit-unchanged", "abcde", 5, "abcde"},
		{"over-limit-truncated", "abcdefgh", 5, "abcde…"},
		{"multibyte-safe", "héllo wörld", 5, "héllo…"},
		{"empty", "", 240, ""},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := boundExcerpt(tc.in, tc.n); got != tc.want {
				t.Fatalf("boundExcerpt(%q,%d) = %q, want %q", tc.in, tc.n, got, tc.want)
			}
		})
	}
}

func TestRollupFromBrief_NoFrameworks(t *testing.T) {
	t.Parallel()
	_, err := RollupFromBrief(board.Brief{}, nil)
	if err != ErrNoBriefData {
		t.Fatalf("empty brief: want ErrNoBriefData, got %v", err)
	}
}

func TestRollupFromBrief_BoundsExcerpts(t *testing.T) {
	t.Parallel()
	b := board.Brief{
		PeriodEnd:  "2026-05-31",
		Frameworks: []board.FrameworkPosture{{Name: "SOC 2", CoveragePct: 80, FreshnessPct: 90}},
		Drift:      board.DriftSummary{WindowDays: 30, Delta: 1, FlippedOutCount: 0},
	}
	xs := make([]Excerpt, maxCitedExcerpts+5)
	for i := range xs {
		xs[i] = Excerpt{ID: "id", Kind: KindControl, Title: "t"}
	}
	r, err := RollupFromBrief(b, xs)
	if err != nil {
		t.Fatalf("RollupFromBrief: %v", err)
	}
	if len(r.Excerpts) != maxCitedExcerpts {
		t.Fatalf("excerpts not bounded: got %d, want %d", len(r.Excerpts), maxCitedExcerpts)
	}
	if r.FrameworkCount != 1 || r.CoveragePct != 80 || r.FreshnessPct != 90 {
		t.Fatalf("rollup projection wrong: %+v", r)
	}
}

func TestIsCloudProvider(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"", "ollama", "ollama-local", "local", "stub", "OLLAMA"} {
		if isCloudProvider(p) {
			t.Errorf("isCloudProvider(%q) = true, want false (local)", p)
		}
	}
	for _, p := range []string{"anthropic", "openai", "bedrock"} {
		if !isCloudProvider(p) {
			t.Errorf("isCloudProvider(%q) = false, want true (cloud)", p)
		}
	}
}

func TestBannedPhraseListForPrompt(t *testing.T) {
	t.Parallel()
	got := BannedPhraseListForPrompt()
	if !strings.Contains(got, "we are proud to report") {
		t.Fatalf("ban list missing a known phrase:\n%s", got)
	}
	// Every line is a bullet.
	for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
		if !strings.HasPrefix(line, "  - ") {
			t.Fatalf("ban-list line not a bullet: %q", line)
		}
	}
}

func TestSortExcerpts(t *testing.T) {
	t.Parallel()
	xs := []Excerpt{
		{ID: "z", Kind: KindEvidence},
		{ID: "a", Kind: KindEvidence},
		{ID: "m", Kind: KindControl},
	}
	sortExcerpts(xs)
	// control sorts before evidence; within a kind, by id ascending.
	if xs[0].Kind != KindControl || xs[1].ID != "a" || xs[2].ID != "z" {
		t.Fatalf("sortExcerpts order wrong: %+v", xs)
	}
}

func TestAllowedExcerptIDs(t *testing.T) {
	t.Parallel()
	r := Rollup{Excerpts: []Excerpt{
		{ID: "c1", Kind: KindControl},
		{ID: "e1", Kind: KindEvidence},
	}}
	got := r.allowedExcerptIDs()
	if got["c1"] != KindControl || got["e1"] != KindEvidence {
		t.Fatalf("allowedExcerptIDs wrong: %+v", got)
	}
	if _, ok := got["nope"]; ok {
		t.Fatalf("allowedExcerptIDs should not contain absent id")
	}
}

// ----- slice 501: reusable numeric-verification library (AC-3 / AC-4 / AC-10) -----

// TestVerifyNumbers_Library proves the reusable numeric library auto-rejects a
// fabricated number and accepts a correct one ACROSS section shapes — the
// load-bearing extraction (AC-10). The library depends on nothing
// section-specific: just (text, allowed-set, period-end).
func TestVerifyNumbers_Library(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		text      string
		allowed   map[int]bool
		periodEnd string
		wantOK    bool
	}{
		{
			name:      "coverage-correct",
			text:      "1. Coverage is 84% and freshness is 91%.",
			allowed:   map[int]bool{84: true, 91: true},
			periodEnd: "2026-05-31",
			wantOK:    true,
		},
		{
			name:      "coverage-fabricated",
			text:      "1. Coverage is 85% and freshness is 91%.",
			allowed:   map[int]bool{84: true, 91: true},
			periodEnd: "2026-05-31",
			wantOK:    false, // 85 is not in the rollup
		},
		{
			name:      "risk-correct",
			text:      "1. There are 3 open risks; the worst residual severity is 12, oldest age 47 days.",
			allowed:   map[int]bool{3: true, 12: true, 47: true},
			periodEnd: "2026-05-31",
			wantOK:    true,
		},
		{
			name:      "risk-fabricated-severity",
			text:      "1. There are 3 open risks; the worst residual severity is 99, oldest age 47 days.",
			allowed:   map[int]bool{3: true, 12: true, 47: true},
			periodEnd: "2026-05-31",
			wantOK:    false, // 99 fabricated
		},
		{
			name:      "drift-signed-delta-correct",
			text:      "1. Over the 30-day window the net drift was -3; 3 controls drifted out.",
			allowed:   map[int]bool{30: true, 3: true, -3: true},
			periodEnd: "2026-05-31",
			wantOK:    true,
		},
		{
			name:      "period-end-not-fabrication",
			text:      "## Section (2026-05-31)\n1. Coverage is 84%.",
			allowed:   map[int]bool{84: true},
			periodEnd: "2026-05-31",
			wantOK:    true, // the date digits are stripped, not flagged
		},
		{
			name:      "list-markers-not-claims",
			text:      "1. Coverage is 84%.\n2. Freshness is 91%.\n3. All good.",
			allowed:   map[int]bool{84: true, 91: true},
			periodEnd: "2026-05-31",
			wantOK:    true, // 1./2./3. markers stripped
		},
		{
			name:      "invented-date-rejected",
			text:      "1. Coverage is 84% as of 2099-12-31.",
			allowed:   map[int]bool{84: true},
			periodEnd: "2026-05-31",
			wantOK:    false, // a date the model invented is not the period-end label
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := VerifyNumbers(tc.text, tc.allowed, tc.periodEnd); got != tc.wantOK {
				t.Fatalf("VerifyNumbers(%q) = %v, want %v", tc.text, got, tc.wantOK)
			}
		})
	}
}

// TestAllowedNumbers_PerSection proves each section's rollup emits the right
// ground-truth integer set (so the library pins the right numbers per section).
func TestAllowedNumbers_PerSection(t *testing.T) {
	t.Parallel()
	cov := Rollup{CoveragePct: 84, FreshnessPct: 91, FrameworkCount: 1, Delta: -3, FlippedOutCount: 3, WindowDays: 30}
	if a := cov.AllowedNumbers(); !a[84] || !a[91] || !a[1] || !a[3] || !a[30] || !a[-3] {
		t.Fatalf("coverage AllowedNumbers missing a ground-truth value: %+v", a)
	}
	risk := Rollup{RiskCount: 3, WorstResidualSeverity: 12, OldestRiskAgeDays: 47, riskSeverity: true}
	ra := risk.AllowedNumbers()
	if !ra[3] || !ra[12] || !ra[47] {
		t.Fatalf("risk AllowedNumbers missing a value: %+v", ra)
	}
	if ra[84] {
		t.Fatalf("risk AllowedNumbers should not contain a coverage number")
	}
	drift := Rollup{Delta: -3, FlippedOutCount: 3, WindowDays: 30, driftOnly: true}
	da := drift.AllowedNumbers()
	if !da[30] || !da[3] || !da[-3] {
		t.Fatalf("drift AllowedNumbers missing a value: %+v", da)
	}
	if da[84] {
		t.Fatalf("drift AllowedNumbers should not contain a coverage number")
	}
}

// ----- slice 501: banned-phrase enforcement honoring the allow-list (AC-11) -----

// TestContainsBannedPhrase_AndAllowList proves the post-generation tone check
// (1) rejects a Section-1 unambiguous banned phrase, (2) rejects a Section-3
// filler form, and (3) HONORS the Section-3 allow-list (the permitted form is
// NOT false-rejected) — P0-501-3 / P0-501-4.
func TestContainsBannedPhrase_AndAllowList(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		text       string
		wantBanned bool
	}{
		// Section 1 — unambiguous, exact-match.
		{"proud-to-report", "We are proud to report 84% coverage.", true},
		{"industry-leading", "Our industry-leading controls passed.", true},
		{"world-class-case-insensitive", "A WORLD-CLASS program.", true},
		{"unprompted-superlative", "An unprecedented quarter for the program.", true},
		// Section 3 — filler forms (rejected).
		{"robust-program-filler", "We have a robust program.", true},
		{"robust-controls-filler", "Our robust controls held up.", true},
		{"strong-posture-filler", "A strong security posture this quarter.", true},
		{"leverage-the-filler", "We leverage the OPA engine widely.", true},
		{"comprehensive-solution-filler", "A comprehensive solution shipped.", true},
		// Section 3 — PERMITTED forms (must NOT be rejected — the allow-list).
		{"robust-against-permitted", "The change-management process is robust against unauthorized merges.", false},
		{"strong-quantified-permitted", "Strong evidence freshness compared to the prior quarter (94% vs 88% within window).", false},
		{"comprehensive-coverage-permitted", "The SCF anchor catalog provides comprehensive coverage of 1403 controls.", false},
		// Clean, plain board text — no false-positive.
		{"clean-clinical", "Coverage rose from 78% to 84% this quarter; two findings opened.", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := containsBannedPhrase(tc.text); got != tc.wantBanned {
				t.Fatalf("containsBannedPhrase(%q) = %v, want %v", tc.text, got, tc.wantBanned)
			}
		})
	}
}

// ----- slice 501: section-shape across the section set (AC-9 shape leg) -----

func TestEnforceShapeFor_Sections(t *testing.T) {
	t.Parallel()
	risk := strings.Join([]string{
		riskHeading,
		"1. There are 3 open risks aging.",
		"2. Worst residual severity is 12; oldest age is 47 days.",
		"3. Coverage is grounded in the access-review control.",
	}, "\n")
	if !enforceShapeFor(risk, riskHeading, riskExpectedItems) {
		t.Fatalf("valid risk shape rejected")
	}
	// Wrong item count.
	bad := risk + "\n4. Extra item."
	if enforceShapeFor(bad, riskHeading, riskExpectedItems) {
		t.Fatalf("risk shape with extra item accepted")
	}
	// Missing heading.
	noHead := strings.Replace(risk, riskHeading, "## Wrong heading", 1)
	if enforceShapeFor(noHead, riskHeading, riskExpectedItems) {
		t.Fatalf("risk shape with wrong heading accepted")
	}
}

// TestSectionDefs_AllWired proves every AI-drafted section has a complete,
// self-consistent SectionDef (no half-wired section can ship).
func TestSectionDefs_AllWired(t *testing.T) {
	t.Parallel()
	for _, key := range AIDraftedSections {
		def, ok := sectionDef(key)
		if !ok {
			t.Fatalf("section %q has no SectionDef", key)
		}
		if def.Heading == "" || def.ExpectedItems <= 0 || def.PromptVersion == "" {
			t.Fatalf("section %q SectionDef incomplete: %+v", key, def)
		}
		if def.buildRollup == nil || def.systemPrompt == nil || def.userPrompt == nil {
			t.Fatalf("section %q SectionDef missing a builder fn", key)
		}
		// Every section's system prompt embeds the banned-phrase list (the
		// guardrail-7 wiring is present per section — AC-5).
		if !strings.Contains(def.systemPrompt(), "we are proud to report") {
			t.Fatalf("section %q system prompt missing the banned-phrase list", key)
		}
		// Every section's system prompt names its exact heading (shape wiring).
		if !strings.Contains(def.systemPrompt(), def.Heading) {
			t.Fatalf("section %q system prompt missing its heading", key)
		}
	}
}

// TestRiskRollupFromBrief projects a Brief's TopRisks into the risk rollup.
func TestRiskRollupFromBrief(t *testing.T) {
	t.Parallel()
	b := board.Brief{
		PeriodEnd:  "2026-05-31",
		Frameworks: []board.FrameworkPosture{{Name: "SOC 2"}},
		TopRisks: []board.RiskAging{
			{ResidualSeverity: 11.6, AgeDays: 20},
			{ResidualSeverity: 8.2, AgeDays: 47},
		},
	}
	r, err := riskRollupFromBrief(b, nil)
	if err != nil {
		t.Fatalf("riskRollupFromBrief: %v", err)
	}
	if r.RiskCount != 2 || r.WorstResidualSeverity != 12 || r.OldestRiskAgeDays != 47 {
		t.Fatalf("risk rollup wrong: %+v", r)
	}
}
