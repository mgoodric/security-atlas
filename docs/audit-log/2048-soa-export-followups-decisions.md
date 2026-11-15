# SoA export follow-ups — decisions log (#2048)

Follow-ups from the review of #2046 (#1791): the contradiction note for a past
revision, the CSV quote round trip through the #1790 import, and the OpenAPI
entry for the export route.

detection_tier_actual: none
detection_tier_target: none

No bug surfaced. The round-trip test was run without the import strip first
and failed on every formula-leading cell, as expected.

## Decisions

**D1: The quote is lossless, not just stripped.** The ticket asks the import
to strip one leading `'` before `= + - @ \t \r`. Doing only that loses text
an operator really typed: a justification of `'=x` exports unchanged (it does
not start with a formula character) and re-imports as `=x`. So the export
quotes a cell that already looks quoted too, and the import strips one quote
only from a cell the export could have written
(`frameworkscope.QuoteFormulaCell` / `UnquoteFormulaCell`). For every cell
without a stray leading quote this is exactly the ticket's rule. Stripping
never produces a formula in the database, because imported cells are stored
as text, and re-exporting quotes the cell again. The test checks that export,
import, export gives the same bytes.

**D2: The note row appears only when it applies.** For any `?as_of=`, and
for a `?revision=` that is not the activated revision, the xlsx title block
gets one more line, right above the header:
`frameworkscope.ContradictionNote`. The activated-revision export keeps its
#1791 layout, with the header on row 5. A fixed blank row would keep the
header row constant, but it would change the default layout for no reader's
benefit. An `as_of` that resolves to the activated revision still gets the
note, because an `as_of` export is a point-in-time document.

**D3: The CSV carries no preamble.** The CSV stays the bare table so it still
reads back through the import (#1791 D3). The OpenAPI 200 description states
the contradiction rule for both formats.

**D4: OpenAPI detail follows the per-operation override pattern.** Most
routes document only path parameters. The routes that document more
(`/v1/search`, `/v1/admin/install/reset-bootstrap`) do it with a
per-operation block in `internal/api/openapi/generator.go`, and this route
follows that pattern rather than adding query parameters to `RouteSpec` for
one route.
