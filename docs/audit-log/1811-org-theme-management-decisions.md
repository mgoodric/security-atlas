# #1811 — organisation theme management (decisions log)

API and CLI for an organisation's own risk themes beside the ten built-ins, a
step of #1745: add, rename and delete, with an audit trail.

- detection_tier_actual: none
- detection_tier_target: integration

No defect surfaced while building; the integration tests passed on first run.

## D1 — routes under `/v1/themes`

`POST /v1/themes`, `PATCH /v1/themes/{ref}`, `DELETE /v1/themes/{ref}`, beside
the existing `GET /v1/themes`. `ref` is the theme's name or id. The OPA
`themes` resource is already writable by grc_engineer (admin is a wildcard),
so no Rego change. The handlers live on the risks handler so they share
#1810's `requireTaxonomyAdmin` guard, strict decoder and actor.

## D2 — names are lowercase slugs

`AssignThemes` lowercases every theme it stores, so a theme named with
capitals could never be assigned. Create and rename trim and lowercase the
name, then require `^[a-z0-9][a-z0-9_-]{0,62}$`, the category slug rule. All
ten built-ins already fit it. " Shadow-IT " becomes `shadow-it`.

## D3 — uniqueness across built-ins, checked in the transaction

The two partial unique indexes on `org_themes` cover the built-in and the
per-tenant namespace separately, so a tenant could otherwise create
`ownership`. `ThemeNameTaken` checks both inside the write's transaction. The
built-ins are migration-only and never change, and a concurrent duplicate in
the tenant's own namespace still hits the tenant index (23505 maps to 409).
No schema change was needed for this.

## D4 — refusals are 409, never 500

A clash with a built-in or another theme, changing a built-in, and deleting a
theme in use are 409 (the last with `risk_count` and `rule_count`). A bad
name or an over-long description is 400; an unknown or other tenant's theme
is 404. Refused writes write no audit row.

## D5 — rename rewrites risks and rules in one transaction

Rename updates the theme row, replaces the name in every one of the
tenant's `risks.themes` arrays (deduplicated and sorted, as `AssignThemes`
stores them), and retargets the tenant's aggregation rules. The engine reads
the target from `rule_body`, not the `target_theme` column, so both are
rewritten; updating only the column would leave the rule evaluating the old
name. The integration test proves it by running an evaluation after the
rename and checking the rule still counts the renamed risks.

A rule's `title_template` may contain the theme name literally; it is left
alone (the `{theme}` placeholder already follows the rename). Existing
meta-risk titles are history and are not rewritten. A rename writes no
`aggregation_rule_audit_log` row; the taxonomy trail records it, and the
response carries `risks_updated` and `rules_updated`.

Rename does not re-evaluate the rules. The same risks match the same rules
before and after, so an evaluation would reach the same result.

## D6 — locks: vocabulary, then rules, then rows

Themes drive the aggregation engine (#1867, #1880), so the rename takes its
locks in the order a risk write does:

1. A per-tenant theme-vocabulary advisory lock. Rename and delete take it
   exclusively; `validateThemesAgainstVocab` (every theme assignment) takes
   it shared before reading the vocabulary. Without it an assignment could
   validate the old name, wait, and store it after the rename or delete
   committed.
2. `BeforeRiskWrite` for the old and new names: the hierarchy lock and the
   meta-risk-key locks of every active rule targeting either, so a rename
   never interleaves with an evaluation of a rule it retargets.
3. Risk rows, taken by the `UPDATE`.

An assignment takes (1) before its own (2) and row lock, so the order is the
same on both sides. Delete takes only (1): it refuses when anything uses
the theme, so it never touches a risk or rule.

## D7 — no archive for themes

Categories archive because a used category cannot be deleted and must still
be hidden from new risks. The ticket gives themes create, rename and delete
only, with delete refused while in use; a theme in use can be renamed, or
removed from its risks and rules first. Archiving themes is not in scope.

## D8 — audit rows reuse `taxonomy_audit_log`

Migration `20261024000000` widens the `subject_kind` CHECK to
`('category', 'theme')`, as the #1810 table header anticipated. Actions are
`create`, `rename` (name and/or description) and `delete`. `subject_slug`
carries the theme name; `before` / `after` carry `{name, description}`.
The rows reach the unified admin audit log as kind `taxonomy` with
`target_type` `theme` without further change.
