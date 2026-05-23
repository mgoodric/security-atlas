# Slice 204 — Per-page audit: /board-packs

- **Live URL audited**: https://atlas-edge.home.gmoney.sh/board-packs
- **Mockup**: Plans/mockups/board-pack.html
- **Audit date**: 2026-05-23
- **Auditor**: slice 204 fleet agent (board-pack-page)
- **Live page status**: 200
- **Findings count**: 5

## Audit context

The mockup `Plans/mockups/board-pack.html` titled "Q1 2026 Board Pack" is a
DETAIL view, not a list. The live `/board-packs` route is the LIST view
(`web/app/(authed)/board-packs/page.tsx`); the corresponding detail view
lives at `/board-packs/[id]` (`web/app/(authed)/board-packs/[id]/page.tsx`).
Both were exercised:

- Live list: HTTP 200, empty state ("No board packs yet. Generate one above.")
  rendered correctly until a pack was created via `POST /v1/board-packs`.
- Live detail: HTTP 200, rendered the slice-043 mockup-faithful composition
  (PackHeader, PostureTiles, TopRisksTable, CoverageTrend, FindingsList,
  OperationalTiles, InvestmentPanel, asks textarea, PublishFooter).

The slice-043 author deliberately substituted the mockup's violet
"AI-drafted · llama3.1-8b · approved" badges with a slate "Templated v1"
badge (see `web/components/board-pack/templated-badge.tsx` and slice-043
decision D1 in `docs/audit-log/032-quarterly-board-pack-decisions.md`).
That substitution is HONEST and aligned with the CLAUDE.md AI-assist
boundary — labeling content "AI-drafted" when no model ran would itself
violate the boundary. **Not filed as a finding.**

Findings below are real divergences where either the mockup overpromises
data the v1 implementation does not have, or where the mockup contains
content the v1 implementation never renders, or where the live page
hardcodes a data-bound field to a placeholder.

## Findings

| #   | Category          | Severity guess | Spillover slice | Brief                                                                                                                                                                                                             |
| --- | ----------------- | -------------- | --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | (i) layout        | low            | #218            | Mockup breadcrumb chain ("Sentinel Labs · Board reports · Q1 2026") missing on live detail-page top bar (ExportBar shows only "← All packs")                                                                      |
| 2   | (iii) data-bound  | medium         | #219            | PackHeader "Author" meta-cell hardcoded to "—" — board-pack records carry no `author` field, but the mockup label promises one ("Sam Rivera (CISO)")                                                              |
| 3   | (iv) mockup-stale | low            | #220            | CoverageTrend visual on live is 3 scalar cards (Baseline / Current / Quarter delta); mockup ships a multi-line per-framework time-series chart with 90-day deltas — server has no per-framework time series in v1 |
| 4   | (iv) mockup-stale | medium         | #221            | Section divergence: mockup ships §06 "Vendor risk burndown" but live `BOARD_PACK_SECTION_KEYS` has no such section; conversely live ships "Open findings" which the mockup omits                                  |
| 5   | (iv) mockup-stale | low            | #222            | Mockup §01 trailing "Coverage definition: weighted SCF-anchored evidence pass rate intersected with each framework's scope predicate" methodology disclosure never renders on live page                           |

## Detailed findings

### Finding 1 — #218 (layout, low)

The mockup top bar (`Plans/mockups/board-pack.html` lines 27–33) renders a
three-segment breadcrumb chain: `Sentinel Labs › Board reports › Q1 2026`.
The live `ExportBar` (`web/components/board-pack/export-bar.tsx`) renders
only a single back-link (`← All packs`). The breadcrumb chain is the
mockup's primary location signal — on the detail page the user has no
chrome cue beyond the page title that they are inside a board pack.
Adding a minimal breadcrumb (`Board packs › <period_label>`) closes the
chrome parity gap.

### Finding 2 — #219 (data-bound, medium)

`web/components/board-pack/pack-header.tsx` line 62 hardcodes
`<MetaCell label="Author" value="—" />`. The backend pack record
(`internal/board/pack.go`) has no `author` field — there is no way for the
live page to populate that cell honestly. The mockup populates it with
`Sam Rivera (CISO)`, which makes the cell a promise the v1 implementation
can never keep. Either remove the cell entirely (3-cell meta strip instead
of 4), or wire it to the session identity that posted the generate
request (recording author at generate-time is a small, additive backend
change).

### Finding 3 — #220 (mockup-stale, low)

`web/components/board-pack/coverage-trend.tsx` documents the deliberate
downgrade: the server's `coverage_trend` section carries scalar
`baseline_coverage_pct` / `coverage_pct` / `coverage_delta` only, with no
per-framework time series. The mockup §03 ships an SVG line chart with
three framework lines over six tick marks plus 90-day per-framework delta
cards. That richer visual cannot be rendered from v1 data without
fabricating it (anti-pattern). Either upgrade the mockup to depict the
v1 scalar reality, OR add a separate coverage-history series to the
backend in a follow-on slice and ship the chart against it.

### Finding 4 — #221 (mockup-stale, medium)

The mockup has seven sections in this order: posture, top risks, coverage
trend, **operational metrics**, **investment vs coverage**, **vendor risk
burndown**, asks. The live implementation
(`web/lib/api.ts:1707 BOARD_PACK_SECTION_KEYS`) has seven sections in this
order: posture, top_risks, coverage_trend, **open_findings**,
operational_metrics, investment, asks. So:

- Live has `open_findings`; mockup does not.
- Mockup has `vendor_burndown`; live does not.

Either the mockup is stale (drop "Vendor risk burndown"; add "Open
findings") or the implementation should grow a vendor-burndown section
(the data is computable from the vendor module). Pick a direction and
align both.

### Finding 5 — #222 (mockup-stale, low)

Mockup §01 closes with a footer paragraph (`Plans/mockups/board-pack.html`
line 146-148) explaining the coverage methodology: "Coverage definition:
weighted SCF-anchored evidence pass rate intersected with each framework's
scope predicate, over the period. Methodology unchanged from prior
quarter." This methodology disclosure has audit-trail value (board
members and auditors both benefit from a one-line definition). The live
posture section renders no equivalent caption. Add the methodology
sentence to the posture-section footer or to a per-tile tooltip.

## Out-of-scope

- The "AI-drafted · llama3.1-8b · approved" mockup badges are deliberately
  rendered as "Templated v1" on the live page per slice-043 decision D1.
  This is a constitutionally-required honesty downgrade, not a finding.
- The 64% draft-complete badge in the mockup is rendered honestly on the
  live page from `approvedCount / totalSections`. Not a finding.
- The mockup's hardcoded "Sentinel Labs" tenant name is mockup-illustrative.
  Not a finding.
