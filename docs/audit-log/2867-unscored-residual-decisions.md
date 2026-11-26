# #2867 — An unscored risk's residual reads as unscored, not a 500 — decisions log

`GET /v1/risks/{id}` returned 500 (`derive residual: inherent_score missing
numeric likelihood/impact`) for a risk with no `inherent_score`. This log
does not block merge.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(Seen in passing in #2835's D7. #2511 added unscored risks with an
integration test over create and list, but not over the single-risk read.)

## D1 — The read path is wrong, not the create path

The ticket offers two fixes: require `inherent_score` at every create
path, or make the read path tolerate its absence. #2511 already decided
the domain rule. `risk.PrepareCreateScores` stores an omitted score as
`{}`, an unscored risk, on REST and MCP alike, and the web shows it as
"Unscored" (`isUnscored` in `web/app/(authed)/risks/filters.ts`). So the
residual deriver is what is out of step, and it is the only thing changed.

## D2 — Shape of an unscored residual

`ResidualResult` gains `Unscored`. On the wire, `residual.inherent_score` and
`residual.residual_score` become nullable and are `null` for an unscored
risk, with `unscored: true`. A `0` would read as a real score of zero. The
control effectiveness breakdown is still derived, because a control's
effectiveness does not depend on the risk's score. No web change is needed:
the detail page reads `risk.inherent_score` and `risk.residual_score`, not
the `residual` object, and already renders Unscored.

## D3 — Nothing is persisted for an unscored risk

`DeriveAndPersist` returns without writing, so `residual_score` stays `{}`.
Writing the derived blob would store `{"score": 0, ...}`, which the list
and sort paths read as scored, and `PrepareCreateScores` refuses a residual
without an inherent score anyway. This also stops the residual subscriber
from redelivering forever for an unscored risk with a linked control.

## D4 — Existing rows

Existing unscored rows need no data fix: they are valid under #2511 and
now read correctly. The read-only count on the live deployment was not
run from this session (the permission classifier refused the SSH to the
NAS). The query is in the PR body.
