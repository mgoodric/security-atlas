// pack_narrative.go — the TEMPLATED quarterly-board-pack narrative renderer.
//
// AC-6 + the P0 anti-criterion "Does NOT generate AI narrative in v1
// (templated only)": the pack narrative is produced by Go `text/template`
// over the structured Pack. There is NO LLM, no inference call, no network
// path. The templates are compile-time constants; the output is a pure,
// deterministic function of the Pack.
//
// Two render entry points:
//
//   - renderSectionNarrative renders ONE section's templated text. The
//     generator calls this per section at generation time and writes the
//     result into Section.TemplatedText. For operator-entered sections
//     (operational_metrics, investment, asks) the template emits a
//     PLACEHOLDER narrative that names the section as operator-entered —
//     it never fabricates data (decision D3).
//   - RenderPackNarrative renders the WHOLE pack to a single Markdown
//     document — the board-ready paste-into-deck artifact (AC-6). It walks
//     SectionKeys in canonical order and emits each section's EffectiveText
//     (operator override when present, else the templated text — AC-2).
package board

import (
	"fmt"
	"strings"
	"text/template"
)

// packSectionFuncs are the per-section template helpers.
var packSectionFuncs = template.FuncMap{
	"arrow":  arrowGlyph,
	"signed": signedInt,
	"add1":   func(i int) int { return i + 1 },
	"pluralize": func(n int, singular, plural string) string {
		if n == 1 {
			return singular
		}
		return plural
	},
	// pluralize64 mirrors pluralize for int64 counts (slice 273 — the
	// vendor-burndown totals are int64 from the slice-122 surface).
	"pluralize64": func(n int64, singular, plural string) string {
		if n == 1 {
			return singular
		}
		return plural
	},
}

// sectionTemplates holds the per-section narrative template, keyed by
// section key. Parsed once at package init; a parse failure is a programmer
// error and panics at init rather than at render time.
var sectionTemplates = func() map[string]*template.Template {
	out := make(map[string]*template.Template, len(sectionSources))
	for key, src := range sectionSources {
		out[key] = template.Must(
			template.New("pack-section-" + key).Funcs(packSectionFuncs).Parse(src),
		)
	}
	return out
}()

// sectionRenderContext is what a per-section template executes against: the
// Section itself plus the pack-level PeriodEnd (so a section narrative can
// say "as of <quarter end>"). A small wrapper keeps the templates able to
// reference both without flattening the Section shape.
type sectionRenderContext struct {
	Section
	PeriodEnd string
}

// sectionSources are the per-section narrative templates. Pure text/template
// — no LLM. The operator-entered sections (operational_metrics, investment,
// asks) emit a placeholder that explicitly names the section as
// operator-entered (decision D3 — no fabricated coverage). Templates execute
// against a sectionRenderContext (the Section fields plus .PeriodEnd).
var sectionSources = map[string]string{
	SectionPosture: `Program posture as of {{ .PeriodEnd }}. ` +
		`{{ if .Data.Frameworks }}The program runs against {{ len .Data.Frameworks }} registered {{ pluralize (len .Data.Frameworks) "framework" "frameworks" }}: ` +
		`{{ range $i, $f := .Data.Frameworks }}{{ if $i }}; {{ end }}{{ $f.Name }} at {{ $f.CoveragePct }}% coverage ({{ $f.State }}, {{ arrow $f.TrendArrow }} {{ signed $f.Delta }} pts over the window){{ end }}.` +
		`{{ else }}No frameworks are registered yet — register a framework to populate per-framework posture.{{ end }}`,

	SectionTopRisks: `{{ if .Data.TopRisks }}The {{ len .Data.TopRisks }} highest-severity open {{ pluralize (len .Data.TopRisks) "risk" "risks" }} aging in the register: ` +
		`{{ range $i, $r := .Data.TopRisks }}{{ add1 $i }}) {{ $r.Title }} ({{ $r.Category }}, treatment: {{ $r.Treatment }}, residual severity {{ printf "%.1f" $r.ResidualSeverity }}, open {{ $r.AgeDays }} {{ pluralize $r.AgeDays "day" "days" }}){{ end }}.` +
		`{{ else }}No open risks in the register.{{ end }}`,

	SectionCoverageTrend: `Control coverage stands at {{ .Data.CoveragePct }}% at quarter end, ` +
		`against an operator-set baseline of {{ .Data.BaselineCoveragePct }}% — ` +
		`a {{ signed .Data.CoverageDelta }} point change over the period. ` +
		`Set the baseline_coverage_pct field to the prior-quarter coverage to make this delta meaningful.`,

	SectionOpenFindings: `{{ if .Data.Findings }}{{ .Data.FindingsCount }} open {{ pluralize .Data.FindingsCount "finding" "findings" }} ` +
		`as of {{ .PeriodEnd }} — each is a control whose latest evaluation is failing as of the quarter-end horizon.` +
		`{{ else }}No open findings — every evaluated control is passing as of {{ .PeriodEnd }}.{{ end }}`,

	// Slice 273: vendor_burndown narrative. Generated from the slice-122
	// high-criticality vendor burndown surface. Three scalars + a derived
	// on-time percentage; the templated text varies by total (none /
	// all-on-time / partial-overdue) so the narrative is informative even
	// at the trivial extremes. NO operator-entered fallback — this is a
	// GENERATED section per slice 273 D1.
	SectionVendorBurndown: `{{ if eq .Data.VendorBurndownTotal 0 }}` +
		`No high-criticality vendors are registered in the vendor module as of {{ .PeriodEnd }} — the burndown is empty. ` +
		`Register high-criticality vendors to populate this section.` +
		`{{ else if eq .Data.VendorBurndownPastDue 0 }}` +
		`All {{ .Data.VendorBurndownTotal }} high-criticality {{ pluralize64 .Data.VendorBurndownTotal "vendor" "vendors" }} ` +
		`are current on review cadence as of {{ .PeriodEnd }} (100% on-time).` +
		`{{ else }}` +
		`{{ .Data.VendorBurndownOnTime }} of {{ .Data.VendorBurndownTotal }} high-criticality vendors ` +
		`({{ .Data.VendorBurndownOnTimePct }}%) are current on review cadence as of {{ .PeriodEnd }}; ` +
		`{{ .Data.VendorBurndownPastDue }} {{ pluralize64 .Data.VendorBurndownPastDue "vendor is" "vendors are" }} past due.` +
		`{{ end }}`,

	SectionOperational: `Operational metrics are operator-entered for v1 — phishing pass rate, P1 patch median, ` +
		`incident count, and vendor reviews on time have no automated connector yet (the training connector and ` +
		`vulnerability-scanner connector ship in a later release). Fill in the operational metrics fields, then ` +
		`override this narrative with the quarter's numbers before approving the section.`,

	SectionInvestment: `{{ if gt .Data.SpendUSD 0 }}Security spend for the quarter was ${{ .Data.SpendUSD }}. ` +
		`Against a {{ signed .Data.CoverageDelta }} point coverage change, that is approximately ` +
		`${{ printf "%.0f" .Data.CostPerCoveragePoint }} per coverage point gained.` +
		`{{ else }}Investment vs coverage is operator-entered — enter the quarter's security spend (spend_usd) ` +
		`and the prior-quarter coverage baseline (baseline_coverage_pct). The cost-per-coverage-point is then ` +
		`computed as spend divided by the coverage delta. Override this narrative once the figures are in.{{ end }}`,

	SectionAsks: `Asks of the board are authored by the security leader — there is no generated or AI-assisted ` +
		`text for this section. Replace this placeholder with the specific decisions, budget, or headcount ` +
		`the program needs from the board this quarter, then approve the section.`,
}

// renderSectionNarrative renders one section's templated narrative against
// the pack-level periodEnd. Pure, deterministic, NO LLM. The generator
// calls this per section and stores the result in Section.TemplatedText.
func renderSectionNarrative(sec Section, periodEnd string) (string, error) {
	tmpl, ok := sectionTemplates[sec.Key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownSection, sec.Key)
	}
	var sb strings.Builder
	ctx := sectionRenderContext{Section: sec, PeriodEnd: periodEnd}
	if err := tmpl.Execute(&sb, ctx); err != nil {
		return "", fmt.Errorf("board: render section %q: %w", sec.Key, err)
	}
	return strings.TrimSpace(sb.String()), nil
}

// RenderPackNarrative renders the whole pack to a single Markdown document —
// the board-ready paste-into-deck artifact (AC-6). It walks SectionKeys in
// canonical order and emits each section's EffectiveText (operator override
// when present, else templated — AC-2). Pure, deterministic, NO LLM.
func RenderPackNarrative(p Pack) (string, error) {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# Quarterly Board Pack — %s\n\n", p.PeriodEnd)
	fmt.Fprintf(&sb, "_Generated %s. Status: %s._\n", p.GeneratedAt, p.Status)

	for i, key := range SectionKeys {
		sec, ok := p.Sections[key]
		if !ok {
			return "", fmt.Errorf("board: pack missing section %q", key)
		}
		fmt.Fprintf(&sb, "\n## %d. %s\n\n", i+1, sectionTitles[key])
		sb.WriteString(sec.EffectiveText())
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n \t") + "\n", nil
}
