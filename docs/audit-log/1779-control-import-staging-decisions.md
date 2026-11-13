# Issue #1779 — Control import: upload and stage a control sheet

Build-time calls made while building
[#1779](https://github.com/mgoodric/security-atlas/issues/1779), the first
ticket under the bring-your-own-controls spec
([#1734](https://github.com/mgoodric/security-atlas/issues/1734)): an operator
uploads a CSV/XLSX of existing controls and gets a staged import batch back,
every row staged or rejected with a reason.

`detection_tier_actual = none` · `detection_tier_target = none` (no product
bug surfaced; the one failure during the work was a malformed CSV literal in a
new unit test).

## D1 — A `BatchStore` interface beside `MappingStore`

**Decision.** `internal/controlimport` gains a `BatchStore` interface
(`ResolveFramework`, `ResolveRequirements`, `CreateBatch`, `GetBatch`), a
`Stager` that drives it, and a Postgres `Store` implementing it. The existing
`MappingStore` is unchanged.

**Why.** `MappingStore` is shaped around one AI proposal at a time
(persist, approve, reject); staging a whole file is a different unit of work.
Widening `MappingStore` would force its test fake and any future
implementation to carry batch methods it has no use for. Both interfaces live
in the importer package, so the spec's "behind the importer's store interface"
still holds, and the candidate and approval tickets can implement
`MappingStore` on the same `Store` type.

## D2 — Four rejection reasons, not three

**Decision.** Rows are rejected as `missing_required_field`,
`unknown_tested_under`, `duplicate_id`, or `invalid_implementation_type`.

**Why.** The ticket names the first three. An implementation type outside the
bundle manifest's enum (`automated | semi_automated | manual_attested |
manual_periodic`) would otherwise either be dropped silently or fail later at
approval, long after the operator could fix the sheet. Values are normalised
first (`Semi-Automated` → `semi_automated`), so only genuinely unknown values
are rejected. A DB CHECK pins the reason set and the staged column's enum.

## D3 — Description stays required

**Decision.** ID, title and description are all required, per row and as
header columns.

**Why.** That was the parser's existing contract (its header test asserts it)
and a control bundle needs a description. A row missing any of them is
rejected with the missing field names in the detail.

## D4 — Duplicate IDs: first occurrence wins, case-insensitive

**Decision.** The first row carrying an organisation control ID is staged;
later rows with the same ID (compared case-insensitively) are rejected as
`duplicate_id`, the detail naming the first row. Duplicates against controls
already in the tenant are not checked here.

**Why.** Staging the first keeps the common re-paste mistake cheap to fix.
Matching against existing controls needs the external references from #1737
and is the approval ticket's job (spec story 7).

## D5 — Framework is required; tenant framework wins; codes match case-insensitively

**Decision.** `framework` is a required form field. It resolves to the
framework's `current` version, preferring a tenant-private framework with that
slug over the global catalog's. Tested-under codes are split like SCF
crosswalk cells (newline, `;`, `,`; bullets and `N/A` dropped), matched
case-insensitively, and stored as the requirement's canonical code plus its
`framework_requirements.id`. An unknown code rejects the whole row.

**Why.** The spec makes the framework part of the CLI shape. Resolving to IDs
at staging time is what the crosswalk candidate ticket needs. Keeping a row
"without its unknown codes" is an opt-in the spec mentions but this ticket's
acceptance criteria do not; it is left for when a user asks for it.

## D6 — Cap errors: 413 for size, 400 for row count

**Decision.** An upload over 5 MiB returns 413 naming the size cap; a sheet
over 5,000 rows returns 400 naming the row cap. Both say to split the file.
Nothing is persisted for a refused upload.

**Why.** 413 is the size status; a row count is a content limit, not a body
size. The caps themselves are the existing `MaxUploadBytes` / `MaxRows`.

## D7 — Routes under `/v1/controls/imports`

**Decision.** `POST /v1/controls/imports` and `GET /v1/controls/imports/{id}`.

**Why.** The authz middleware derives the resource from the first path
segment, so these are `controls` writes/reads under the existing role rules
(grc_engineer writes, auditors and viewers read) with no new Rego. The literal
`imports` segment sits ahead of `/v1/controls/{id}/...` like `/export`.

## D8 — The XLSX fixture is built from the CSV fixture at test time

**Decision.** `testdata/controls.csv` is the committed fixture; the XLSX case
converts it cell-for-cell with excelize.

**Why.** One source of truth for the expected rows, and no binary in the diff.
Both formats go through the real upload path.

## D9 — Integration suite enrolled on Leg A

**Decision.** `./internal/api/controlimports/...` runs on integration Leg A.

**Why.** The suite seeds global catalog rows (`frameworks`, `scf_anchors`,
`framework_requirements`, `fw_to_scf_edges`) under per-test slugs. P0-2 of
`scripts/integration-shards.txt` keeps catalog seeders on Leg A.

## D10 — CLI auth uses `--token`, uploader is the credential id

**Decision.** `atlas-cli controls import` authenticates with `--token` /
`SECURITY_ATLAS_TOKEN` (`resolveCommon`). The batch's `uploaded_by` is the
authenticated credential's id, as the questionnaire handlers record their
actor.

**Why.** The OAuth client-credential flags belong to `controls upload`'s
bootstrap path; an operator importing a sheet holds a token. Tenancy comes
from the credential either way (#1739).

## Coverage floors

New rows in `cmd/scripts/coverage-thresholds.json`, at measured merged
(unit + integration) coverage minus 2pp: `internal/controlimport` 83
(measured 85.0%) and `internal/api/controlimports` 78 (measured 80.3%).
