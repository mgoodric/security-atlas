- **frontend:** slice 256 — Coverage column in the `/controls/{id}`
  coverage-by-framework table. Closes the slice 204 page-audit
  Finding 4 by promoting the mockup's headline data-bound metric
  (`Plans/mockups/control.html` line 178) to a first-class backend
  field on `GET /v1/controls/{id}/coverage`. The Go handler at
  `internal/api/ucfcoverage/handlers.go` computes per-row
  `coverage = strength × 30d_pass_rate` when the requirement's
  `framework_version` is in scope for the control (control's
  applicability set ∩ the activated FrameworkScope's predicate); the
  field is `null` when out of scope OR when the control has zero
  effectiveness data (`TotalCount == 0` — distinguishing "no data" from
  "perfectly failing" per anti-criterion P0-256-1). The field is
  always emitted on the wire (no `omitempty`) so callers see a stable
  `coverage: <number> | null` shape. The frontend at
  `web/components/control/coverage-table.tsx` adds the Coverage column
  (right-aligned, mono numeric, two decimals; "n/a" in muted style
  for null), re-binds the existing strength bar's fill to coverage
  (not raw strength — mockup lines 192/203/214), renders a
  non-interactive chevron affordance with explanatory tooltip
  (JUDGMENT D2, per anti-criterion P0-256-4 — no 404 destinations),
  and adds the mockup-line-254 footer reading "coverage is strength ×
  30-day effectiveness, intersected with the framework's scope
  predicate." Two pure helpers (`formatCoverage`, `coverageBarPercent`
  at `web/components/control/coverage.ts`) carry the rounding /
  clamping / null-handling logic and ship with 8 vitest cases per
  AC-6. The frontend never recomputes coverage client-side — the
  slice 041 "fabricating it here risks a number that disagrees with
  the backend" warning is honored by reading the backend's authoritative
  value verbatim (P0-256-1). Integration tests at
  `internal/api/ucfcoverage/integration_test.go` cover the three AC-2
  branches: in-scope numeric, out-of-scope null, zero-effectiveness
  null, plus a wire-shape guard that the `coverage` key is always
  emitted (not just when non-null). Playwright e2e at
  `web/e2e/control-detail.spec.ts` adds a `slice 256: coverage column
renders both numeric and n/a rows` test (assertions commented
  pending the slice-082 seed harness, matching the surrounding
  convention). The `ucfcoverage.Handler` constructor adopts a
  two-stage `New + AttachCoverage` shape so unit servers that don't
  wire eval/scope/framework_scope plumbing keep the slice-008 wire
  shape (JUDGMENT D5). Decisions log:
  [docs/audit-log/256-coverage-column-decisions.md](docs/audit-log/256-coverage-column-decisions.md).
  Closes #256.
