// Unit tests for the pure HTML-rendering layer of the quarterly board pack
// (internal/board/pack_pdf.go). The chromedp render itself needs a real
// browser and is exercised by integration tests; the HTML builder is pure
// and is the load-bearing surface for the visual fidelity to the
// Plans/_archive/mockups/board-pack.html reference.
//
// Load-bearing functions exercised:
//
//   - buildPackHTML — assembles the full HTML document. The chromedp PDF
//     render is deterministic given the same HTML, so a regression in this
//     function is the proximate cause of board-pack visual drift.
//   - writeSectionData — the per-section data renderer. Each section has a
//     distinct shape (stat grid, table, single-line summary, "not entered"
//     placeholder); regression in any branch produces a malformed PDF.
//   - writeOptIntRow — the operator-input row helper. Renders "not entered"
//     for nil pointers per decision D3 (never fabricate data).
//   - StoredPack.IsPublished + Section.EffectiveText — small accessors that
//     gate the renderer's published-vs-draft and override-vs-templated
//     branches.
//
// Branches deliberately left to integration:
//   - RenderPackPDF — requires chromedp + a real Chrome binary; integration
//     test asserts the leading `%PDF-` magic header.
//   - encodeForDataURL — already covered transitively by the slice-031
//     brief render path.
//
// Slice 279 — coverage lift target. Pre-lift merged %: 23.7. The pure HTML
// builder is the largest unit-testable surface in the package; closing
// these branches moves the package toward the 70% bar.

package board

import (
	"strings"
	"testing"
)

func newDraftPack() StoredPack {
	return StoredPack{
		PeriodEnd: "2026-06-30",
		Status:    PackStatusDraft,
		Content: Pack{
			PeriodEnd:   "2026-06-30",
			GeneratedAt: "2026-06-30T10:00:00Z",
			Status:      PackStatusDraft,
			Sections:    emptyPackSections(),
		},
	}
}

func emptyPackSections() map[string]Section {
	out := make(map[string]Section, len(SectionKeys))
	for _, k := range SectionKeys {
		out[k] = Section{Key: k, Title: sectionTitles[k], TemplatedText: "placeholder for " + k}
	}
	return out
}

// ===== buildPackHTML — top-level shape =====

func TestBuildPackHTML_DraftStatusRendersDraftBadge(t *testing.T) {
	t.Parallel()
	got := buildPackHTML(newDraftPack())
	if !strings.Contains(got, `class="status-draft"`) {
		t.Fatalf("draft pack must render status-draft badge; got len=%d", len(got))
	}
	if strings.Contains(got, `class="status-published"`) {
		t.Fatal("draft pack must NOT render status-published badge")
	}
}

func TestBuildPackHTML_PublishedStatusRendersPublishedBadge(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	sp.Status = PackStatusPublished
	sp.Content.Status = PackStatusPublished
	sp.PublishedBy = "alice@example.com"
	got := buildPackHTML(sp)
	if !strings.Contains(got, `class="status-published"`) {
		t.Fatal("published pack must render status-published badge")
	}
	if !strings.Contains(got, "alice@example.com") {
		t.Fatal("published pack must render publisher email")
	}
}

func TestBuildPackHTML_AllEightSectionsPresent(t *testing.T) {
	t.Parallel()
	got := buildPackHTML(newDraftPack())
	// Each section's title must appear; the renderer walks SectionKeys in
	// canonical order so missing a section means the slice-273-expanded set
	// (vendor_burndown at slot 5) drifted.
	for _, key := range SectionKeys {
		want := sectionTitles[key]
		if !strings.Contains(got, want) {
			t.Errorf("section title %q (key=%s) missing from HTML", want, key)
		}
	}
}

func TestBuildPackHTML_EscapesPeriodEnd(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	sp.Content.PeriodEnd = `2026-06-30<script>alert(1)</script>`
	got := buildPackHTML(sp)
	if strings.Contains(got, "<script>alert(1)") {
		t.Fatal("html.EscapeString should have escaped the script tag")
	}
	if !strings.Contains(got, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("expected escaped form in output")
	}
}

// ===== writeSectionData — per-section branches =====

func TestBuildPackHTML_PostureSectionRendersFrameworkCards(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	posture := sp.Content.Sections[SectionPosture]
	posture.Data.Frameworks = []FrameworkPosture{
		{Name: "SOC 2", CoveragePct: 87, TrendArrow: "up", Delta: 3, State: "Improving"},
		{Name: "ISO 27001", CoveragePct: 72, TrendArrow: "flat", Delta: 0, State: "Stable"},
	}
	sp.Content.Sections[SectionPosture] = posture
	got := buildPackHTML(sp)
	if !strings.Contains(got, "SOC 2") || !strings.Contains(got, "87%") {
		t.Fatal("posture card must render framework name + coverage %")
	}
	if !strings.Contains(got, "ISO 27001") || !strings.Contains(got, "72%") {
		t.Fatal("posture card must render second framework")
	}
	if !strings.Contains(got, "Improving") {
		t.Fatal("posture card must render state label")
	}
}

func TestBuildPackHTML_TopRisksRendersTableRows(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	risks := sp.Content.Sections[SectionTopRisks]
	risks.Data.TopRisks = []RiskAging{
		{Title: "Unpatched secrets manager", Category: "Cryptography", Treatment: "Mitigate", ResidualSeverity: 16.0, AgeDays: 45},
		{Title: "Phishing simulation drift", Category: "Awareness", Treatment: "Mitigate", ResidualSeverity: 9.0, AgeDays: 18},
	}
	sp.Content.Sections[SectionTopRisks] = risks
	got := buildPackHTML(sp)
	if !strings.Contains(got, "Unpatched secrets manager") {
		t.Fatal("top-risks table must list each risk title")
	}
	if !strings.Contains(got, "<table>") {
		t.Fatal("top-risks must render as a table")
	}
}

func TestBuildPackHTML_OpenFindingsRendersFindings(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	findings := sp.Content.Sections[SectionOpenFindings]
	findings.Data.Findings = []Finding{
		{ControlID: "ctrl-1", ScopeCellID: "cell-1", EvaluatedAt: "2026-06-29T08:00:00Z", FreshnessStatus: "fresh"},
	}
	findings.Data.FindingsCount = 1
	sp.Content.Sections[SectionOpenFindings] = findings
	got := buildPackHTML(sp)
	if !strings.Contains(got, "ctrl-1") {
		t.Fatal("findings must render the control id")
	}
}

func TestBuildPackHTML_VendorBurndown_EmptyShowsMutedNotice(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	// No VendorBurndownTotal set — zero total triggers the muted-notice
	// branch per slice 273's "no high-criticality vendors" copy.
	got := buildPackHTML(sp)
	if !strings.Contains(got, "No high-criticality vendors registered.") {
		t.Fatal("zero-total burndown must render the muted notice")
	}
}

func TestBuildPackHTML_VendorBurndown_PopulatedRendersGrid(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	vb := sp.Content.Sections[SectionVendorBurndown]
	vb.Data.VendorBurndownTotal = 12
	vb.Data.VendorBurndownOnTime = 9
	vb.Data.VendorBurndownPastDue = 3
	vb.Data.VendorBurndownOnTimePct = 75
	sp.Content.Sections[SectionVendorBurndown] = vb
	got := buildPackHTML(sp)
	if !strings.Contains(got, "75% of total") {
		t.Fatal("burndown grid must render 75% of total")
	}
	if !strings.Contains(got, "Past due") {
		t.Fatal("burndown grid must include the Past due card")
	}
}

func TestBuildPackHTML_OperationalRendersAllMetrics(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	op := sp.Content.Sections[SectionOperational]
	phishing := 94
	op.Data.PhishingPassRatePct = &phishing
	sp.Content.Sections[SectionOperational] = op
	got := buildPackHTML(sp)
	if !strings.Contains(got, "Phishing pass rate") {
		t.Fatal("operational table must list phishing pass rate label")
	}
	// The other metrics with nil pointers must render "not entered".
	if !strings.Contains(got, "not entered") {
		t.Fatal("nil operator metrics must render 'not entered' rather than 0")
	}
	if !strings.Contains(got, ">94<") {
		t.Fatal("populated operator metric must render its value")
	}
}

func TestBuildPackHTML_InvestmentRendersSummaryLine(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	inv := sp.Content.Sections[SectionInvestment]
	inv.Data.SpendUSD = 250000
	inv.Data.CoverageDelta = 5
	inv.Data.CostPerCoveragePoint = 50000.0
	sp.Content.Sections[SectionInvestment] = inv
	got := buildPackHTML(sp)
	if !strings.Contains(got, "$250000") {
		t.Fatal("investment line must render spend")
	}
	if !strings.Contains(got, "+5 pts") {
		t.Fatalf("investment line must render signed coverage delta")
	}
}

// ===== Override / EffectiveText branch =====

func TestBuildPackHTML_OverrideTextOverridesTemplated(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	asks := sp.Content.Sections[SectionAsks]
	asks.TemplatedText = "templated boilerplate"
	asks.OverrideText = "Board ask: approve the 2026 H2 staffing plan."
	sp.Content.Sections[SectionAsks] = asks
	got := buildPackHTML(sp)
	if !strings.Contains(got, "Board ask: approve the 2026 H2 staffing plan.") {
		t.Fatal("override text must appear in HTML output")
	}
	if strings.Contains(got, "templated boilerplate") {
		t.Fatal("templated text must NOT appear when override is set")
	}
}

func TestBuildPackHTML_ApprovedFlagRendersApprovedLabel(t *testing.T) {
	t.Parallel()
	sp := newDraftPack()
	approved := sp.Content.Sections[SectionPosture]
	approved.Approved = true
	sp.Content.Sections[SectionPosture] = approved
	got := buildPackHTML(sp)
	if !strings.Contains(got, `class="approved">approved`) {
		t.Fatal("approved section must render the approved label")
	}
}

func TestBuildPackHTML_UnapprovedFlagRendersUnapprovedLabel(t *testing.T) {
	t.Parallel()
	got := buildPackHTML(newDraftPack())
	// Default sections have Approved=false; every section renders as unapproved.
	if !strings.Contains(got, `class="unapproved">not approved`) {
		t.Fatal("unapproved section must render 'not approved' label")
	}
}

// ===== Section / StoredPack accessor branches =====

func TestSection_EffectiveText_OverridePreferred(t *testing.T) {
	t.Parallel()
	s := Section{TemplatedText: "templated", OverrideText: "override"}
	if got := s.EffectiveText(); got != "override" {
		t.Fatalf("EffectiveText = %q; want %q", got, "override")
	}
}

func TestSection_EffectiveText_FallsBackToTemplated(t *testing.T) {
	t.Parallel()
	s := Section{TemplatedText: "templated"}
	if got := s.EffectiveText(); got != "templated" {
		t.Fatalf("EffectiveText = %q; want %q", got, "templated")
	}
}

func TestSection_EffectiveText_BothEmpty(t *testing.T) {
	t.Parallel()
	s := Section{}
	if got := s.EffectiveText(); got != "" {
		t.Fatalf("EffectiveText = %q; want empty", got)
	}
}

func TestStoredPack_IsPublished_DraftFalse(t *testing.T) {
	t.Parallel()
	sp := StoredPack{Status: PackStatusDraft}
	if sp.IsPublished() {
		t.Fatal("draft pack must report IsPublished=false")
	}
}

func TestStoredPack_IsPublished_PublishedTrue(t *testing.T) {
	t.Parallel()
	sp := StoredPack{Status: PackStatusPublished}
	if !sp.IsPublished() {
		t.Fatal("published pack must report IsPublished=true")
	}
}

// ===== writeOptIntRow direct branch coverage =====

func TestWriteOptIntRow_NilRendersNotEntered(t *testing.T) {
	t.Parallel()
	var w strings.Builder
	writeOptIntRow(&w, "P1 patch median", nil)
	got := w.String()
	if !strings.Contains(got, "not entered") {
		t.Fatalf("nil value must render 'not entered': %s", got)
	}
}

func TestWriteOptIntRow_PopulatedRendersValue(t *testing.T) {
	t.Parallel()
	var w strings.Builder
	v := 7
	writeOptIntRow(&w, "Incident count", &v)
	got := w.String()
	if !strings.Contains(got, ">7<") {
		t.Fatalf("populated value must render the number: %s", got)
	}
	if strings.Contains(got, "not entered") {
		t.Fatal("populated value must NOT render 'not entered'")
	}
}

// ===== isKnownSection — gates several store paths =====

func TestIsKnownSection_AllSectionKeys(t *testing.T) {
	t.Parallel()
	for _, k := range SectionKeys {
		k := k
		t.Run(k, func(t *testing.T) {
			t.Parallel()
			if !isKnownSection(k) {
				t.Fatalf("isKnownSection(%q) = false; want true", k)
			}
		})
	}
}

func TestIsKnownSection_UnknownKey(t *testing.T) {
	t.Parallel()
	if isKnownSection("not_a_section") {
		t.Fatal("unknown section key must be rejected")
	}
	if isKnownSection("") {
		t.Fatal("empty section key must be rejected")
	}
}

// ===== allSectionsApproved — publish gate logic =====

func TestAllSectionsApproved_AllApproved(t *testing.T) {
	t.Parallel()
	p := Pack{Sections: emptyPackSections()}
	for k, s := range p.Sections {
		s.Approved = true
		p.Sections[k] = s
	}
	if key, ok := allSectionsApproved(p); !ok {
		t.Fatalf("allSectionsApproved = (%q, false); want (_, true)", key)
	}
}

func TestAllSectionsApproved_OneUnapproved(t *testing.T) {
	t.Parallel()
	p := Pack{Sections: emptyPackSections()}
	for k, s := range p.Sections {
		s.Approved = true
		p.Sections[k] = s
	}
	// Knock out the asks section.
	asks := p.Sections[SectionAsks]
	asks.Approved = false
	p.Sections[SectionAsks] = asks
	title, ok := allSectionsApproved(p)
	if ok {
		t.Fatal("allSectionsApproved = true; want false (asks unapproved)")
	}
	// The function returns the first-unapproved section's TITLE (not key)
	// for precise error messages. The asks section is keyed `asks`; the
	// title is whatever sectionTitles[SectionAsks] resolves to.
	want := sectionTitles[SectionAsks]
	if title != want {
		t.Fatalf("first-unapproved title = %q; want %q", title, want)
	}
}
