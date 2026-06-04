// pack.go — the QUARTERLY BOARD PACK domain model (slice 032).
//
// The quarterly board pack (canvas §7.5, Plans/_archive/mockups/board-pack.html) is
// the full board-meeting artifact — it EXTENDS the slice-031 monthly brief
// into a multi-section, board-ready report with a draft -> publish lifecycle.
//
// Unlike the monthly brief (slice 031: append-only, frozen at generation),
// the quarterly pack has a DRAFT -> PUBLISHED lifecycle on a SINGLE row with
// a stable id (decision D1). While `draft`, the operator reviews each
// section, overrides the templated narrative, and approves it. `publish`
// freezes the pack — and only succeeds when EVERY section is approved
// (decision D6).
//
// The structured pack is serialized into `board_packs.content` (JSONB) in
// one column (decision D2): every section, including each section's
// `templated_text` / `override_text` / `approved` flag. One row, atomic
// UPDATEs.
//
// The narrative is TEMPLATED — Go `text/template` over the structured Pack
// (see pack_narrative.go). There is NO LLM in v1 (P0 anti-criterion): this
// package imports no inference client, opens no network connection, and has
// no Ollama / cloud-LLM path.
//
// Section data sources (decisions D3 + D4):
//
//   - posture / top_risks / coverage_trend / open_findings are GENERATED
//     from live platform read models (the slice-016 freshness + drift read
//     models, the risks table, the slice-012 control_evaluations).
//   - operational_metrics is OPERATOR-ENTERED. No v1 data source exists
//     (the training connector is v2; the vuln-scanner connector is not
//     built). The generator seeds it empty with a templated placeholder
//     narrative — it does NOT fabricate coverage (CLAUDE.md anti-pattern).
//   - investment is OPERATOR-ENTERED ($ spend) plus a computed coverage
//     delta against an operator `baseline_coverage_pct` (decision D5).
//   - asks is OPERATOR-AUTHORED freeform text — no AI generation (AC-4).
package board

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrPackBadPeriodEnd is returned (wrapped) when a pack period_end string is
// not a valid YYYY-MM-DD date. The HTTP layer maps it to 400.
var ErrPackBadPeriodEnd = errors.New("board: pack period_end must be a YYYY-MM-DD date")

// ErrPackNotFound is returned (wrapped) when a pack id does not resolve in
// the caller's tenant. The HTTP layer maps it to 404. A cross-tenant id also
// surfaces as ErrPackNotFound — RLS makes the foreign row invisible.
var ErrPackNotFound = errors.New("board: pack not found")

// ErrPackNotDraft is returned when a mutating operation (section override,
// section approval, publish) targets a pack that is no longer in `draft`
// status. The HTTP layer maps it to 409 — a published pack is immutable
// (AC-7, P0 anti-criterion).
var ErrPackNotDraft = errors.New("board: pack is published and immutable")

// ErrUnknownSection is returned when a section key is not one of the fixed
// enumerated SectionKeys. The HTTP layer maps it to 404.
var ErrUnknownSection = errors.New("board: unknown section key")

// ErrPackNotReady is returned by Publish when at least one section is not
// yet approved. The HTTP layer maps it to 409 — publish is gated on
// every section being approved (decision D6, AC-5).
var ErrPackNotReady = errors.New("board: cannot publish — every section must be approved first")

// Pack status values. A pack is created `draft` and transitions once to
// `published`; there is no path back (decision D1).
const (
	PackStatusDraft     = "draft"
	PackStatusPublished = "published"
)

// Section keys — the FIXED, ENUMERATED set of sections every quarterly pack
// carries (decision D6). `publish` iterates exactly this set and rejects the
// publish if any section's `approved` flag is false. Mirrors the eight
// sections of the canonical board-pack shape.
//
// Slice 273 (spillover from slice 221 D1=A) expanded the set from 7 to 8 by
// adding `vendor_burndown` — a GENERATED section sourced from the existing
// slice-122 high-criticality-vendor burndown surface. Position: slot §05,
// after `open_findings` and before the operator-entered `operational_metrics`
// (the slice's D1). Rationale: vendor burndown is a "what's wrong right
// now" panel (sibling to `open_findings`), and GENERATED sections precede
// operator-entered ones in canonical order. See
// docs/audit-log/273-decisions.md D1.
//
// The order of SectionKeys is the canonical render order — pack_narrative.go
// and pack_pdf.go both walk it.
const (
	SectionPosture        = "posture"
	SectionTopRisks       = "top_risks"
	SectionCoverageTrend  = "coverage_trend"
	SectionOpenFindings   = "open_findings"
	SectionVendorBurndown = "vendor_burndown"
	SectionOperational    = "operational_metrics"
	SectionInvestment     = "investment"
	SectionAsks           = "asks"
)

// SectionKeys is the canonical, ordered list of the fixed section keys. It is
// the single source of truth for "what sections exist" — the generator
// builds exactly these, the publish gate checks exactly these, and the
// renderers walk them in this order.
var SectionKeys = []string{
	SectionPosture,
	SectionTopRisks,
	SectionCoverageTrend,
	SectionOpenFindings,
	SectionVendorBurndown,
	SectionOperational,
	SectionInvestment,
	SectionAsks,
}

// sectionTitles maps a section key to its board-facing title. Used by the
// narrative + PDF renderers and the publish-gate error messages.
var sectionTitles = map[string]string{
	SectionPosture:        "Program posture",
	SectionTopRisks:       "Top risks · aging",
	SectionCoverageTrend:  "Control coverage trend",
	SectionOpenFindings:   "Open findings",
	SectionVendorBurndown: "Vendor risk burndown",
	SectionOperational:    "Operational metrics",
	SectionInvestment:     "Investment vs coverage",
	SectionAsks:           "Asks of the board",
}

// isKnownSection reports whether key is one of the fixed SectionKeys.
func isKnownSection(key string) bool {
	_, ok := sectionTitles[key]
	return ok
}

// Pack is the structured quarterly board pack — the shape that is serialized
// into `board_packs.content` (JSONB). While the pack is `draft` this content
// is mutated in place (atomic UPDATEs); at `publish` it is frozen. Every
// field is JSON-tagged so the stored content is self-describing.
type Pack struct {
	// PeriodEnd is the quarter-end report date the pack is pinned to
	// (YYYY-MM-DD).
	PeriodEnd string `json:"period_end"`
	// GeneratedAt is the wall-clock generation time (RFC3339).
	GeneratedAt string `json:"generated_at"`
	// Status mirrors the board_packs.status column — "draft" | "published".
	// Carried in content so a serialized pack is self-describing.
	Status string `json:"status"`
	// Sections is the per-section content, keyed by SectionKeys. Always
	// contains exactly the fixed set of keys after generation.
	Sections map[string]Section `json:"sections"`
}

// Section is one section of the pack. Every section — generated or
// operator-entered — carries the same envelope (decision D2):
//
//   - TemplatedText is the deterministic Go-template narrative the generator
//     produced. For operator-entered sections (operational_metrics,
//     investment, asks) it is a templated PLACEHOLDER narrative — never
//     fabricated data (decision D3).
//   - OverrideText, when non-empty, is the operator's edit. The renderers
//     prefer OverrideText over TemplatedText (AC-2).
//   - Approved is the per-section human approval flag. `publish` rejects the
//     pack unless every section's Approved is true (decision D6, AC-5).
//   - Data is the section's structured payload (posture rows, risk rows,
//     findings, operator inputs). Section-specific shapes live below.
type Section struct {
	Key           string      `json:"key"`
	Title         string      `json:"title"`
	TemplatedText string      `json:"templated_text"`
	OverrideText  string      `json:"override_text"`
	Approved      bool        `json:"approved"`
	Data          SectionData `json:"data"`
}

// EffectiveText returns the operator override when present, else the
// templated text — the text the renderers actually emit (AC-2).
func (s Section) EffectiveText() string {
	if s.OverrideText != "" {
		return s.OverrideText
	}
	return s.TemplatedText
}

// SectionData is the structured payload carried by a section. Each section
// populates the subset of fields relevant to it; the rest stay zero-valued.
// A single struct (rather than a per-section union) keeps the JSONB shape
// flat and the Go deserialization a single Unmarshal.
type SectionData struct {
	// posture: one row per registered framework (reuses the slice-031
	// FrameworkPosture shape — program-wide posture listed per framework).
	Frameworks []FrameworkPosture `json:"frameworks,omitempty"`

	// top_risks: the top risks aging (reuses the slice-031 RiskAging shape).
	TopRisks []RiskAging `json:"top_risks,omitempty"`

	// coverage_trend: the program coverage percentage at quarter end plus
	// the operator baseline it is compared against (decision D5).
	CoveragePct         int `json:"coverage_pct,omitempty"`
	BaselineCoveragePct int `json:"baseline_coverage_pct,omitempty"`
	CoverageDelta       int `json:"coverage_delta,omitempty"`

	// open_findings: failing control evaluations as of period_end
	// (decision D4 — a failing evaluation IS a finding for v1).
	Findings      []Finding `json:"findings,omitempty"`
	FindingsCount int       `json:"findings_count,omitempty"`

	// vendor_burndown (slice 273): GENERATED from the slice-122 high-
	// criticality vendor burndown surface (vendor.Store.Burndown). Three
	// scalars + a derived on-time fraction; pinned to `criticality=high`
	// per slice 273 D2 — the board concern is overdue reviews on the
	// vendors that matter. AsOf == period_end. The operator-entered
	// "Vendor reviews on time: N/M" tile in operational_metrics remains
	// independent (manual roster vs vendor-module tracked) — slice 273 D5.
	VendorBurndownTotal          int64   `json:"vendor_burndown_total,omitempty"`
	VendorBurndownOnTime         int64   `json:"vendor_burndown_on_time,omitempty"`
	VendorBurndownPastDue        int64   `json:"vendor_burndown_past_due,omitempty"`
	VendorBurndownOnTimePct      int     `json:"vendor_burndown_on_time_pct,omitempty"`
	VendorBurndownOnTimeFraction float64 `json:"vendor_burndown_on_time_fraction,omitempty"`

	// operational_metrics: OPERATOR-ENTERED (decision D3). No v1 data source
	// exists; these are seeded zero and the operator fills them in. A
	// "*Entered" flag would be redundant — the placeholder narrative names
	// the section as operator-entered explicitly.
	PhishingPassRatePct *int `json:"phishing_pass_rate_pct,omitempty"`
	P1PatchMedianDays   *int `json:"p1_patch_median_days,omitempty"`
	IncidentCount       *int `json:"incident_count,omitempty"`
	VendorReviewsOnTime *int `json:"vendor_reviews_on_time,omitempty"`
	VendorReviewsTotal  *int `json:"vendor_reviews_total,omitempty"`

	// investment: OPERATOR-ENTERED $ spend (decision D5). SpendUSD is the
	// quarter's security spend the operator types in; CostPerCoveragePoint
	// is the computed spend / max(coverage_delta, 1).
	SpendUSD             int     `json:"spend_usd,omitempty"`
	CostPerCoveragePoint float64 `json:"cost_per_coverage_point,omitempty"`
}

// Finding is one open finding in the pack's open_findings section. For v1 a
// finding is a failing control evaluation as of period_end (decision D4) —
// there is no separate findings table, the same semantics as slice 030.
type Finding struct {
	// EvaluationID is the control_evaluations row id.
	EvaluationID string `json:"evaluation_id"`
	// ControlID is the failing control's UUID.
	ControlID string `json:"control_id"`
	// ScopeCellID is the scope cell the evaluation was for (may be the
	// nil UUID for a whole-tenant evaluation).
	ScopeCellID string `json:"scope_cell_id"`
	// EvaluatedAt is when the failing evaluation was computed (RFC3339).
	EvaluatedAt string `json:"evaluated_at"`
	// FreshnessStatus is the evidence freshness at evaluation time.
	FreshnessStatus string `json:"freshness_status"`
}

// StoredPack is a quarterly pack as it lives in the `board_packs` table —
// the Pack plus its row identity and lifecycle metadata. Returned by the
// Store's methods.
type StoredPack struct {
	ID          uuid.UUID
	PeriodEnd   string // YYYY-MM-DD
	Status      string // "draft" | "published"
	Content     Pack
	NarrativeMd string
	PublishedBy string // "" while draft
	PublishedAt time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// IsPublished reports whether the stored pack is frozen.
func (sp StoredPack) IsPublished() bool {
	return sp.Status == PackStatusPublished
}

// allSectionsApproved reports whether every fixed section is approved — the
// publish gate (decision D6). Returns the first unapproved section title for
// a precise error message.
func allSectionsApproved(p Pack) (string, bool) {
	for _, key := range SectionKeys {
		sec, ok := p.Sections[key]
		if !ok || !sec.Approved {
			return sectionTitles[key], false
		}
	}
	return "", true
}
