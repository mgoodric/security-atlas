// #1814: board briefs and packs print the organisation's category name,
// and packs stored before the name was frozen into them still render.

package board

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRankTopRisks_CarriesCategoryName(t *testing.T) {
	t.Parallel()
	got := rankTopRisks([]riskRow{{
		ID:           uuid.New(),
		Title:        "Unpatched CVE backlog",
		Category:     "operational",
		CategoryName: "Operational Resilience",
		Treatment:    "mitigate",
	}}, time.Now(), 3)
	if len(got) != 1 {
		t.Fatalf("rankTopRisks returned %d rows, want 1", len(got))
	}
	if got[0].Category != "operational" || got[0].CategoryName != "Operational Resilience" {
		t.Errorf("category = %q / %q, want operational / Operational Resilience", got[0].Category, got[0].CategoryName)
	}
}

func TestPackNarrative_TopRisksPrintCategoryName(t *testing.T) {
	t.Parallel()
	sec := newSection(SectionTopRisks, SectionData{TopRisks: []RiskAging{{
		Title: "Unpatched CVE backlog", Category: "operational", CategoryName: "Operational Resilience",
		Treatment: "mitigate", ResidualSeverity: 16, AgeDays: 95,
	}}})
	got, err := renderSectionNarrative(sec, "2026-03-31")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if !strings.Contains(got, "(Operational Resilience, treatment:") {
		t.Errorf("pack narrative does not print the category name: %q", got)
	}
}

func TestBriefNarrative_TopRisksPrintCategoryName(t *testing.T) {
	t.Parallel()
	got, err := RenderNarrative(Brief{PeriodEnd: "2026-04-30", TopRisks: []RiskAging{{
		Title: "Unpatched CVE backlog", Category: "operational", CategoryName: "Operational Resilience",
		Treatment: "mitigate", ResidualSeverity: 16.5, AgeDays: 95,
	}}})
	if err != nil {
		t.Fatalf("RenderNarrative: %v", err)
	}
	if !strings.Contains(got, "(Operational Resilience, treatment:") {
		t.Errorf("brief narrative does not print the category name: %q", got)
	}
}

// A pack stored before #1814 has no category_name in its top risks. It
// still decodes, and the PDF table and a re-rendered narrative show the
// slug it does carry.
func TestLegacyStoredPack_TopRisksFallBackToSlug(t *testing.T) {
	t.Parallel()
	legacy := []byte(`{"key":"top_risks","title":"Top risks · aging","templated_text":"","override_text":"","approved":false,` +
		`"data":{"top_risks":[{"id":"6f1c2c1e-0000-4000-8000-000000000001","title":"Unpatched CVE backlog",` +
		`"category":"operational","treatment":"mitigate","residual_severity":16.5,"age_days":95}]}}`)
	var sec Section
	if err := json.Unmarshal(legacy, &sec); err != nil {
		t.Fatalf("decode legacy section: %v", err)
	}
	if n := len(sec.Data.TopRisks); n != 1 {
		t.Fatalf("legacy section decoded %d top risks, want 1", n)
	}
	if got := sec.Data.TopRisks[0].CategoryLabel(); got != "operational" {
		t.Errorf("CategoryLabel() = %q, want the slug operational", got)
	}

	text, err := renderSectionNarrative(sec, "2026-03-31")
	if err != nil {
		t.Fatalf("renderSectionNarrative: %v", err)
	}
	if !strings.Contains(text, "(operational, treatment:") {
		t.Errorf("legacy narrative does not fall back to the slug: %q", text)
	}

	sp := newDraftPack()
	sp.Content.Sections[SectionTopRisks] = sec
	if html := buildPackHTML(sp); !strings.Contains(html, "<td>operational</td>") {
		t.Error("legacy pack PDF does not fall back to the category slug")
	}
}

func TestBuildPackHTML_TopRisksPrintCategoryName(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	sec := sp.Content.Sections[SectionTopRisks]
	sec.Data.TopRisks = []RiskAging{{
		Title: "Unpatched CVE backlog", Category: "operational", CategoryName: "Operational <Resilience>",
		Treatment: "mitigate", ResidualSeverity: 16, AgeDays: 95,
	}}
	sp.Content.Sections[SectionTopRisks] = sec
	html := buildPackHTML(sp)
	if !strings.Contains(html, "<td>Operational &lt;Resilience&gt;</td>") {
		t.Error("pack PDF does not print the escaped category name")
	}
}
