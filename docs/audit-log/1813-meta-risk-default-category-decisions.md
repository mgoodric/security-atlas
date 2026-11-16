# #1813 — aggregation meta-risks fall back to the organisation's default category: decisions log

Migrate step of #1745 (expand -> migrate -> contract). The aggregation
engine no longer hard-codes `operational` as a meta-risk's category; it
falls back to the tenant's default category. The `risk_category` enum is
still dual-written until the contract ticket. The calls below were made at
build time and are recorded for revisit.

- detection_tier_actual: manual_review
- detection_tier_target: unit

(The bug: a rule-generated meta-risk was created in an archived category
when its first scored child was in one, which #1810 forbids for new risks.
It was found by reading the engine while scoping this ticket.)

## D1. The rule changed from the ticket wording: first scored child with an _active_ category

The ticket says a meta-risk takes "its first scored child's category,
otherwise the organisation's default". The second arm cannot happen. A rule
fires only when its scored-child count reaches `min_risks`, and `min_risks`
must be positive in both the DSL validator and the DB CHECK
`aggregation_rules_min_risks_positive`. So every meta-risk the engine creates
has a scored child, and the old `operational` branch was dead code.

The rule is now: **the first scored child whose category is not archived,
otherwise the organisation's default.** An archived category takes no new
assignments (#1810), and a meta-risk is a new risk. Before this change the
engine copied an archived child category onto a new meta-risk, because it
writes only the enum and the `risks_category_sync` trigger maps the enum to
the reference without checking `archived`. With the new rule the default is
reachable, and the acceptance tests drive it through the API: archive the
children's category, then evaluate. The dispatcher approved this change on
2026-11-14.

Meta-risks that already exist keep their category. The engine only chooses
a category when it creates a meta-risk, and refreshes never move it.

## D2. The categories are a plain read, taken before any lock

`Engine.Evaluate` reads the tenant's categories (`ListRiskCategories`) after
it selects the rules and before the hierarchy lock and the meta-risk key
locks. The read is a plain `SELECT` with no `FOR SHARE`, so it takes no row
lock and cannot join the rule-key -> row lock order (#1867). A default
changed by a concurrent set-default can still land between the read and the
insert. The meta-risk then gets the previous default, which was valid when
it was read. That is acceptable for a fallback; #2233 tracks the lock-order
follow-up. On a risk write, `LockRules` takes the key locks before
`Evaluate` runs. The read still locks nothing, so that order is unaffected.

`ListRiskCategories` was added to the engine's `metaRiskWriter` interface as
a read. The five-write safety bound is unchanged.

## D3. Set-default refuses a custom category until contract

The risk row still carries the enum, so a default the enum cannot hold
(a category added through #1810's management API) would make every fallback
meta-risk insert fail, and with it the risk write that triggered the
evaluation. `SetDefaultCategory` now refuses such a category with an
`InvalidCategoryError`, a 400 with the same wording as risk create's
refusal. The contract step lifts the refusal when it drops the enum.

A tenant that set a custom default before this refusal existed gets the
first scored child's category, archived or not. That was the behaviour
before this change, and it never writes a value the column cannot hold. The
engine never fails a risk write over the category.

## D4. No migration

The default already lives on `org_risk_categories.is_default` (#1809), and
the seed marks `operational` as the default. The behaviour of a tenant that
never set a default is therefore unchanged.
