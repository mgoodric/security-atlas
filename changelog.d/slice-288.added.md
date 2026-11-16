- **test(coverage):** slice 288 — `internal/audit/walkthrough` coverage
  lift from 6.0% to 79.9% merged. The package shipped a 571-line
  `integration_test.go` in slice 027 (Create / Get / List /
  AddAttachment / Finalize / RLS isolation / tamper detection / period
  freeze) but was never enrolled in the CI integration job, so the
  slice 279 audit measured unit-only at 6.0%. Enrolling
  `./internal/audit/walkthrough/...` in the `Go · integration (Postgres
RLS)` job lifts the Store methods from 0% to 70-90% per-function. A
  new `internal/audit/walkthrough/export_test.go` adds 12 pure-Go
  unit-tests covering the previously-uncovered export helpers:
  `buildPDFHTML` (full happy path + tamper banner + live-walkthrough
  placeholder + HTML escaping of user-controlled fields + optional-
  section omission), `renderMarkdown` (every block kind H1-H4, UL with
  both marker forms, OL including multi-digit, paragraphs joined by
  space, blank-line break, block transitions exercising closeBlocks),
  `renderInline` (closed + unterminated backticks + HTML escaping),
  `headingLevel` (every accepted depth + 5+ rejection + no-space + no-
  hash + empty), `isAllDigits` (digit-only + mixed + empty negative
  contract), `encodeForDataURL` (every replacement target: % # & ? \n
  \r), `isChromeUnavailable` (each substring branch + nil + unrelated-
  error negative cases), `safePrefix` (short + exact-16 + long buffer),
  and `IsTamperFlagged`. Per-function unit coverage on export.go
  helpers moves from 0% to 100% on every named function. Coverage-
  threshold floor for `internal/audit/walkthrough` ratchets from 3 to
  77 (`floor(79.9 − 2)`) per the slice 069 ratchet contract and the
  slice 279 measurement methodology (`floor(measured − 2pp)`). Net new
  test surface: 1 new file (321 lines unit tests for export.go
  helpers), no production code changes, no new dependencies.
  Honors invariant 6 (RLS continues to be the integration suite's
  tenancy gate). [#288]
