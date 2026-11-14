# SoA export (CSV and xlsx) — decisions log (#1791)

Fifth slice of the Statement of Applicability (#1744), after decision
storage (#1787), coverage exclusions (#1788), contradiction flags (#1789) and
bulk set and CSV import (#1790). An ISMS manager can export the SoA for a
framework version and hand it to an auditor.

detection_tier_actual: integration
detection_tier_target: integration

One bug surfaced while writing the tests: the first cut exported
`approver_user_id` as the approver, and the integration test found it NULL
after a real approval (see D4).

## Decisions

**D1: One route per framework version.** `GET
/v1/framework-scopes/applicability/export?framework_version=…&format=csv|xlsx`,
beside the existing `as_of` read at `/v1/framework-scopes/applicability`. The
export is per framework version, as the spec says, so it doesn't hang off a
scope id. `?revision=<scope id>` and `?as_of=<RFC3339>` pick the revision;
together they are a 400. A revision of another framework version is a 400,
an unknown revision or framework version a 404.

**D2: No revision in force exports every requirement undecided.** With no
activated revision (default) or none in force at `as_of`, the export lists
every requirement as `undecided` with empty revision and approval columns.
This matches the `as_of` decision read (#1787), which returns a null scope and
every requirement undecided rather than a 404.

**D3: CSV is the bare table; xlsx carries the title.** The CSV starts with the
header row, so it parses as a table and reads back through the #1790 import:
`requirement_code`, `decision`, `justification` and `implementation_ref` are
import column names, and the import ignores the others. `undecided` rows are
rejected there as an invalid decision, which is the right result. The title reaches
the CSV only through its filename (`statement-of-applicability-iso27001-2022.csv`).
The xlsx is the human deliverable: one sheet named for the title, then four
lines (title, framework and version, scope revision, counts including
undecided and contradictions), then the same table.

**D4: Approver comes from the workflow log.** `approver_user_id` is set only
when the approving credential id parses as a UUID (`Store.Approve`), so for
ordinary `key_…` credentials it is NULL. The export reads the actor of the
revision's most recent `approve` workflow-log row, which is always present,
and only when the revision currently carries an approval (`approved_at` set).
`approved_at` is exported as RFC3339 UTC.

**D5: Linked controls use the contradiction query's rule.** The column lists
the `bundle_id`s of active (`lifecycle_state = 'active'`, not superseded)
tenant controls that reach the requirement through
`effective_fw_to_scf_edges`, the same crosswalk read and the same "active
control" definition as `ListFrameworkScopeContradictions`. A flagged
contradiction therefore always lists the control behind it. `bundle_id` is
the control's stable identifier; titles can repeat and contain separators.

**D6: Formula neutralisation.** CSV cells whose first character is one of
`= + - @ \t \r` are prefixed with `'` (OWASP CSV injection, the same rule as
`internal/export`), on every column, not only the free-text ones. xlsx cells
are written as typed strings with `SetCellStr`, which a spreadsheet never
evaluates, so the text is kept verbatim; the test asserts the cell holding
`=HYPERLINK(…)` has no formula. Prefixing a quote in the xlsx would only
corrupt the text.

**D7: ISO detection is the catalog slug `iso27001`.** That is the slug the
ISO importer and `catalog/framework_codes.go` use. Anything else, including
other ISO standards, is titled "Applicability".

**D8: Contradictions follow the decision list.** The flag is computed against
current control evaluations for every selector, `as_of` included, as #1789 D6
set for the decision list.

**D9: xlsx writer.** The file is built with excelize (already a dependency),
in memory, then written. An SoA is at most a few hundred rows, so streaming
isn't needed, and a failed encode returns a clean 500 rather than a truncated
download.
