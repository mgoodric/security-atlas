// Package board is the MONTHLY BOARD BRIEF generator of security-atlas
// (slice 031). It assembles a single-page, board-ready posture snapshot —
// per-framework posture, drift in the last 30 days, top-3 risks aging — and
// persists it as a PINNED, IMMUTABLE snapshot (canvas §7.5).
//
// The defining property is immutability. A board brief is a FROZEN snapshot:
// the board reads what posture WAS at the report date even if live state
// changes afterward. Two things enforce this:
//
//   - The `board_briefs` table is append-only by construction (slice 031
//     migration `_031`: SELECT + INSERT RLS policies only, no UPDATE/DELETE
//     under FORCE ROW LEVEL SECURITY). There is no SQL path to mutate a brief
//     row.
//   - The structured metrics are serialized into `content` (JSONB) and the
//     rendered narrative into `narrative_md` (TEXT) at generation time. Every
//     read returns those frozen columns verbatim — AC-5.
//
// The narrative is TEMPLATED — Go `text/template` over the structured Brief
// (see narrative.go). There is NO LLM in v1 (AC-6, P0 anti-criterion): this
// package imports no inference client, opens no network connection, and has
// no Ollama / cloud-LLM path. The CLAUDE.md AI-assist boundary is honored by
// the absence of any AI path, not by a flag.
//
// Constitutional invariants honored:
//
//   - Invariant 6 (tenant isolation): the Store opens a transaction, applies
//     the tenant GUC via internal/tenancy, and runs every query inside it so
//     RLS is enforced. Cross-tenant brief reads return ErrNotFound.
//   - Invariant 2 (separated stages): the Generator is a pure READER of the
//     slice-012 / slice-016 read models (control_drift_snapshots,
//     evidence_freshness) plus the risks + frameworks tables. Its only write
//     target is `board_briefs` — it never writes a ledger.
package board

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrBadPeriodEnd is returned (wrapped) when a period_end string is not a
// valid YYYY-MM-DD date. The HTTP layer maps it to 400.
var ErrBadPeriodEnd = errors.New("board: period_end must be a YYYY-MM-DD date")

// DriftWindow is the lookback window for the brief's "recent drift" section.
// Canvas §7.5: the monthly brief reports "drift in the last 30 days".
const DriftWindow = 30 * 24 * time.Hour

// TopRisksCount is how many risks the brief's "top-3 risks aging" section
// surfaces. Canvas §7.5: "single page, posture + drift + top-3 risks".
const TopRisksCount = 3

// Brief is the structured monthly board brief — the shape that is frozen
// into `board_briefs.content` (JSONB) at generation time and read back
// verbatim on every fetch (AC-5). Every field is JSON-tagged so the frozen
// content is self-describing.
type Brief struct {
	// PeriodEnd is the report date the brief is pinned to (YYYY-MM-DD).
	PeriodEnd string `json:"period_end"`
	// GeneratedAt is the wall-clock generation time (RFC3339).
	GeneratedAt string `json:"generated_at"`
	// Frameworks is the per-framework posture summary — one row per
	// registered framework the program runs against (AC-2).
	Frameworks []FrameworkPosture `json:"frameworks"`
	// Drift is the 30-day control-drift summary (AC-2).
	Drift DriftSummary `json:"drift"`
	// TopRisks is the top-3 risks aging, ranked by residual severity then
	// age (AC-2).
	TopRisks []RiskAging `json:"top_risks"`
}

// FrameworkPosture is one framework's posture row in the brief.
//
// v1 scope (decisions log D3): true per-framework control attribution
// requires the SCF anchor graph + framework-scope intersection — heavyweight.
// v1 reports the program's tenant-wide posture numbers (coverage / freshness
// / drift) listed against each registered framework. The brief is honest
// about this: it states the program posture and names every framework the
// program runs against.
type FrameworkPosture struct {
	// Slug is the framework slug (e.g. "soc2").
	Slug string `json:"slug"`
	// Name is the human framework name (e.g. "SOC 2").
	Name string `json:"name"`
	// CoveragePct is the program control-coverage percentage [0,100]:
	// controls passing today / controls with evidence, rounded.
	CoveragePct int `json:"coverage_pct"`
	// FreshnessPct is the program evidence-freshness percentage [0,100]:
	// fresh controls / total controls in the freshness read model, rounded.
	FreshnessPct int `json:"freshness_pct"`
	// TrendArrow is the 30-day coverage-trend glyph: "up" | "down" | "flat".
	TrendArrow string `json:"trend_arrow"`
	// Delta is the signed 30-day drift count (controls_passing latest minus
	// earliest in the window).
	Delta int `json:"delta"`
	// State is the posture label: "audit-ready" | "in-progress" | "at-risk".
	State string `json:"state"`
}

// DriftSummary is the brief's 30-day control-drift section.
type DriftSummary struct {
	// WindowDays is the lookback window in days (30 for the monthly brief).
	WindowDays int `json:"window_days"`
	// Since / Through bound the window (YYYY-MM-DD).
	Since   string `json:"since"`
	Through string `json:"through"`
	// Delta is the signed drift count over the window.
	Delta int `json:"delta"`
	// FlippedOutCount is how many controls drifted OUT of passing in the
	// window.
	FlippedOutCount int `json:"flipped_out_count"`
}

// RiskAging is one risk in the brief's "top-3 risks aging" section.
type RiskAging struct {
	// ID is the risk's UUID.
	ID string `json:"id"`
	// Title is the risk title.
	Title string `json:"title"`
	// Category is the risk category.
	Category string `json:"category"`
	// Treatment is the current treatment status.
	Treatment string `json:"treatment"`
	// ResidualSeverity is the residual-severity scalar used for ranking.
	// Extracted from the risk's residual_score JSONB (falling back to
	// inherent_score) — see generator.go.
	ResidualSeverity float64 `json:"residual_severity"`
	// AgeDays is the age-since-treatment proxy: now - updated_at, in days
	// (decisions log D4 — risks have no treatment-applied timestamp).
	AgeDays int `json:"age_days"`
}

// StoredBrief is a frozen brief as it lives in the `board_briefs` table —
// the Brief plus its row identity. Returned by the Store's read methods.
type StoredBrief struct {
	ID          uuid.UUID
	PeriodEnd   string // YYYY-MM-DD
	GeneratedAt time.Time
	Content     Brief
	NarrativeMd string
}
