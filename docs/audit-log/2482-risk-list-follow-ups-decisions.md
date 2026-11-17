# Issue #2482 — Risk list follow-ups after the server-side register

Three follow-ups from the review of #2475 (#2132), which moved the register's
filter, sort and paging onto `GET /v1/risks` and capped a call at 500 rows.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: unit

All three were found in review, not by a test. The BFF's dropped repeated
filter and the pickers' silent truncation are both unit-testable seams, and
are now covered there.

## D1 — The BFF forwards every value of a repeated key

**Decision.** `web/app/api/risks/route.ts` reads each allow-listed key with
`getAll`, and `listRisks` repeats a key given as a list
(`?category=a&category=b`). The BFF does not keep a second list of which
keys take several values.

**Why.** Only `category` is multi-value on the platform today
(`categoryRefs(q["category"])` in `internal/api/risks/handlers.go`). Every
other key is read with `q.Get`, which takes the first value, the same value
the BFF used to forward. Forwarding all values therefore changes nothing for
single-value keys, and a key the platform later makes multi-value works
without a BFF change.

## D2 — Whole-list callers of `GET /v1/risks`

Every caller of the list endpoint, and what each needs from the 500 cap:

| Caller                                                           | Needs the whole list?                      | Change                                                                                                                                                     |
| ---------------------------------------------------------------- | ------------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Sidebar badge (`RisksCountBadge`, `fetchRisksList`)              | Yes: counts high-severity rows client-side | `fetchRisksList` walks every page.                                                                                                                         |
| Register "N of M" (`registerSizeQ`, same query key as the badge) | No: reads `total`                          | None. It already preferred `total`.                                                                                                                        |
| Entity picker (`fetchRisksListWithRollUps`)                      | Yes: offers every risk                     | `fetchRisksListWithRollUps` walks every page.                                                                                                              |
| Risk hierarchy attach picker (`fetchRisksListWithRollUps`)       | Yes: offers every risk                     | Same as above.                                                                                                                                             |
| Register page (`fetchRegisterPage`)                              | No: one page, reads `total`                | None.                                                                                                                                                      |
| Dashboard top risks (`getMitigateRisks`, `/api/dashboard/risks`) | No: shows the top rows by `residual,age`   | None. The envelope's `count` is set from `risks.length` but no reader uses it.                                                                             |
| MCP `list_risks` (`internal/mcp/tools/risks.go`)                 | No, but reports `capped`                   | Sends its `limit` to the platform and reads `capped` from `total`. Before, with more than 500 risks and `limit=500`, a full page reported `capped: false`. |
| Risk export (`/v1/risks/export`)                                 | Yes                                        | None. It reads `dbx.ListRisks` directly, which is not paged.                                                                                               |
| Board pack, dashboard export, metrics                            | Yes                                        | None. They read the store in-process, not the HTTP list.                                                                                                   |

**Decision.** Page through rather than raise the cap or add a count
endpoint. The walk stops when it holds `total`, and also on an empty page,
so a register that shrinks mid-walk cannot loop. A response without `total`
(an older platform) ends after one page, as before.

## D3 — A residual score is a JSON number, never a numeric string

**Decision.** Keep JSON numbers only, on both sides. `risk.ScoreValue`
(through `numField`, a `float64` type assertion) and the SQL residual
extraction in `ListRisksPage` (`jsonb_typeof(...) = 'number'`) already agree:
neither reads `{"score":"20"}`. Both treat it as unscored, and it sorts
with the unscored risks.

**Why.** The ticket asked to align the two only if `numField` accepted
strings. It does not, so there was nothing to align, only to pin. The
create path stores a non-FAIR `residual_score` as given, so a client can
write a string. Coercing strings on read would spread a second rule into
both readers for a shape nothing in the platform writes (the residual
deriver writes numbers). Refusing strings on write is a separate change,
left out of scope here.

**Pinned by.** `TestScoreValueReadsEveryShape` (three numeric-string
cases) and `TestListRisksPage_ResidualOrderMatchesRanksAbove`, which now
seeds a `{"score":"25"}` risk and checks that the SQL order still matches
`risk.RanksAbove` with that risk below every scored one.
