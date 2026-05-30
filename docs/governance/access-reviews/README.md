# Access review artifacts

This directory holds the per-review evidence artifacts produced by
the cadence defined in
[`../access-review.md`](../access-review.md). One Markdown file per
review event.

## File naming convention

| Review type   | Filename pattern               | Example                                |
| ------------- | ------------------------------ | -------------------------------------- |
| Quarterly     | `YYYY-Q<N>.md`                 | `2026-Q3.md`                           |
| Semi-annual   | `YYYY-H<1\|2>.md`              | `2026-H2.md`                           |
| Annual        | `YYYY-annual.md`               | `2027-annual.md`                       |
| Trigger-based | `YYYY-MM-DD-<trigger-slug>.md` | `2026-07-15-collaborator-departure.md` |

## Per-artifact template

Each artifact uses the TOML-frontmatter + Markdown body template
specified in [`../access-review.md` §6](../access-review.md#per-review-artifact-template).
The frontmatter carries machine-readable fields (`review_id`,
`review_type`, `scheduled_date`, `performed_date`, `reviewer`,
`trigger`) so future automation can index the artifacts without
parsing the body.

## Confidentiality

Per-review artifacts are **public by default**, same posture as the
[incident-response plan §8](../incident-response.md#8-documentation-and-audit-trail)
and the [business-continuity plan §10](../business-continuity.md#10-documentation-and-audit-trail).
Where redaction is required (e.g., a webhook URL whose path is a
secret), the artifact carries a `[redacted — see private archive]`
placeholder and the unredacted material is held by the maintainer
privately.

## First-review baseline

This directory is empty at slice 374's filing (2026-05-28). The
**first** quarterly review under the cadence is due **2026-08-28**;
its artifact will be `2026-Q3.md`. No retroactive artifact is
filed for any pre-cadence informal access checks (per
[`../access-review.md` §3 pre-cadence baseline note](../access-review.md#pre-cadence-baseline-reviews)).
