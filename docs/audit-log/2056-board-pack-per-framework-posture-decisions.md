# #2056 — board pack posture is the dashboard's, per framework — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2056
**Shape:** `PackGenerator` reads the dashboard's `FrameworkPosture` query
(`internal/db/queries/dashboard.sql`) through `PackStore.FrameworkPosture`,
instead of the slice-031 tenant-wide `programPosture` over the freshness read
model. `NewPackGenerator` loses its freshness and drift arguments; nothing in
the pack reads them now.

detection_tier_actual: manual_review
detection_tier_target: integration

## D1 — one query, extended rather than copied

The pack calls the same sqlc query the dashboard, the dashboard export and
the demo-seed posture test call. Three columns are added to it:

- `scope_activated` — whether the tenant has an activated framework-scope
  revision for the version. #2114 needs the same flag for the dashboard tile,
  so it lives in the shared query, not in a board-only read.
- `framework_version_id` — the key of the framework-scope page a "Scope not
  set" tile links to. It is 1:1 with the existing `(framework_id,
framework_version)` group, so the grouping does not change.
- `trend_history` — whether any control covering the version has an
  evaluation (pass or fail) on or before the 90-day cutoff (D8).

The dashboard handler's wire shape (`postureWire`) is unchanged; it ignores
these columns until #2114 (and a dashboard follow-up for D8) use them.

## D2 — "Scope not set" (#2071 decision B3)

A framework version with no activated scope gets `scope_not_set: true`,
`state: "scope-not-set"`, and zero numbers. The renderers (narrative, PDF,
web tile) show "Scope not set" and never print the zeros. The numbers are
zero rather than absent so that `PackFrameworkPosture` keeps the slice-032
field types and old stored packs still decode.

## D3 — a pack-only posture type

`SectionData.Frameworks` moves from the brief's `FrameworkPosture` to a new
`PackFrameworkPosture` with float percentages (the query returns two
decimals; the dashboard tile shows one). The JSON names already stored in
`board_packs.content` are unchanged. The monthly brief keeps
`FrameworkPosture` and its tenant-wide formula: the board-narrative
numeric-verification gate (`internal/boardnarrative`) reads the brief's
integers, and the ticket names the pack. Moving the brief onto the same query
is the follow-up https://github.com/mgoodric/security-atlas/issues/2210.

Until then the brief names its figure for what it is (review N1): "evidence
freshness rate (tenant-wide)" in the narrative and the PDF, and "controls
with fresh evidence ... (tenant-wide)" for the second number. It no longer
says "coverage", so it cannot be read as the pack's or the dashboard's
posture.

## D4 — audit-ready per framework, never rounded up

`State` is `stateFromCoverage(int(coverage))`. Truncation is floor for a
non-negative percentage, so 89.99 stays `in-progress`.

## D5 — coverage trend: the scoped frameworks' mean, baseline from the trend

The `coverage_trend` section still carries one figure (the investment
section's cost-per-point divides by its delta). It is now the unweighted mean
of the scoped frameworks' coverage, and the baseline is the mean of their
coverage at the start of the 90-day trend (`coverage - trend_delta_90d`).
Unscoped frameworks are left out. The narrative says "averages ... across the
N frameworks with an activated scope", so it cannot be read as a separate
posture formula. An operator can still overwrite the baseline (decision D5 of
slice 032); the delta is recomputed as before. The three scalars become
floats.

Unweighted rather than requirement-weighted: the query does not return
requirement counts, and a mean of the per-framework numbers the board sees one
section earlier is the reading a board member would check.

## D6 — residuals

#2163 already moved the register to the 1..25 scale and the board pack web
table to the band beside the number. The generator's `extractSeverity` reads
the same fields in the same order as `risk.residualMagnitude` and the web's
`residualSeverity`. This change adds the register's band (High from 15,
Medium from 8, Low from 1) to the narrative ("20.0 of 25, High") and the PDF
table, and pins the value and band in a unit test.

## D7 — the posture caption

The web caption said "weighted SCF-anchored evidence pass rate intersected
with each framework's scope predicate", copied from the archived mockup. The
platform never computed that. The caption now states the query's definition,
and its unit test pins the new string.

## D8 — no evaluation history before the window: no baseline (review B1)

`covered_then` needs a covering control whose latest evaluation on or before
the cutoff passed. A tenant whose controls were first evaluated inside the
last 90 days has none, so `trend_delta_90d` equals `coverage_pct` and the
baseline reads 0% — #2056's own symptom, reached a second way.

The query now returns `trend_history`, true when any control covering the
version has an evaluation of either result on or before the cutoff. Without
it:

- the framework row carries `no_trend_history`, no baseline, no delta and a
  flat arrow; its coverage and audit-ready state are unchanged;
- the coverage-trend section carries `no_baseline` whenever any scoped
  framework lacks history (a mean of real and missing baselines would
  understate the start), and reports no change;
- investment mirrors `no_baseline`, and the cost per coverage point is n/a;
- every renderer (narrative, PDF, web tile, trend card, investment panel)
  says "no baseline yet: less than 90 days of evaluation history" or "n/a".

An operator-entered `baseline_coverage_pct` clears `no_baseline` on the
section, as a prior-quarter figure is a real baseline.

Known quirk, left as it is: `covered_now` counts a requirement as covered when
a non-superseded control is _defined_ on its anchor, while `covered_then`
needs that control to have a _passing_ evaluation at the cutoff. A tenant
whose controls have always been defined but failing still shows a positive
delta. Changing either half changes the dashboard's number, which is outside
this ticket; the trend is labelled as the dashboard's, and the two now agree.

## D9 — investment with no scoped framework (review N2)

With no framework scoped there is no coverage for spend to buy. The
investment section is n/a (`no_baseline`, cost absent) whenever
`scoped_framework_count` is 0, even if the operator types a baseline. A pack
generated before #2056 carries no scoped count, so editing one of those
drafts also shows n/a; its old coverage figure was the formula this ticket
removes.

## D10 — a posture row whose framework is not in the tenant's list (review N3)

`ListFrameworksForTenant` and the posture query read the same RLS-scoped
tables, so a miss should not happen. If it does, the row is named by its
framework id and `PackStore.FrameworkPosture` logs a warning with the
framework id and version.
