# #2210 — monthly brief reports the board pack's coverage — decisions log

**Ticket:** https://github.com/mgoodric/security-atlas/issues/2210
**Shape:** the monthly brief's `Generator` reads the dashboard's
`FrameworkPosture` query through `Store.FrameworkPosture`, the same read
`PackStore.FrameworkPosture` makes (both call `readFrameworkPosture`), and
reduces it with the pack's own `packPostures` and `coverageTrend`. The
slice-031 tenant-wide "coverage" (fresh controls with evidence / controls
with evidence) is gone. Follow-up to #2056 decision D3.

detection_tier_actual: none
detection_tier_target: integration

## D1 — one row type for brief and pack

`board.FrameworkPosture` is now an alias of `PackFrameworkPosture`. The brief
and the pack cannot drift apart field by field, and the integration test
compares their rows with `==`.

## D2 — the program figure is the pack's coverage_trend figure

`Brief.Coverage` (`CoverageSummary`) carries the unweighted mean over the
frameworks with an activated scope (#2056 D5), the scoped count, and the
tenant-wide evidence freshness rate. The brief does not carry the pack's
baseline: the ticket is about the coverage figure, and each framework row
already carries its own 90-day trend.

## D3 — the tenant-wide freshness figure stays, under its own name

The freshness read model's fresh / total stays as
`evidence_freshness_rate_pct`, rendered "Evidence freshness rate
(tenant-wide)". The coverage section's second sentence needs a freshness
number, and this one is honest under that label. It is never called coverage
in the narrative, the PDF, the rollup or the prompt.

## D4 — old briefs

A brief stored before #2210 has no `coverage` object. Its integer
`coverage_pct`, `freshness_pct` and `delta` decode into the float fields
unchanged; `Coverage == nil` selects the old template and PDF cards, which
render byte-for-byte as before (pinned by a test over a slice-031 JSON
fixture). The narrative surface never grounds on one: `RollupFromBrief`
returns `ErrNoBriefData` for a brief without `Coverage`.

## D5 — the gate takes the pack's rendering, and nothing near it

The pack prints coverage to one decimal (`formatPct`: 41.93 -> "41.9",
75 -> "75"). The gate was integer-only, so it would either reject the true
figure or admit a rounded one the pack never shows. The coverage section now
runs `verifyCoverageNumbers`, which adds two checks on top of the integer
set:

- a decimal token must equal `board.FormatPct(coverage)` exactly; "42",
  "41.8", "41.93" and "75.0" all fail;
- a number followed by `%` must be a percentage field (the coverage figure
  when it is a whole number, or the freshness rate), so a count with a "%"
  after it fails even when the count itself is allowed.

The tenant-wide coverage integer is no longer in `AllowedNumbers`. Other
sections keep `VerifyNumbers` unchanged.

Not caught: the model writing the freshness rate's value and calling it
coverage. The gate checks values, not which noun precedes them; the prompt
names the two figures apart.

## D6 — no scoped framework

With nothing scoped, the brief and the PDF say "No framework has an activated
scope, so no coverage figure is reported." The rollup drops the coverage
figure and the scoped count from the allowed sets, the prompt says the figure
is not reported, and a "0%" coverage claim fails the percentage check unless
the freshness rate is itself 0.

## D7 — frameworks without requirements drop out of the brief

The posture query lists current framework versions that have requirements.
A registered framework with no current version or no requirements was listed
by the slice-031 brief and is not now; the pack has behaved this way since
#2056. Two integration tests seeded such a framework and now seed a version
(and, for the API test, one requirement).

## D8 — prompt version

`boardnarrative-coverage-v1` -> `boardnarrative-coverage-v2`: the rollup's
fields, the prompt's figure lines and the section template's first two items
changed.
