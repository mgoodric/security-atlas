// narrative.go — the TEMPLATED board-brief narrative renderer.
//
// AC-3 + AC-6 + the P0 anti-criterion "Does NOT include LLM-generated
// narrative in v1": the brief narrative is produced by a Go `text/template`
// over the structured Brief. There is NO LLM, no inference call, no network
// path. The template is a compile-time constant; the output is a pure,
// deterministic function of the Brief.
//
// The rendered narrative is Markdown — board-ready for paste into the deck
// of choice (canvas §7.5: "PDF + editable Markdown/HTML for paste into the
// deck"). It is frozen into `board_briefs.narrative_md` at generation time
// (AC-3 example: "We are in audit-ready state for SOC 2 (94%, up 2 pts).").
package board

import (
	"strconv"
	"strings"
	"text/template"
)

// narrativeFuncs are the template helpers. Defined once, up front, so the
// single template.Must parse below resolves every function reference.
var narrativeFuncs = template.FuncMap{
	"arrow":  arrowGlyph,
	"signed": signedInt,
	"add1":   func(i int) int { return i + 1 },
}

// narrativeTmpl is the board-brief narrative template. Pure text/template —
// no LLM, no inference. Parsed once at package init; a parse failure is a
// programmer error and panics at init rather than at render time.
var narrativeTmpl = template.Must(
	template.New("board-brief").Funcs(narrativeFuncs).Parse(narrativeSource),
)

const narrativeSource = `# Monthly Board Brief — {{ .PeriodEnd }}

_Generated {{ .GeneratedAt }}. Pinned snapshot — posture as of the report date._

## Program posture

{{ range .Frameworks -}}
- **{{ .Name }}** — We are in {{ .State }} state for {{ .Name }} ({{ .CoveragePct }}% coverage, {{ arrow .TrendArrow }} {{ signed .Delta }} pts over 30 days). Evidence freshness {{ .FreshnessPct }}%.
{{ end }}
## Control drift — last {{ .Drift.WindowDays }} days

Over {{ .Drift.Since }} to {{ .Drift.Through }}, the program drift count is {{ signed .Drift.Delta }}{{ if gt .Drift.FlippedOutCount 0 }} — {{ .Drift.FlippedOutCount }} control(s) drifted out of passing{{ else }} — no controls drifted out of passing{{ end }}.

## Top risks aging

{{ if .TopRisks -}}
{{ range $i, $r := .TopRisks -}}
{{ add1 $i }}. **{{ $r.Title }}** ({{ $r.Category }}, treatment: {{ $r.Treatment }}) — residual severity {{ printf "%.1f" $r.ResidualSeverity }}, open {{ $r.AgeDays }} day(s).
{{ end -}}
{{ else -}}
No open risks in the register.
{{ end }}`

// RenderNarrative renders the templated Markdown narrative for a Brief. Pure,
// deterministic, NO LLM. Returns an error only if the template execution
// itself fails (which, for a fixed template over a well-formed Brief, does
// not happen in practice — the error path exists for completeness).
func RenderNarrative(b Brief) (string, error) {
	var sb strings.Builder
	if err := narrativeTmpl.Execute(&sb, b); err != nil {
		return "", err
	}
	// text/template can leave trailing whitespace from the trim markers;
	// normalize to a single trailing newline so the frozen narrative is
	// byte-stable.
	return strings.TrimRight(sb.String(), "\n \t") + "\n", nil
}

// arrowGlyph maps the trend-arrow token to a Markdown-safe glyph word. The
// template uses words ("up"/"down"/"flat") rather than Unicode arrows so the
// frozen narrative survives any downstream encoding.
func arrowGlyph(token string) string {
	switch token {
	case TrendUp:
		return "up"
	case TrendDown:
		return "down"
	default:
		return "flat"
	}
}

// signedInt formats an int with an explicit sign so "+3" / "-2" / "0" read
// unambiguously in the narrative.
func signedInt(n int) string {
	if n > 0 {
		return "+" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// Trend-arrow tokens. Stored in FrameworkPosture.TrendArrow and mapped to a
// glyph word by arrowGlyph.
const (
	TrendUp   = "up"
	TrendDown = "down"
	TrendFlat = "flat"
)
