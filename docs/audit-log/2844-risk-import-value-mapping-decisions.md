# Issue #2844 — Risk import: category and theme value mapping

The value-mapping slice of spec #2812 (Q13), replacing the tracer's (#2839)
exact per-row category match.

- `detection_tier_actual`: integration
- `detection_tier_target`: unit

(The handler first recorded a created category's mapping under `cred.ID`,
which for a test JWT is `jwt:` with no subject. The integration suite showed
it in a response body; `mapped_by` now uses `credstore.PrincipalOf`, as the
rest of the import does. A handler unit test could have caught it.)

## D1 — A value is a trimmed, case-insensitive spelling within one batch

**Decision.** The batch lists each distinct category value and each distinct
theme value once. `Ops` and `OPS` are one value, listed under the spelling
that appears first; `Ops`, `Operational` and `operations` are three values,
each mapped by the reviewer.

**Why.** The point of mapping per batch is that spelling variants stop
multiplying. Case is the variant a reviewer would never want to map twice.
Anything looser (stemming, edit distance) would decide for the reviewer
which values mean the same thing, which is what the mapping step is for.

## D2 — A value that already names a category or theme is mapped at upload

**Decision.** At staging, a category value that names an active category by
slug or by name (`MatchCategory`, the tracer's rule) is mapped to it, and a
theme value that equals a visible theme name ignoring case is mapped to it.
`mapped_by` is empty for these. Every other value stays unmapped. The
reviewer can change or clear any mapping while the batch is staged.

**Why.** Story 9: an export re-imports with no mapping. Mapping to an
existing category or theme creates nothing, so this is not the implicit
creation the spec forbids.

## D3 — Creation goes through `risk.Store.CreateCategory` / `CreateTheme`, gated as their routes are

**Decision.** `PUT /v1/risks/imports/{id}/values` with `create` calls the
same store methods as `POST /v1/risks/categories` and `POST /v1/themes`, so
the same validation and the same `taxonomy_audit_log` row apply. The handler
gate is the routes' own gate, `authz.IsTenantApprover`, and the audit actor
is `cred.ID`, as those routes' `actorFrom` records it. Mapping to an existing
value is a write on risks, the upload's permission. The batch and value are
checked before anything is created, so a create for a value the batch lacks,
or on a completed batch, creates nothing.

**Why.** The ticket says "through the existing approver-gated, audited
category and theme creation". A live `grc_engineer` grant is a tenant
approver (#2428), so a GRC engineer with a live grant can create; a
credential that holds the role only as a JWT claim (#2381), a viewer, an
auditor or a control owner cannot. In practice those are already refused by
OPA's write on risks. The handler gate is tested directly in
`TestMapValueRefusals`.

## D4 — An unmapped value rejects the row at approval, as a decision

**Decision.** Approval (all pending, or one row) reads the batch's map under
`FOR SHARE`. A row with an unmapped category value or any unmapped theme
value is decided `rejected`, with a reason naming each value ("not mapped in
this import batch: category "operations", theme "People""). The rejection
writes an import audit `reject` row naming the approving person, and
`ApproveResult.rejected` lists it. No risk is created. The duplicate check
comes first, so a duplicate row stays a duplicate whatever its values.

**Why.** The ticket says "a row with an unmapped value is rejected with a
reason", and the spec says "an unmapped value rejects the row". The
alternative was to refuse the approval and leave the row pending (a 422, as
for an archived category). It was not chosen because it diverges from both
texts, and because a value the reviewer chose not to map is a decision about
that row. A rejected row is final, as every decision is; the register's
rejected rows are fixed and uploaded again (story 17). The `FOR SHARE` lock
means a mapping change waits for an approval reading it, so a row is never
decided under a mapping that changed mid-decision.

## D5 — Themes are set inside the create, not by a later assignment

**Decision.** `risk.CreateInput` gains `Themes`. `insertPrepared` checks
them against the visible vocabulary and takes the theme vocabulary lock
(shared) and the aggregation rules' locks before it inserts. It then sets
them on the new row and writes a `themes_changed` audit row from none,
after the `created` row. The API create leaves `Themes` empty, which leaves
its behaviour unchanged.

**Why.** An approved row's risk, its decision and its audit rows commit
together (#2839). A second transaction through `AssignThemes` would let a
risk exist without the themes its row was approved with. The lock order is
the one `AssignThemes` takes (vocabulary, rules, rows), so a concurrent
rename or delete of a theme cannot interleave. A theme deleted between
mapping and approval is the store's `ErrUnknownTheme`, which skips the row
and leaves it pending, like an archived category.

## D6 — `category_slug` is replaced by `category_value`, with a backfill

**Decision.** Migration `20261292000000_risk_import_value_maps` adds
`category_value` and `theme_values` to `risk_import_rows` and the
`risk_import_value_maps` table (four-policy RLS, like the other import
tables). Rows staged earlier carry the slug their value matched. The
migration copies that slug into `category_value`, maps it to itself, and
drops `category_slug`. The down migration restores `category_slug` from the
map.

**Why.** A pending row from before the migration must stay approvable with
the category it was staged with. One source of truth, the map, is simpler
than reading a slug column for old rows and the map for new ones.

## D7 — Themes are read from a themes column, split on commas and semicolons

**Decision.** `Themes`, `Theme`, `Risk Themes`, `Risk Theme`, `risk_themes`
and the export's `themes` map to the themes field. A cell splits on `,` and
`;`, and each value is trimmed and kept once ignoring case.

**Why.** The risk export writes themes comma-separated. Registers commonly
use semicolons. The export's `themes` header used to land in unmapped; it
now round-trips (`TestRiskImport_ExportRoundTrip`).

## D8 — The review page shows values, not a mapping editor

**Decision.** The review page shows an unmapped category as
`<value> (unmapped)`, and shows a row's theme values beside the themes they
map to. The interactive mapping step on the upload page (spec, "Web") is not
in this change.

**Why.** The ticket's acceptance criteria are the API and its tests. Without
this change, the page would show an empty category for an unmapped row. The
mapping editor is follow-up work for the spec's web upload page.
