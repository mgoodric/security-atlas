# 3087 — POA&M items from replay runs carry the judged instant: decisions log

Issue: mgoodric/security-atlas#3087 (follow-up from #3071 / PR #3086, its D4).

## Detection-tier classification

- **detection_tier_actual:** manual_review. #3071's decisions log recorded
  that `ListFailingEvaluationsAsOf` does not select `evaluated_as_of`. No
  production code calls `Engine.Replay` yet, so nothing shipped was wrong.
- **detection_tier_target:** integration
  (`TestExport_ReplayPOAMItemCarriesJudgedInstant`, observed failing against
  main's `poamInput` on the replay row only, then passing), plus a unit case
  (`TestPOAMInput_ReplayRowCountsFromJudgedInstant`) and a bridge test
  (`test_serialize_poam_names_a_replay_items_judged_instant`).

## Decisions

### D1 — the judged instant replaces only the evaluated_at fallback

The due date already prefers `last_observed_at`, then `evaluated_at`, then
now. The judged instant `COALESCE(evaluated_as_of, evaluated_at)` replaces
the `evaluated_at` step; `last_observed_at` keeps precedence. A replay of T
reads evidence observed at or before T, so `last_observed_at` is already
bounded by the judged instant and stays the more specific base. For a live
row `evaluated_as_of` is NULL, so every branch is unchanged.

### D2 — the SSP carries nothing to change

The ticket asked for SSP results too. The SSP's `ControlImplementation` has
`evaluation_result` and `evaluated_at` fields, but `sspInput` never fills
them (`EvaluationResult: ""`), and `failingEvals` feeds only the POA&M. There
is no SSP result taken from a replay row to annotate, so no SSP field was
added. Populating the SSP's per-control result is separate work; when it
lands, it reads the same `evaluated_as_of` column.

### D3 — additive proto field, mirrors ExceptedCell

`PoamItem.evaluated_as_of = 9`, RFC-3339, empty for a live row and for any
item that is not from an evaluation. The bridge emits `evaluated-as-of` only
when set, the same rule as `ExceptedCell` (#3071).
