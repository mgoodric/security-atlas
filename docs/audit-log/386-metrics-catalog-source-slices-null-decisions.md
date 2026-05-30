# Slice 386 — decisions log

## Decisions made

- **D1: Coerce nil→non-nil in Go (`nonNilStrings`), not change the schema or the
  YAML.** The column is correctly NOT NULL with an empty-array default; the bug
  is that the Go seed passed an explicit NULL. Fixing at the param-construction
  site is the minimal, correct change. Editing the three YAML metrics to add a
  dummy `source_slices` value would be wrong (they genuinely have no source
  slices) and would not fix the latent nil-encoding hazard for future metrics.
  Confidence: high.
- **D2: Extract a named helper for unit testability.** The inline expression
  could only be regression-tested via the DB integration path, which `t.Skip`s
  in CI without the migrator DSN — exactly why the bug slipped through. A pure
  helper gets a guard that runs in the always-on unit job. Confidence: high.
- **D3: Did not make the startup seed failure fatal in this PR.** That is a
  separate operational-policy decision (fail-fast boot vs. degrade-and-log);
  bundling it would widen this hotfix. Filed as out-of-scope. Confidence: high.

## Revisit once in use

- After deploy, confirm `metrics_catalog` seeds all metrics on `atlas-edge` and
  the scheduler's `metric_observations` FK violations clear.
- The evaluator query drift (`framework_id`/`policy_versions`) is a separate
  live failure still to fix.

## Confidence

High. The fix matches the exact SQLSTATE 23502 observed on atlas-edge, and the
non-nil-slice encoding behavior is well understood.
