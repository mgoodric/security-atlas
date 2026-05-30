# Fix-forward rate

> Slice 353 (Q-14 from slice 333's QA strategy audit). Companion to the
> [flake budget](flake-budget.md) and the
> [detection-tier classification](../CLAUDE.md#defect-detection-tier-classification).

## What this measures

For an OSS, self-hostable project with no SRE-owned production environment,
classic "defects leaked to production" is unmeasurable (slice 333 finding
Q-14). The canonical proxy the project already tracks informally — in
`docs/issues/_STATUS.md`'s `UNSTABLE` annotations and the per-batch records
in `MEMORY.md` — is **fix-forward rate**:

```
fix_forward_rate(quarter) = slices_merged_with_fix_forward / total_slices_merged
```

A slice "merged with fix-forward" is one whose merge required a follow-up
correction commit (an orchestrator-as-mechanic rescue, a `chore(status)`
reconcile-with-fix, a post-merge race-fix, a CI-delta amend after the
squash) — i.e. the four-surface gate let the slice merge but it was not
clean. This is distinct from a flake (a re-run-cleared transient on the
same SHA — that is the [flake budget](flake-budget.md)'s domain). A
fix-forward is a real follow-up commit with a real diff.

## Why a rate, not a count

The project's velocity varies wildly batch-to-batch (a single-slice focused
session vs. a 50-slice v2 drain). A raw count of fix-forwards is dominated
by throughput. The rate normalizes: it answers "of the slices we merged,
what fraction needed a correction?" — a quality signal independent of
volume.

## Targets (advisory)

| Quarter band | Fix-forward rate | Reading                                                             |
| ------------ | ---------------- | ------------------------------------------------------------------- |
| Green        | < 15%            | Healthy — the gate is doing its job; corrections are the exception. |
| Yellow       | 15–30%           | Drifting — review the detection-tier classification (Q-13) for a    |
|              |                  | recurring `actual=fix_forward` pattern; likely an enrolment gap.    |
| Red          | > 30%            | The gate is leaking a class of bug. File a QA-strategy re-audit     |
|              |                  | slice (the slice 333 cadence).                                      |

These bands are advisory, not a merge gate. They convert "are we merging
too much broken work?" from a vibe into a tracked number.

## Data source + refresh

- **Primary source:** `docs/issues/_STATUS.md` — every batch's reconcile
  block records each merged slice's clean/UNSTABLE status and any
  fix-forward commit SHA.
- **Secondary source:** the per-batch records in the project's `MEMORY.md`
  (e.g. "batch 160 closed … two orchestrator-as-mechanic rescues") — these
  name the fix-forward events narratively.
- **Detection-tier link:** the JUDGMENT decisions-log `detection_tier_*`
  fields (Q-13) tell you WHY a fix-forward happened (which tier should have
  caught it). A fix-forward with `detection_tier_target=integration` is an
  enrolment gap; one with `target=unit` is a coverage-tier gap.

**Refresh cadence:** quarterly, by the maintainer, alongside the
coverage-excludes retirement pass (Q-5) and the detection-tier aggregation
(Q-13). This is a maintainer review, **not a slice** — the same stance as
the flake-budget dashboard's derived-artifact status. Updating the
_definition_ of the metric (the formula or the bands) requires a slice;
recording a quarter's number does not.

## History

| Quarter | total_merged | with_fix_forward | rate | band | notes                                    |
| ------- | ------------ | ---------------- | ---- | ---- | ---------------------------------------- |
| _seed_  | _TBD_        | _TBD_            | _—_  | _—_  | First quarterly pass populates this row. |

The seed row is intentionally unpopulated: backfilling the full v1 + v2
history from `_STATUS.md` is a maintainer task, not in-scope for slice 353,
which formalizes the metric definition and the refresh discipline. The
first quarterly review fills the first real row.
