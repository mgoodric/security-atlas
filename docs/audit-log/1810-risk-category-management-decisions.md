# #1810 — organisation risk category management (decisions log)

API and CLI for managing an organisation's risk categories, a step of #1745
after the #1809 expand step: add, rename, archive / un-archive, set default,
and delete an unused category, with usage counts and an audit trail.

- detection_tier_actual: integration
- detection_tier_target: integration

The integration tier caught one defect while building (D6).

## D1 — routes stay under `/v1/risks/categories`

`POST /v1/risks/categories`, `PATCH|DELETE /v1/risks/categories/{ref}` and
`POST /v1/risks/categories/{ref}/{archive|unarchive|set-default}`. `ref` is a
slug or an id, as risk create accepts. Staying under `/v1/risks/` keeps the
existing `risks` OPA resource and feature gate: writes there are already
admin and grc_engineer only, so no Rego change was needed. None of the
action segments is an authz transition action, so all three map to `write`.

## D2 — who may write: admin or grc_engineer

The spec says "risk-admin / tenant-admin". The canonical roles are admin and
grc_engineer (the role that authors risks). OPA is the production gate; the
handler guard `requireTaxonomyAdmin` is the testable twin, because test
servers run without OPA. It accepts `IsAdmin`, `IsApprover`, or a JWT tenant
role of `admin` or `grc_engineer` (jwtmw carries tenant roles in
`OwnerRoles`, so a tenant admin who is not a super-admin is recognised by
name). control_owner, auditor, viewer and bare credentials get 403. Reads
keep #1809's posture.

## D3 — what "an aggregation rule uses a category" means

Aggregation rules target a theme, not a category; a rule touches a category
only through the meta-risks it creates. `rule_count` is the number of
distinct rules with a meta-risk (`risk_aggregations.rule_id`) in that
category. Those meta-risks are risks too, so `risk_count` > 0 whenever
`rule_count` > 0. Delete refuses either. The default category is also
refused for archive and delete (409, set another default first): it is the
aggregation fallback #1745 introduces, and the table CHECK forbids an
archived default.

## D4 — one `taxonomy_audit_log` table, not `risk_audit_log`

`risk_audit_log` is keyed by risk id and not in the unified log. The new
append-only table follows its shape (SELECT + INSERT RLS, a trigger denying
UPDATE/DELETE) with `subject_kind` (`category` now; the themes ticket adds
`theme` to the CHECK) and appears in `/v1/admin/audit-log/unified` as kind
`taxonomy`. Before/after hold the category's slug, name, description,
archived and default flags. Each row is written in the change's transaction,
so a refused change leaves no row.

## D5 — archived categories take no new assignments

`resolveCategory` now refuses an archived category on risk create (400).
Existing risks keep it. No risk update path changes category yet (#1809 D5),
so "moving a risk off an archived category" has nothing to change. The MCP
`create_risk` applier resolves its category in the apply transaction through
`risk.ResolveCategoryTx` and writes `category_id` explicitly, so an unknown,
archived or deleted category is `ErrInvalidInput` (400 on confirm) and
nothing is written. Review found this path, which D6 would otherwise have
turned into a 500 for a deleted category. Aggregation meta-risks copy their
first child's category and still go through the sync trigger; #1745's
aggregation batch moves them to the organisation default.

## D6 — the lazy seed no longer restores a deleted category

#1809's `seed_risk_categories` inserted any missing seeded slug and runs on
every list and risk write, so a deleted seeded category (`financial`) came
back on the next list. The integration test caught it. The function now
seeds only a tenant with no categories at all; the down migration restores
the old body. The API and the MCP applier resolve the category first, so a
deleted one is a 400 there (D5). A raw SQL write that sends a deleted
category's enum value gets a NOT NULL violation on `category_id`, which is
correct (the category is gone); the contract step removes the enum path.

## D7 — added categories cannot hold risks until the contract step

A new slug (`cloud-security`) has no `risk_category` enum value, so creating
a risk in it still fails until #1745's contract step drops the enum.
`resolveCategory` already refused it, as a 500; it is now a 400 that says
why. Management is complete without it; the category exists, lists,
renames, archives and deletes.

## D8 — CLI uses `--endpoint` and `--token`

`atlas-cli taxonomy categories ...` follows `controls import`: the persistent
`--endpoint` (HTTP base URL) and `--token`, not the `features` command's
`--http-endpoint` / `$ATLAS_ADMIN_TOKEN` pair. `list` prints a table
(`--json` for the raw response); writes print one line naming the result.
