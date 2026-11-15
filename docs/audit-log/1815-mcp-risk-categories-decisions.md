# #1815 — MCP risk tools take organisation categories: decisions log

Migrate step of #1745 (expand -> migrate -> contract). The MCP risk tools
take a category by name or slug, say where the valid ones come from, and no
longer suggest `technical` or `legal`. The `risk_category` enum is still
dual-written until the contract ticket. The calls below were made at build
time and are recorded for revisit.

- detection_tier_actual: none
- detection_tier_target: none

(No product bug surfaced. The invalid schema examples were the reported
defect; `TestRiskToolSchemas_NoInvalidCategoryExamples` now guards them.)

## D1. Name matching lives in `risk.ResolveCategoryTx`, not in the MCP layer

The `create_risk` applier already resolves its category through
`ResolveCategoryTx`, the path `POST /v1/risks` uses. Name matching was added
there, as a fallback after the slug-or-id lookup, rather than as a second
resolver in the MCP tool. Consequence: `POST /v1/risks` and
`PATCH /v1/risks/{id}` accept a display name too. That is additive (a slug or
id still wins, and every previously valid request resolves the same way)
and keeps one resolver for every writer.

## D2. Case-insensitive on the trimmed value, tenant-scoped, archived included

`ResolveRiskCategoriesByName` matches `lower(btrim(name))` against
`lower(btrim(ref))` within the caller's tenant (explicit predicate plus RLS).
It returns archived categories as well, so a name that only an archived
category carries is refused as archived (the existing message) rather than
as unknown, which tells the caller what is actually wrong.

## D3. More than one name match is refused, listing the slugs

Active names are unique per tenant (`org_risk_categories_active_name_unique`,
on `lower(name)`), but an archived category may share a name with an active
one. Such a name is an `AmbiguousCategoryError` naming every matching slug,
rather than preferring the active match. Fail closed: the caller names the
category by slug, which is never ambiguous. Revisit if operators routinely
recycle archived names and find the refusal noisy.

## D4. A `list_risk_categories` read tool, not a list baked into the schema

Categories are organisation-owned, so no fixed example list can be correct
in a static schema. A read tool wrapping `GET /v1/risks/categories` (with
`include_archived`) gives the assistant the tenant's own values; the
`create_risk` and `list_risks` schema descriptions point at it. It sits after
`get_risk` in `CanonicalToolOrder`, so the read surface is now 12 tools.

## D5. The `list_risks` filter stays slug-or-id

The ticket says the read filter takes slugs, and `GET /v1/risks?category=`
(#1812) takes slugs or ids. The MCP tool forwards the value unchanged; the
description now names slugs and `list_risk_categories`. Name matching was not
added to the filter, which keeps the read path's semantics identical to the
API's.

## D6. Unknown categories are refused at confirm, not at propose

The proposal endpoint stores `tool_input` without resolving it; the applier
resolves it when the approver confirms, inside the same transaction as the
write. Resolving at propose time as well would mean a second check against a
category set that can change before confirmation. The assistant avoids the
round trip by calling `list_risk_categories` first.
