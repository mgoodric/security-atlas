# 2095 — Charts and tiles: labelled axes, colour only against a target: decisions log

Issue: mgoodric/security-atlas#2095. Parent: #2070 (UI foundations).

This change adds a shared chart wrapper (`web/components/charts/axis-chart.tsx`,
with its math in `web/lib/charts/axes.ts`) and a single rule for colouring a
change (`web/lib/charts/delta-tone.ts`). The metric detail chart now draws
through the wrapper. Posture and metric tiles stop colouring values that have
no target to be measured against.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The flat, axis-less chart and the
  sign-coloured posture trend were reported by review, not by a test.
- **detection_tier_target:** playwright. `e2e/charts-and-tiles.spec.ts` now
  pins both behaviours.

## Decisions

### D1 — framework posture has no target, so posture tiles are always neutral

`FrameworkPostureRow` has no target field, and no table stores one. Only
metrics have targets (`metric_targets`). A posture tile therefore renders
coverage in the neutral foreground and runs its 90-day delta through
`deltaTone` with a null target, which is always `neutral`. The old 90/70
traffic-light cut-offs on coverage, and the green/red trend by sign, are
gone. "No target set" is plain text with no link, because posture targets
cannot be set anywhere yet; a link to the scope page would suggest they can.
Posture targets, and a UI to set metric targets (API-only today), are
**#2341**. When #2341 lands, the tile passes its target to the same
`deltaTone` and the colour follows.

### D2 — "toward the target" means "improving in the target's direction"

For `higher_is_better`, an increase is improving whether the value sits above
or below the target. For `lower_is_better`, a decrease is. For
`target_is_better`, the delta improves when it shrinks the distance to the
target. Treating "above target and still rising" as moving away from the
target would make a higher-is-better metric go red for getting better, which
is the same sign-only mistake the ticket removes. The full matrix
(above/below × improving/worsening × higher/lower) is in
`lib/charts/delta-tone.test.ts`.

### D3 — the "component test" is a pure-function test

`web/vitest.config.ts` is node-only, with no React rendering (Q-3 in
AGENTS.md). The colouring decision therefore lives in a pure module that
vitest covers in full. Playwright covers the rendered tiles.

### D4 — colour is never the only signal

Every toned delta carries an arrow (up, down or flat) and an `aria-label`
that names the verdict ("improving against target", "worsening against
target", "no target set"). Elements also carry `data-tone` so specs assert
the tone directly rather than through CSS classes.

### D5 — metric tiles show the 90-day change

A metric tile now shows the change from the oldest measured observation in
its 90-day window to the current value, toned by `deltaTone`. Percent values
stored as fractions (0-1) are reported in percentage points. A metric with no
target links "No target set" to `/dashboards/metrics/<id>#metric-detail-target`,
the detail page's Target card. That card is the only target surface until
#2341.

### D6 — axis layout

The y-axis is fitted with round steps (1, 2, 2.5 or 5 × 10^n) that cover the
data plus any threshold overlays. The x-axis has four evenly spaced ticks,
endpoints included, labelled with the UTC date ("Nov 3"), or the date and
hour when the span is under two days. Tick labels and axis titles sit in the
margins outside the plot, and the end x-labels anchor inward, so no label
overlaps a mark or the canvas edge. The chart grew from 200 to 220 px tall to
make room for the axes.

### D7 — a fraction-valued percent axis stays inside [0, 1]

`formatValue` reads a percent value of 1 or less as a fraction and anything
above 1 as already scaled. A flat series at 100% (`1.0`) was padded to
0.9–1.1, so its top ticks read "1.1%" above "100.0%" (review B1 on #2345).
`fractionBounds` now caps the fitted axis at [0, 1] when every value and
overlay of a percent or ratio series lies in that range. The axis stays on
one scale, and 100% is its top tick.
