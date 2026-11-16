- **test(coverage):** slice 319 — `internal/questionnaire` coverage lift
  from 26.5% to 79.3% merged. Round-3 coverage spillover surfaced during
  slice 312's audit (`docs/coverage-audit-2026-05-round-3.md`) — the
  questionnaire engine (Excel parser + AnswerLibrary RLS lookup + PDF
  render helpers + Store CRUD over the four `questionnaire_*` tables
  - `answer_library`) measured 26.5% merged with no entry in
    `cmd/scripts/coverage-thresholds.json` (untracked). 324 statements;
    distinct from `internal/api/questionnaires` (the HTTP handler from
    slice 155 + slice 316 enrolment surface). The engine's existing
    unit-only excel + library tests covered the parser and the SQL-shape
    but exercised none of the Store CRUD nor any of `pdf.go` — because
    the Store paths were exercised only transitively by
    `internal/api/questionnaires/integration_test.go`, that coverage
    attributed to the HTTP-handler package, not the engine. Three new
    test files land in this slice:
    `internal/questionnaire/integration_test.go` (build-tagged
    `integration`, in-package so coverage attributes to the engine —
    exercises `Store.CreateQuestionnaire` / `GetQuestionnaire` /
    `ListQuestionnaires` / `AddQuestionsFromParse` / `UpsertAnswer` /
    `ListQuestionsWithAnswers` / `SuggestForAnchorWithPool` against a
    real Postgres, with a dedicated RLS-isolation test that proves
    tenant B cannot see tenant A's library entries — canvas invariant
    #6); `internal/questionnaire/pdf_test.go` (pure-Go unit suite hitting
    every branch of `buildHTML` — needs-mapping / anchor / domain /
    answer-vs-unanswered / HTML-escape — `encodeForDataURL` — every
    replacement pair — and `isChromeUnavailable` — recognized + nil +
    unrelated patterns); `internal/questionnaire/store_helpers_test.go`
    (pure-Go unit suite for `uuidToString` / `pgUUID` / `rowToAnswer` /
    `rowToQuestion` — covers the invalid-UUID short-circuit, the
    empty-citations no-unmarshal branch, and the nil-vs-empty-string-vs-
    populated `ScfAnchorID` ladder). Two small additions to the existing
    `excel_test.go` + `library_test.go` push their per-function coverage
    to 100% (excel) / 88.9% (library): single-row workbook →
    `ErrNoHeaderRow`, corrupt-bytes payload → wrapped excelize error,
    header-missing-text → `ErrNoHeaderRow`, `matchAlias`
    empty/whitespace, `SuggestForAnchor` multi-row scan happy path,
    default-limit clamp boundary. CI's `tests-integration` job's package
    list extends to include `./internal/questionnaire/...` so the new
    build-tagged tests run against the same Postgres RLS service
    container as the other floored packages. Floor in
    `cmd/scripts/coverage-thresholds.json` adds a new entry at
    `internal/questionnaire: 77` per slice 069's
    `floor(measured - 2pp)` methodology (`floor(79.3 - 2) = 77`) — the
    package was previously untracked, so the ratchet adds a floor where
    there was none. The AI-assist citations field on `UpsertAnswer` is
    exercised through its pass-through branch (the canvas §4.6.5
    citation validation lives at the HTTP layer; the engine itself
    stores citations as opaque JSON). No vendor-prefixed tokens in
    fixtures per `CLAUDE.md`'s hard rule. Same playbook as slices
    290 / 297 / 310 / 315 — in-package `integration_test.go` + CI
    integration-enrollment is the load-bearing move. Closes
    [#319](docs/issues/319-coverage-internal-questionnaire.md).
