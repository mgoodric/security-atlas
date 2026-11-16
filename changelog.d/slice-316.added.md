- **test(coverage):** slice 316 — three HTTP handler packages
  (`internal/api/calendar`, `internal/api/search`,
  `internal/api/questionnaires`) coverage lift to 70%+ merged. Round-3
  coverage spillover surfaced during slice 312's audit
  (`docs/coverage-audit-2026-05-round-3.md`) — three HTTP handler
  packages measured below 70% merged: `internal/api/calendar` (223
  stmts, 40.4% merged), `internal/api/search` (214 stmts, 32.2% merged),
  and `internal/api/questionnaires` (147 stmts, 5.4% merged). All three
  already shipped comprehensive `integration_test.go` suites from their
  parent slices (calendar: slice 094, search: slice 268, questionnaires:
  slice 155) that drive the chi router + tenancy middleware + RLS-bound
  app role against real Postgres — but none was enrolled in CI's
  `tests-integration` job's `-coverpkg` list, so the merged audit
  measured the unit-only floor. Enrolling all three packages lifts
  calendar from 40.4% → 81.6% merged and search from 32.2% → 80.8%
  merged on integration enrolment alone — both packages already shipped
  adequate unit-only test suites (`handler_test.go` + `ics_test.go` for
  calendar; `search_test.go` for search) so no new unit file lands for
  those two (slice 315 D2 doctrine: skip vanity unit tests when the
  existing suite covers the load-bearing surface). For
  `internal/api/questionnaires`, a new
  `internal/api/questionnaires/helpers_test.go` covers the pure-Go
  pre-DB short-circuits reachable without a Postgres pool: every
  handler's `no-tenant-context → 401` branch (Create, List,
  ImportExcel, Get, UpsertAnswer, Suggestions, ExportPDF — the
  `Suggestions` handler was 0% covered pre-slice because the existing
  RLS-isolation test silently skips when the test DB has no SCF
  anchors), the `missing-id / missing-qid / missing-anchor → 400`
  branches, `bad-JSON → 400` on Create + UpsertAnswer,
  `name-required → 400` on Create, plus the pure helpers `writeJSON`,
  `writeError` (Content-Type + status + body shape pin), and
  `contextWithPDFDeadline` (a non-zero deadline on the derived
  context). Together with the integration enrolment this lifts
  `internal/api/questionnaires` from 5.4% → 73.5% merged. Floors in
  `cmd/scripts/coverage-thresholds.json` add three new entries
  (`internal/api/calendar: 79`, `internal/api/search: 78`,
  `internal/api/questionnaires: 71`) per slice 069's
  `floor(measured - 2pp)` methodology. The `internal/questionnaire`
  engine — distinct from the HTTP handler — is owned by slice 319 and
  explicitly NOT bundled here (P0-316-4). No new functional logic;
  testing-only PR. AI-assist boundary unchanged. Same
  enrolment-as-load-bearing-move playbook as slices 290 / 297 / 310 /
  313 / 315 / 317 / 319. See
  `docs/issues/316-coverage-http-handlers-enrollment.md`.
