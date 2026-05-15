// Unit tests for the quarterly board pack's pure logic (slice 032):
// section-key invariants, the cost-per-coverage-point formula (decision D5),
// the section-edit recompute (coverage delta + cost-per-point), and the
// publish gate (decision D6). These are deterministic transforms — no DB,
// no network, no LLM.

package board

import (
	"testing"
)

// SectionKeys is the FIXED enumerated set (decision D6). Every key has a
// title; the set is the single source of truth for "what sections exist".
func TestSectionKeys_AllHaveTitlesAndAreKnown(t *testing.T) {
	if len(SectionKeys) != 7 {
		t.Fatalf("SectionKeys has %d entries, want 7 (the fixed mockup section set)", len(SectionKeys))
	}
	for _, key := range SectionKeys {
		if _, ok := sectionTitles[key]; !ok {
			t.Errorf("section key %q has no title", key)
		}
		if !isKnownSection(key) {
			t.Errorf("section key %q is not recognized by isKnownSection", key)
		}
	}
	if isKnownSection("not_a_real_section") {
		t.Error("isKnownSection accepted an unknown key")
	}
}

// costPerCoveragePoint = spend / max(delta, 1) (decision D5). A non-positive
// delta floors the denominator at 1; zero spend yields zero.
func TestCostPerCoveragePoint(t *testing.T) {
	cases := []struct {
		name  string
		spend int
		delta int
		want  float64
	}{
		{"normal positive delta", 60000, 6, 10000},
		{"delta of one", 5000, 1, 5000},
		{"zero delta floors denominator at one", 8000, 0, 8000},
		{"negative delta floors denominator at one", 8000, -4, 8000},
		{"zero spend yields zero", 0, 5, 0},
		{"negative spend yields zero", -100, 5, 0},
	}
	for _, c := range cases {
		got := costPerCoveragePoint(c.spend, c.delta)
		if got != c.want {
			t.Errorf("%s: costPerCoveragePoint(%d, %d) = %v, want %v",
				c.name, c.spend, c.delta, got, c.want)
		}
	}
}

// allSectionsApproved: the publish gate (decision D6). Returns false (with
// the first unapproved section's title) until every fixed section is
// approved.
func TestAllSectionsApproved_PublishGate(t *testing.T) {
	// Build a pack with every fixed section, all unapproved.
	p := Pack{Sections: make(map[string]Section, len(SectionKeys))}
	for _, key := range SectionKeys {
		p.Sections[key] = Section{Key: key, Title: sectionTitles[key], Approved: false}
	}

	// Nothing approved -> gate is closed, names the first section.
	title, ok := allSectionsApproved(p)
	if ok {
		t.Fatal("allSectionsApproved returned true with zero sections approved")
	}
	if title != sectionTitles[SectionKeys[0]] {
		t.Errorf("first unapproved title = %q, want %q", title, sectionTitles[SectionKeys[0]])
	}

	// Approve all but the last -> still closed, names the last.
	for _, key := range SectionKeys[:len(SectionKeys)-1] {
		sec := p.Sections[key]
		sec.Approved = true
		p.Sections[key] = sec
	}
	title, ok = allSectionsApproved(p)
	if ok {
		t.Fatal("allSectionsApproved returned true with the last section unapproved")
	}
	lastKey := SectionKeys[len(SectionKeys)-1]
	if title != sectionTitles[lastKey] {
		t.Errorf("first unapproved title = %q, want %q (the last section)", title, sectionTitles[lastKey])
	}

	// Approve the last -> gate opens.
	sec := p.Sections[lastKey]
	sec.Approved = true
	p.Sections[lastKey] = sec
	if _, ok := allSectionsApproved(p); !ok {
		t.Error("allSectionsApproved returned false with every section approved")
	}
}

// allSectionsApproved: a pack MISSING a fixed section fails the gate (a
// section not present is treated as unapproved).
func TestAllSectionsApproved_MissingSectionFailsGate(t *testing.T) {
	p := Pack{Sections: make(map[string]Section)}
	// Only one section, approved — the other six are missing.
	p.Sections[SectionPosture] = Section{Key: SectionPosture, Approved: true}
	if _, ok := allSectionsApproved(p); ok {
		t.Error("allSectionsApproved returned true for a pack missing six fixed sections")
	}
}

// applySectionEdit: editing the investment section's spend recomputes
// cost-per-coverage-point against the current coverage delta (decision D5),
// and does NOT mutate the input pack.
func TestApplySectionEdit_InvestmentRecompute(t *testing.T) {
	p := newSeededPack(t)
	// coverage_trend seeded: coverage 80, baseline 0 -> delta 80.
	spend := 16000
	edit := SectionEdit{
		SectionKey: SectionInvestment,
		Inputs:     &SectionInputs{SpendUSD: &spend},
	}
	out := applySectionEdit(p, edit)

	inv := out.Sections[SectionInvestment]
	if inv.Data.SpendUSD != 16000 {
		t.Errorf("investment spend = %d, want 16000", inv.Data.SpendUSD)
	}
	// delta is 80 (coverage 80 - baseline 0); cost-per-point = 16000/80 = 200.
	if inv.Data.CostPerCoveragePoint != 200 {
		t.Errorf("cost per coverage point = %v, want 200", inv.Data.CostPerCoveragePoint)
	}
	// Input pack is untouched (applySectionEdit copies the section map).
	if p.Sections[SectionInvestment].Data.SpendUSD != 0 {
		t.Error("applySectionEdit mutated the input pack's investment section")
	}
}

// applySectionEdit: editing the coverage baseline recomputes the coverage
// delta on BOTH coverage_trend and investment (they are coupled, D5).
func TestApplySectionEdit_BaselineRecomputesDeltaOnBothSections(t *testing.T) {
	p := newSeededPack(t)
	// Set baseline to 65; coverage seeded at 80 -> delta should become 15.
	baseline := 65
	edit := SectionEdit{
		SectionKey: SectionCoverageTrend,
		Inputs:     &SectionInputs{BaselineCoveragePct: &baseline},
	}
	out := applySectionEdit(p, edit)

	trend := out.Sections[SectionCoverageTrend]
	if trend.Data.BaselineCoveragePct != 65 {
		t.Errorf("baseline = %d, want 65", trend.Data.BaselineCoveragePct)
	}
	if trend.Data.CoverageDelta != 15 {
		t.Errorf("coverage_trend delta = %d, want 15 (80 - 65)", trend.Data.CoverageDelta)
	}
	inv := out.Sections[SectionInvestment]
	if inv.Data.CoverageDelta != 15 {
		t.Errorf("investment delta = %d, want 15 (mirrors coverage_trend)", inv.Data.CoverageDelta)
	}
}

// applySectionEdit: an operator override on the asks section is stored and
// EffectiveText prefers it over the templated text (AC-2 / AC-4).
func TestApplySectionEdit_OverrideTextWins(t *testing.T) {
	p := newSeededPack(t)
	override := "We ask the board to approve a $250k security hire budget for Q3."
	edit := SectionEdit{
		SectionKey:   SectionAsks,
		OverrideText: &override,
	}
	out := applySectionEdit(p, edit)

	asks := out.Sections[SectionAsks]
	if asks.OverrideText != override {
		t.Errorf("override text not stored: got %q", asks.OverrideText)
	}
	if asks.EffectiveText() != override {
		t.Errorf("EffectiveText() = %q, want the override", asks.EffectiveText())
	}
	// Clearing the override (empty string) falls back to the templated text.
	empty := ""
	out2 := applySectionEdit(out, SectionEdit{SectionKey: SectionAsks, OverrideText: &empty})
	asks2 := out2.Sections[SectionAsks]
	if asks2.EffectiveText() != asks2.TemplatedText {
		t.Error("clearing the override should fall back to the templated text")
	}
}

// applySectionEdit: setting the approved flag flips it without touching
// other fields.
func TestApplySectionEdit_ApprovedFlag(t *testing.T) {
	p := newSeededPack(t)
	approved := true
	out := applySectionEdit(p, SectionEdit{SectionKey: SectionPosture, Approved: &approved})
	if !out.Sections[SectionPosture].Approved {
		t.Error("applySectionEdit did not set the approved flag")
	}
	// Other sections stay unapproved.
	if out.Sections[SectionTopRisks].Approved {
		t.Error("approving posture also approved top_risks")
	}
}

// newSeededPack builds a Pack with every fixed section populated the way the
// generator would seed it: coverage_trend at coverage 80 / baseline 0,
// investment at zero spend. Used by the applySectionEdit tests.
func newSeededPack(t *testing.T) Pack {
	t.Helper()
	p := Pack{
		PeriodEnd:   "2026-03-31",
		GeneratedAt: "2026-05-14T00:00:00Z",
		Status:      PackStatusDraft,
		Sections:    make(map[string]Section, len(SectionKeys)),
	}
	for _, key := range SectionKeys {
		p.Sections[key] = newSection(key, SectionData{})
	}
	trend := p.Sections[SectionCoverageTrend]
	trend.Data.CoveragePct = 80
	trend.Data.BaselineCoveragePct = 0
	trend.Data.CoverageDelta = 80
	p.Sections[SectionCoverageTrend] = trend

	inv := p.Sections[SectionInvestment]
	inv.Data.CoverageDelta = 80
	p.Sections[SectionInvestment] = inv

	// Render templated text so EffectiveText fallback tests are meaningful.
	for key, sec := range p.Sections {
		if text, err := renderSectionNarrative(sec, p.PeriodEnd); err == nil {
			sec.TemplatedText = text
			p.Sections[key] = sec
		}
	}
	return p
}
