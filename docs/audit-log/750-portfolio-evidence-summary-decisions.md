# Slice 750 — portfolio / multi-control evidence summary — decisions log

`Type: JUDGMENT`. Claude made the subjective build-time calls itself and recorded
them here; the maintainer iterates post-deployment from the "Revisit once in use"
list. This log does NOT block merge.

- detection_tier_actual: playwright
- detection_tier_target: playwright

(The load-bearing defect on this slice was a dashboard-invariant regression
caught at the **Playwright** tier — exactly where it should be. The first cut of
the dashboard panel client-fetched `/api/dashboard/portfolio-summary` on mount
via `useQuery`, which violated the slice-380 invariant that the FIRST dashboard
load fires ZERO `/api/dashboard/*` BFF requests (all dashboard data is prefetched
server-side). The existing `dashboard-server-component.spec.ts` AC-4/AC-8 spec
caught it in CI (failed on the initial run AND retry #1 — not a flake). The fix is
also the correct design (D5): the panel is now operator-triggered/on-demand, so it
fires nothing on first paint AND never runs an expensive LLM generation on a
passive dashboard view. A new Playwright assertion proves idle-on-mount +
fetch-on-click so the regression cannot recur. A separate, non-product
coverage-tier item also surfaced during the original build and was caught at the
integration tier: the first cut dropped both Go packages' merged coverage below
their ratchet floors (`internal/evidencesummary` 86, `internal/api/evidencesummary` 88) — fixed in the same change by adding the missing integration + unit tests, no
floor lowered (P0-347-1). No product/constitutional bug surfaced: two-level bound
(AC-1), citation gate (AC-2), numeric-claim suppression (AC-3), cross-tenant
isolation across the whole set (AC-4) all passed first run against real Postgres +
RLS via the slice-498 `StubClient` CI seam.)

## Context

Slice 750 is the cross-control **generalization** of slice 502 (the single-control
live evidence summary) — the portfolio sibling of slice 749's frozen-population
sibling. Where 502 answers "what does the evidence for THIS control show", 750
answers "what does my evidence collectively show across this framework / this
control-family / my whole program right now". It was deliberately built as a thin
variant of 502: it reuses 502's entire constitutional contract verbatim (the
shared `runSummary`-style pipeline, the validate-every-citation-then-suppress
gate, graceful degradation, never-persisted, no approve/publish/export,
local-default routing, current-live-evidence-only) and changes only (a) the corpus
(a two-level bounded SET of controls, not one), (b) the mount (dashboard, not
control-detail), and (c) ONE added gate the single-control surface does not need —
portfolio numeric-claim verification (AC-3).

## Decisions made

### D1 — The two-level corpus bound: 12 controls/summary × 4 records/control. `HIGH` (the headline JUDGMENT call)

**The problem.** A portfolio summary spanning a framework or a control-family can
match hundreds of controls, each with a long evidence history. N controls × M
records is the unbounded-corpus risk squared (threat-model D). The bound must keep
the prompt + the citable-id set bounded AND keep the summary meaningful at
portfolio scale (a bound so tight it summarizes 2 controls is useless; a bound so
loose it blows the token budget is the failure the slice doc warns about).

**Chosen.**

- `MaxControlsPerSummary = 12` (first level — cap controls).
- `MaxRecordsPerControl = 4` (second level — cap records per control).

**Why those numbers.**

- **Bounded prompt.** 12 controls × 4 records = 48 cited excerpts, + 12 control
  ids = **60 citable ids** maximum. At a few hundred bytes per excerpt line the
  whole context block is a few KB — well inside the headroom of `MaxSummaryTokens`
  (512, inherited from 502) and the local-Ollama default's comfortable input
  window. The citation gate iterates at most 60 ids, so it stays fast.
- **Meaningful summary.** 12 controls is enough to read as a _portfolio-level_
  picture for the v1 persona's typical framework/family slice (the solo security
  leader is not summarizing 400 controls in one paragraph — they want the shape of
  a dozen). 4 records/control is enough recency grounding to characterize each
  control's recent posture (pass/fail trend across the freshest few) without the
  per-control contribution dominating the corpus.
- **Asymmetry is deliberate.** `MaxRecordsPerControl` (4) is intentionally SMALLER
  than the single-control `MaxCitedExcerpts` (8): at portfolio scale records
  multiply by controls, so each control contributes fewer. The single-control
  surface can afford 8 because it has only one control.

**Ordering / relevance rule.** Controls are resolved deterministically (`bundle_id
ASC, id ASC` in `ListActiveControlsForPortfolio`) and the first
`MaxControlsPerSummary` enter the corpus — a STABLE subset, not a random one. The
store reads `MaxControlsPerSummary + 1` rows so `TotalControls` is honest (the "K
of N" label reflects whether the filter matched MORE than the cap) without a
second COUNT round-trip. Records are ordered `observed_at DESC` (recency, mirroring
502). "Most-relevant" in v1 == "most-recent within a deterministically-ordered
control set"; a smarter relevance ranking (coverage-gap-weighted, freshness-
weighted) is a documented follow-on (see Revisit #2). Both bounds are stated
honestly in the prompt AND the UI ("summarizing the K most-relevant of N controls;
up to M records each").

### D2 — Numeric-claim verification: lift the slice-501 pattern LOCALLY, don't import `boardnarrative`. `MEDIUM`

**Options.** (a) Import `internal/boardnarrative.VerifyNumbers` (exported,
`func(text string, allowed map[int]bool, periodEnd string) bool`). (b) Lift the
small scan pattern into a local `verifyPortfolioNumbers` in `internal/evidencesummary`.

**Chosen: (b).** The brief flagged this as a judgment call ("cleaner, avoids
evidencesummary→boardnarrative coupling"). The numeric scan is ~10 lines (regex
integer extraction + allowed-set membership + a single-miss-fails-the-draft rule).
Importing `boardnarrative` would couple a low-risk comprehension-aid package to the
highest-risk board-narrative package for a tiny shared helper, and the
board-narrative `VerifyNumbers` carries machinery this surface does not need (a
period-end label to strip, a numbered-section list-marker strip). The portfolio
summary has no period-end label and no section template, so the only shared concern
is the **UUID strip** (citation ids must not be read as statistics) plus the
integer-overflow-sentinel discipline (slice-501 / slice-508 lesson: never silently
narrow an unbounded parse). I lifted exactly that and nothing more. The PATTERN is
reused (identical contract: a single fabricated count fails the whole draft); the
CODE is local. If a third AI-assist surface ever needs the same scan, that is the
moment to extract a genuinely shared `internal/llm/numeric` helper — not now (rule
of three).

**Boundary documented in tests.** The numeric gate checks number _membership_ in
the deterministic rollup's allowed set, not the semantic pairing. "5 of 5 controls"
where the rollup says TotalMatched=5 passes the NUMERIC gate (5 is a real rollup
number); the lie there ("covered") is a _coverage_ claim caught by the
citation/grounding discipline, not a numeric one. A genuinely fabricated count
("40 of 40" when the rollup is 1, or "9 controls" when no rollup number is 9)
auto-suppresses. The unit test asserts both arms so the boundary is not mistaken
for a bug later.

### D3 — Dashboard mount + control-set filter (family + framework; scope deferred). `MEDIUM`

**Mount.** The portfolio summary is the program-level "what does my evidence show"
question, so it mounts on the **dashboard** (`web/app/(authed)/dashboard/page.tsx`)
as a panel below the deterministic panels, behind the existing program/control-read
authz (admin / grc_engineer / control_owner — the SAME role set the dashboard and
the single-control summary already use; **no new role**). The panel degrades
gracefully (its own TanStack Query; a slow/failed summary shows a neutral note and
never blocks the rest of the dashboard — AC-7).

**Filter.** AC-1 wants a set filtered "by framework, scope cell, OR control-family"
(the OR — any one mode satisfies the AC). I shipped:

- **control-family** — a direct `control_family =` column filter (deterministic,
  zero new mechanism).
- **framework** — resolved via the EXISTING framework→anchor→control path:
  `ListSCFAnchorsForVersion` (slice 006) gives the framework version's SCF anchor
  ids, fed to a `scf_anchor_id = ANY(...)` clause. This reuses the UCF traversal
  rather than inventing a control-by-framework mechanism (the brief's instruction).
- **whole program** (no filter) — the default the dashboard panel uses.

I added exactly ONE new sqlc query (`ListActiveControlsForPortfolio`) — a control-set
resolver with optional `family` + optional `anchor_ids[]` via `sqlc.narg` (the
established nullable-param convention), capped at `MaxControlsPerSummary + 1`. The
per-control evidence reads REUSE the slice-502 `ListEvidenceRecordsByControl` /
`CountEvidenceRecordsByControl` (no new evidence query). **No migration** — pure
reads over existing tables.

**Scope-cell filter DEFERRED.** Scope is multidimensional (canvas invariant #4/#5):
`effective_scope = applicability_expr ∩ framework_scope.predicate`. That intersection
is genuinely heavier graph/DSL work and was out of proportion for this slice's
"add the cross-control rollup shape" core. AC-1's OR is satisfied by family +
framework. The scope-cell filter is a documented follow-on (Revisit #1), NOT a
silent omission — the UI/prompt label honestly states the active filter mode.

### D4 — Extend `internal/evidencesummary` (sibling Service + Store), don't spin a new package. `MEDIUM`

Mirrors slice 749's D1. Added `portfolio.go` (`PortfolioService` + types + the
numeric gate) and `portfoliostore.go` (`PortfolioStore`, whose embedded
single-control `*Store` IS the citation resolver — a portfolio cited id resolves
exactly as a single-control one; the cross-control grounding gate over
`portfolioAllowedIDs` is what scopes citations to the summarized set). The shared
suppression vocabulary, citation gate (`validateCitations`), UUID parsing, and
`allowedIDs`-style grounding are reused verbatim. The ONLY genuinely new logic is
the two-level corpus assembly + the numeric gate.

### D5 — The dashboard panel is OPERATOR-TRIGGERED / on-demand, not auto-fired. `HIGH` (fix-forward after CI)

**The problem (caught by CI).** The first cut of `portfolio-summary-panel.tsx`
mounted a `useQuery` that client-fetched `/api/dashboard/portfolio-summary` on
mount. This failed `dashboard-server-component.spec.ts` (the slice-380 AC-4/AC-8
spec) — the dashboard's load-bearing invariant is that the FIRST load fires ZERO
`/api/dashboard/*` BFF requests, because every dashboard panel's data is
prefetched server-side in the sibling Server Component and shipped via
`HydrationBoundary`. An auto-firing client query violates that contract.

**Options.** (a) Prefetch the portfolio summary server-side like the other six
panels (restores the zero-BFF invariant). (b) Make the panel operator-triggered —
no fetch on mount; fetch only when the operator clicks "Generate summary".

**Chosen: (b).** Prefetching (a) would technically satisfy the spec but is the
WRONG design: a portfolio summary is an **expensive local-LLM generation**, and
prefetching it server-side would run an LLM call on EVERY dashboard load (every
SSR render of the home screen), for a comprehension aid the operator may never
look at. That is exactly the "continuous monitoring that's actually 24-hour
polling" anti-pattern energy applied to inference cost. Operator-triggered (b)
simultaneously: (1) restores the slice-380 zero-BFF-on-first-load invariant (the
query is `enabled: triggered`, so nothing fires on mount), and (2) runs the LLM
only when the operator explicitly asks for it. The panel renders an idle state
with a "Generate summary" button; clicking it sets `triggered=true` and the query
fires once. All the AC-5 work (non-binding disclosure, BOTH-bound labels, no
approve/export, graceful degradation) is preserved on the rendered result.

**Regression guard.** `dashboard-server-component.spec.ts` gains (i) an assertion
in the existing zero-BFF test that the portfolio panel renders its idle state and
fires nothing on first paint, and (ii) a new test proving the BFF request fires
ONLY after the trigger click. The detection-tier classification above is set to
`playwright`/`playwright` accordingly.

## Inherited 502/749 calls (re-affirmed, unchanged)

- **No fabricated coverage or counts** — strict citation gate over the LARGER
  cross-control citable-id set + numeric-claim verification; a single failure
  suppresses the whole summary and the deterministic rollup renders alone.
- **Cross-tenant isolation across the WHOLE set** — corpus assembly + citation
  resolution run under `app.current_tenant` in one transaction; a Tenant-B summary
  cannot cite/quote ANY Tenant-A control or evidence record (proven by
  `TestPortfolio_CrossTenantIsolation` against real RLS, with overlapping family
  structure across two tenants).
- **Non-binding + read-only** — no approve/publish/export anywhere (asserted in
  the handler + view-model tests); never persisted (no `ai_generations` row).
- **Current live evidence only** — labeled `live_only`; no frozen-population mixing
  (a period-scoped portfolio summary = #749 ∩ #750 is a further follow-on).
- **Local-Ollama default** — rides the slice-499 per-tenant inference client; the
  cloud-routing banner is inherited by the dashboard panel for free.

## Revisit once in use

1. **Scope-cell portfolio filter.** Add the `applicability_expr ∩
framework_scope.predicate` intersection as a third filter dimension once the
   PCI/HIPAA scope-ownership UX lands. Captured as the spillover slice (see below).
2. **Richer relevance ranking.** "Most-relevant of N controls" is currently
   "most-recent within a deterministic order". A coverage-gap-weighted or
   freshness-weighted ranking would make the bounded subset more useful when a
   framework matches far more than 12 controls. Captured as the spillover slice.
3. **Period-scoped portfolio summary (#749 ∩ #750).** A frozen-audit-period
   portfolio rollup — bounded cross-control corpus drawn ONLY from a frozen sample
   population. A further follow-on; not built here (out of scope per the slice doc).
4. **Bound tuning.** Re-evaluate 12×4 once real operators use it: if a framework
   routinely matches 30+ controls, a "show more" pagination or a larger cap behind
   a per-tenant config may read better. The numbers are a starting point, not a
   constitutional commitment.
