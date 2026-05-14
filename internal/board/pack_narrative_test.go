// Unit tests for the quarterly-board-pack TEMPLATED narrative renderer
// (slice 032). These assert the narrative is a pure, deterministic function
// of the structured Pack — NO LLM, NO network. The P0 anti-criterion "Does
// NOT generate AI narrative in v1 (templated only)" is verified structurally
// here: the renderer is a Go text/template, the output is byte-stable across
// repeated calls, and the operator-entered sections emit a placeholder that
// names them as operator-entered (decision D3 — no fabricated coverage).

package board

import (
	"strings"
	"testing"
)

// Every fixed section has a parsed template — sectionTemplates covers
// exactly SectionKeys.
func TestSectionTemplates_CoverEverySectionKey(t *testing.T) {
	for _, key := range SectionKeys {
		if _, ok := sectionTemplates[key]; !ok {
			t.Errorf("no narrative template for section key %q", key)
		}
	}
	if len(sectionTemplates) != len(SectionKeys) {
		t.Errorf("sectionTemplates has %d entries, want %d", len(sectionTemplates), len(SectionKeys))
	}
}

// renderSectionNarrative is deterministic — the same Section renders to the
// same string every time (no clock, no randomness, no LLM).
func TestRenderSectionNarrative_Deterministic(t *testing.T) {
	sec := newSection(SectionPosture, SectionData{
		Frameworks: []FrameworkPosture{
			{Slug: "soc2", Name: "SOC 2", CoveragePct: 94, TrendArrow: TrendUp, Delta: 2, State: "audit-ready"},
		},
	})
	first, err := renderSectionNarrative(sec, "2026-03-31")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	for i := 0; i < 5; i++ {
		again, err := renderSectionNarrative(sec, "2026-03-31")
		if err != nil {
			t.Fatalf("renderSectionNarrative (repeat): %v", err)
		}
		if again != first {
			t.Fatalf("renderSectionNarrative is not deterministic:\n first: %q\n again: %q", first, again)
		}
	}
	if !strings.Contains(first, "SOC 2") || !strings.Contains(first, "94%") {
		t.Errorf("posture narrative missing framework data: %q", first)
	}
}

// renderSectionNarrative for an unknown key returns ErrUnknownSection — it
// never panics or fabricates.
func TestRenderSectionNarrative_UnknownKey(t *testing.T) {
	_, err := renderSectionNarrative(Section{Key: "bogus"}, "2026-03-31")
	if err == nil {
		t.Fatal("renderSectionNarrative accepted an unknown section key")
	}
}

// The operator-entered sections (decision D3) emit a placeholder narrative
// that explicitly names them as operator-entered — the generator must not
// fabricate phishing rates, vendor numbers, or spend.
func TestRenderSectionNarrative_OperatorSectionsArePlaceholders(t *testing.T) {
	cases := []struct {
		key         string
		mustContain string
	}{
		{SectionOperational, "operator-entered"},
		{SectionInvestment, "operator-entered"},
		{SectionAsks, "authored by the security leader"},
	}
	for _, c := range cases {
		sec := newSection(c.key, SectionData{})
		text, err := renderSectionNarrative(sec, "2026-03-31")
		if err != nil {
			t.Fatalf("%s: renderSectionNarrative: %v", c.key, err)
		}
		if !strings.Contains(text, c.mustContain) {
			t.Errorf("%s placeholder narrative = %q, want it to contain %q", c.key, text, c.mustContain)
		}
	}
}

// The investment narrative switches from placeholder to a computed sentence
// once spend is entered (decision D5) — and the computed figure appears.
func TestRenderSectionNarrative_InvestmentWithSpend(t *testing.T) {
	sec := newSection(SectionInvestment, SectionData{
		SpendUSD:             40000,
		CoverageDelta:        8,
		CostPerCoveragePoint: 5000,
	})
	text, err := renderSectionNarrative(sec, "2026-03-31")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if strings.Contains(text, "operator-entered") {
		t.Errorf("investment narrative still a placeholder after spend entered: %q", text)
	}
	if !strings.Contains(text, "$40000") || !strings.Contains(text, "$5000") {
		t.Errorf("investment narrative missing computed figures: %q", text)
	}
}

// RenderPackNarrative renders the WHOLE pack to one Markdown document — a
// title, a status line, and one numbered heading per fixed section in
// canonical order. Deterministic, no LLM.
func TestRenderPackNarrative_WholeDocument(t *testing.T) {
	p := newSeededPack(t)
	md, err := RenderPackNarrative(p)
	if err != nil {
		t.Fatalf("RenderPackNarrative: %v", err)
	}
	if !strings.HasPrefix(md, "# Quarterly Board Pack — 2026-03-31") {
		head := md
		if len(head) > 60 {
			head = head[:60]
		}
		t.Errorf("narrative does not start with the pack title: %q", head)
	}
	if !strings.Contains(md, "Status: draft") {
		t.Error("narrative missing the status line")
	}
	// Every section gets a numbered heading, in canonical order.
	for i, key := range SectionKeys {
		heading := sectionTitles[key]
		if !strings.Contains(md, heading) {
			t.Errorf("narrative missing section %d heading %q", i+1, heading)
		}
	}
	// Determinism.
	again, _ := RenderPackNarrative(p)
	if again != md {
		t.Error("RenderPackNarrative is not deterministic")
	}
}

// RenderPackNarrative prefers a section's operator override over the
// templated text (AC-2).
func TestRenderPackNarrative_UsesOverride(t *testing.T) {
	p := newSeededPack(t)
	asks := p.Sections[SectionAsks]
	asks.OverrideText = "Approve the Q3 security hire."
	p.Sections[SectionAsks] = asks

	md, err := RenderPackNarrative(p)
	if err != nil {
		t.Fatalf("RenderPackNarrative: %v", err)
	}
	if !strings.Contains(md, "Approve the Q3 security hire.") {
		t.Error("whole-pack narrative did not use the asks-section override")
	}
}
