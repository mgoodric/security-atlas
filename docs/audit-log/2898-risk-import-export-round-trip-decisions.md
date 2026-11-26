# Issue #2898 — Risk import: scores, owner and org unit round-trip from a risk export

A follow-up to #2841 (scores, PR #2889, D7), with two additions from its
comments: the linked owner (#2843 review, PR #2938) and the org unit (#2845
review, PR #2949, D4).

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — The export's JSON score columns are fields of every methodology

**Decision.** `inherent_score` and `residual_score` are two new column-map
fields, recognised by the export's own headers. Unlike the eight pair fields
they apply to every methodology. The cell's JSON goes through
`risk.PrepareCreateScores` for the batch's methodology, so a score of another
shape (a FAIR score in a NIST batch) fails the schema and rejects the row as
`invalid_score`. `{}` and `null`, the export's unscored risk, are no score.

**Why.** The ticket names the rule. Scoping the JSON fields to a methodology,
as the pair fields are, would leave a mismatched score unmapped and import
the risk unscored without a word. A rejection is the louder and correct
outcome when a register scored one way is imported as another.

## D2 — A score comes from its JSON cell or its pair, never both

**Decision.** Per score (inherent, residual), a row with both a JSON cell and
a filled pair is `invalid_score`: "give the score as inherent score or as
inherent likelihood and inherent impact, not both". A row may give its
inherent score as JSON and its residual as a pair, or the other way round.

**Why.** Picking one silently would discard the other, and the two can
disagree. Checking per score, not per row, keeps a register that adds a
residual pair beside exported JSON importable.

## D3 — An id column is used when it resolves, and is otherwise unmapped

**Decision.** `owner_user_id` and `org_unit_id` are new fields. Parsing keeps
each value both as a ref and as an unmapped value, because it cannot know the
tenant. Staging then uses the value and removes it from the row's unmapped
values when it resolves:

- `owner_user_id` resolves when it is the id of an active user of the
  uploading tenant (the same tenant and status predicate as the email match);
- `org_unit_id` resolves through `risk.Store.GetOrgUnit`, the tenant-filtered
  `GetOrgUnitByID`, as the #2949 review asked.

A value that does not resolve (another tenant's id, a deleted unit, a
disabled user, text) stays unmapped and rejects nothing, which is what
happened to every value before this change. The batch lists the column among
its unmapped columns only while some staged row still holds a value under it.

**Why.** The two comments ask for exactly this fallback. Exporting an email
instead was the alternative for owners, and #2085 rules it out.

## D4 — A resolved owner id wins over the owner cell

**Decision.** When `owner_user_id` resolves, it is the linked owner and the
owner cell (`treatment_owner` in an export) is not read.

**Why.** It matches the create path: with an owner user set,
`TreatmentOwner` is not stored. In an export of a linked risk the free-text
owner is empty anyway.

## D5 — An org unit name wins over the org unit id

**Decision.** `org_unit_id` is used only when the row names no org unit by
name. With a name, the name rule (#2845) decides, including its
`unknown_org_unit` and `ambiguous_org_unit` rejections, and the id stays
unmapped. A resolved id stages `org_unit_value` as the unit's name, which
the review page shows and `risk_import_rows`' CHECK requires.

**Why.** The comment frames the id as the fallback when no name column is
mapped. A person who maps a name column has said which unit they mean.

## D6 — Parent links stay unmapped

**Decision.** `parent_risk_ids` is unchanged: it stays unmapped. Noted as a
candidate follow-up, not built.

**Why.** It is not in the ticket's scope. Linking a parent by uuid also needs
the parent to exist in the uploading tenant, which it does only on a
same-tenant re-import. That rule deserves its own decision (#2953 D5).

## D7 — The generic score aliases are documented, not changed

**Decision.** `docs-site/docs/primitives/risks.md` gains "Importing a risk
register", which says that `Likelihood`/`Impact` and `LEF`/`LM` must hold
numbers, that a text value such as "High" rejects the row as `invalid_score`,
and how to leave such a column out.

**Why.** The ticket asks for documentation. Translating words to numbers
would mean choosing a scale for someone else's register, and the import
refuses to guess (#2841 D5).
