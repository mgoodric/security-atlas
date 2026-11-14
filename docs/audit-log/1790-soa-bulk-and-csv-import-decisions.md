# SoA bulk set and CSV import — decisions log (#1790)

Fourth slice of the Statement of Applicability (#1744), after decision
storage (#1787), coverage exclusions (#1788) and contradiction flags (#1789).
An ISMS manager can set one decision on many requirements in one call, and
import decisions from a CSV.

detection_tier_actual: none
detection_tier_target: integration

No bug surfaced. The mutation check is in the PR body.

## Decisions

**D1: One write path for single, bulk and import.** `SetDecision` now calls
`SetDecisions`, and the import applies its valid rows through the same
`writeDecisions` helper. Every decision goes through the same upsert, the
same frozen-revision refusal and the same `applicability.update` log row, so
the three entry points can't drift apart. Each requirement gets its own log
row, and the first row on a revision in review or approved records the reset
to draft (`from_state` approved, `to_state` draft). The rows after it are
draft to draft.

**D2: Bulk is all-or-nothing.** All three acceptance tests deal with one
decision applied to every listed requirement. An invalid decision or a
missing justification returns 400, a requirement outside the revision's
framework version returns 404, and a frozen revision returns 409, the same
codes as the single PUT. In each case nothing is written. Duplicate ids in
`requirement_ids` are collapsed.

**D3: Import is partial and reports bad rows.** This is what the ticket says
should happen. The valid rows are applied as one change, in one transaction.
The bad rows are returned with their 1-based file line (the header is line
1), the requirement code and a fixed reason string:

- `unknown requirement code`
- `requirement code appears more than once with different values` (D9)
- `decision must be applicable or excluded`
- `a justification is required to exclude a requirement`
- `row could not be read`, for a CSV quoting error

**D4: Status codes follow the policy import (#1747).** The response is 200
when at least one row was applied. It is 422, with the same report body,
when no row was valid, and in that case nothing is written and the revision
keeps its state. A file with no header, or a header that lacks the code or
decision column, returns 400, and so does a header with no data rows.

**D5: The header row is required, and columns are matched by name.** The
code column accepts `requirement_code`, `requirement code`, `requirement` or
`code`. The decision column is `decision`. The two optional columns are
`justification` and either `implementation_ref` or `implementation
reference`. Matching ignores case, spaces, hyphens and underscores. Columns
can come in any order, and extra columns are ignored, so an exported
spreadsheet with more columns imports as it is. A UTF-8 BOM (which Excel
writes) is stripped. Decisions are case-folded (`Applicable` works), and
every cell is trimmed. A row with every mapped cell empty is skipped, since
spreadsheets often end with blank rows.

**D6: The body is the raw CSV.** It is capped at 4 MiB, which is far more
than any SoA needs. The Content-Type isn't checked, so `curl --data-binary
@soa.csv` works.

**D7: Codes match exactly after trimming.** The match is case-sensitive,
because framework requirement codes are distinct strings and folding case
could map one code onto another.

**D8: Routes.** The two routes are `POST
/v1/framework-scopes/{id}/applicability/bulk` and `.../applicability/import`.
They are under the existing `framework-scopes` resource, so the rego write
gate (grc_engineer and admin) applies to them unchanged.

## Follow-ups (#2036)

From the review of #2027.

**D9: A code repeated with different values rejects every row for it.**
Originally the first row for a code was applied and later rows were
rejected as duplicates, so the file's row order silently decided which
decision won. Now, if a code appears more than once and the rows differ in
decision, justification or implementation reference, every row for that
code is rejected and nothing is written for it; the operator fixes the
file. Rows that repeat a code with identical values are collapsed and
applied once, since a copy-pasted row is not ambiguous. Comparison is after
trimming every cell and case-folding the decision, so `Applicable` and
`applicable` count as the same.

**D10: Bulk case-folds its decision.** Bulk now trims and lower-cases
`decision`, as the import does (D5), so `Excluded` works on both paths. The
single PUT is unchanged.

**D11: The bulk body is capped at 1 MiB.** It is read through
`http.MaxBytesReader`, as `policies/import.go` does, and an oversized body
returns 413. 1 MiB holds thousands of requirement ids.

**Note for the SoA export ticket.** `justification` and
`implementation_ref` are stored verbatim, as the operator typed or imported
them. An export to CSV or a spreadsheet must neutralise cells that start
with a formula character (`=`, `+`, `-`, `@`) so an exported SoA can't run
a formula when opened.
