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
the demo-seed posture test call. Two columns are added to it:

- `scope_activated` — whether the tenant has an activated framework-scope
  revision for the version. #2114 needs the same flag for the dashboard tile,
  so it lives in the shared query, not in a board-only read.
- `framework_version_id` — the key of the framework-scope page a "Scope not
  set" tile links to. It is 1:1 with the existing `(framework_id,
framework_version)` group, so the grouping does not change.

The dashboard handler's wire shape (`postureWire`) is unchanged; it ignores
both columns until #2114 uses them.

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
is a follow-up, not done here.

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
