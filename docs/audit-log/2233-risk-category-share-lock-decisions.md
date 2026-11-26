# #2233 — risk category writes hold the category FOR SHARE: decisions log

Follow-up from the reviews of #2225 (#1812) and #2258 (#1815).
`ResolveCategoryTx` read the target category with an unlocked `SELECT`, so an
`ArchiveCategory` committing between that read and the risk write left a
risk on a category archived at the same moment. The FK check takes only
`FOR KEY SHARE` and does not re-read `archived`.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(The race was found in review of #2225. The integration tests below fail
on the unlocked read: the racing create returns 201 and the racing move 200.)

## D1. Lock inside `ResolveCategoryTx`, not a second resolver

Every caller of `ResolveCategoryTx` assigns a risk to the result: risk
create (`Store.Create`), the PATCH category move (`Store.UpdatePlacement`)
and the MCP `create_risk` applier. No read path uses it. So the two queries
it runs became share-locking ones, renamed to say so
(`ShareLockRiskCategory`, `ShareLockRiskCategoriesByName`), rather than
adding a locked variant beside an unlocked one nothing would call.

The lock is `FOR SHARE`, not `FOR UPDATE`: share locks do not conflict with
each other, so concurrent risk writes onto one category do not queue, and
it conflicts with the archive's `FOR UPDATE` (`LockRiskCategory`). Under
READ COMMITTED a write that waits on the archive re-reads the row after the
archive commits and sees `archived = true`, so it is refused; an archive
that waits on a write archives after the write commits (the old state: a
risk already on a category that is then archived, which #1810 allows).

## D2. Lock order

| Path                                                             | Locks, in order                                                                                      |
| ---------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| Risk create                                                      | category row `FOR SHARE` (no risk row exists yet)                                                    |
| PATCH category move (`UpdatePlacement`)                          | category row `FOR SHARE`, then the aggregation rules' advisory locks, then the risk row `FOR UPDATE` |
| MCP `create_risk`                                                | category row `FOR SHARE`                                                                             |
| Category admin (archive, unarchive, rename, set default, delete) | that category row `FOR UPDATE` only                                                                  |

The risk paths take the category before the risk row, as the existing
placement order (#1867 / #2204) already resolved the category first. What
matters for deadlock is that no path takes them the other way round, and
none does: category admin never locks a risk row. Its delete reads
`RiskCategoryUsage`, a plain `count(*)` with no row locks, after locking
the category; the `DELETE` itself only reaches risks through the FK check,
and only when that count was zero. The aggregation engine updates a
meta-risk's `category_id` under its rule and risk locks; its FK check takes
`FOR KEY SHARE` on the category, which is compatible with `FOR SHARE` and
only waits behind a category admin write, which waits on nothing.

## D3. A single active name match wins over archived ones

Supersedes #1815's D3. Active names are unique per tenant
(`org_risk_categories_active_name_unique`) and an archived category takes no
assignments, so a name matching one active category and any archived ones
resolves to the active one. Zero active matches with archived ones is
refused as archived; several active matches (not reachable while the unique
index holds) stay an `AmbiguousCategoryError`.

## D4. Race tests are condition-driven

The race tests hold an archive open in a real transaction, start the risk
write, and wait until `pg_locks` shows a backend blocked by the archive's
pid (`pg_blocking_pids`) before committing. No fixed sleep stands in for
"the write has reached the lock".

## D5. The down migration of `20261030000000` names its refusal

It already could not apply while `category_changed` rows existed (the
narrowed CHECK fails on them, and the append-only trigger refuses deleting
them), but with a bare constraint-violation error. It now raises
`risk_audit_log holds N category_changed row(s); refusing to delete audit
evidence` with a hint, the shape of the other audit-log down migrations.
