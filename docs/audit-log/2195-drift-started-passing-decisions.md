# Issue #2195 — Board brief, narrative and export show started-passing

The board brief, the board-narrative rollup and the dashboard export paired
`delta` (net change in passing controls, earliest vs latest snapshot) with only
`flipped_out_count`, so "+33 / 0 flipped out" read as a contradiction, the same
one #2063 fixed on the dashboard panel. All three now carry the started-passing
count from `drift.DriftReport.StartedPassing` (#2188) and name `delta` as a net
change. Gains minus losses is not `delta` in general (a control can fail and
recover inside the window), so no output derives one from the others.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

## D1 — A pointer on the stored brief, so legacy briefs stay honest

**Decision.** `board.DriftSummary.StartedPassingCount` is `*int` with
`json:"started_passing_count,omitempty"`. A brief or pack stored before #2195
decodes with nil, and renders the old loss-only clause ("1 control(s) drifted
out of passing") instead of claiming "0 started passing". A new brief always
stores the count, zero included.

**Why.** An `int` with omitempty cannot tell "no gains" from "not recorded",
and a stored brief is immutable, so the renderer has to know which it has.

## D2 — The narrative gate admits the count only when the brief has one

**Decision.** `Rollup.StartedPassingCount` mirrors the brief's pointer. The
coverage and drift-activity sections add it to `AllowedNumbers` when non-nil.
For a legacy brief the prompt omits the `controls_started_passing` line and the
gate admits no gains number. Nothing else in the allowed sets changed; gains
minus losses is not added.

**Why.** The gate checks integers against the brief's ground truth (#2190 D3,
#2210). A new ground-truth integer joins the set; a number the brief never
recorded stays rejected.

## D3 — Prompt versions bump to v1

**Decision.** `boardnarrative-coverage-v0` and `boardnarrative-drift-v0` become
`-v1`. The rollup line `drift_delta_30d` is now
`net_change_in_passing_controls_30d`, `controls_started_passing` is added, and
each section template asks for the started-passing count beside drifted-out.
The `ai_generations` context inputs keep their `drift_delta_30d` key and add
`controls_started_passing`.

**Why.** `promptVersion`'s contract is to bump on any material change to the
prompt, rollup shape or template. The audit-row keys stay stable for readers
of existing rows.

## D4 — CSV/xlsx drift table gains a trailing `change` column

**Decision.** The drift sheet lists started-passing and stopped-passing
controls, each row tagged `started_passing` / `stopped_passing` in a new last
column. The window sentinel row reads
`net_change_in_passing=… started_passing_count=… flipped_out_count=…` (was
`delta=… flipped_out_count=…`). The JSON panel keeps `delta` and adds
`started_passing_count` plus the `started_passing` list, as
`/v1/controls/drift` does.

**Why.** Appending the column keeps the existing column positions for anyone
reading the sheet by index. The export is computed live, so there is no stored
legacy shape to decode.
