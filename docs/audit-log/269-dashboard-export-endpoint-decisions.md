# Slice 269 — Dashboard snapshot export endpoint decisions

Slice 269 (`docs/issues/269-dashboard-snapshot-export-endpoint.md`)
ships `GET /v1/dashboard/export?format=json|csv|xlsx` to unblock the
`Export` half of slice 230 (the dashboard's header CTAs). The
endpoint composes the six dashboard panels into a point-in-time
snapshot in three formats.

The slice is typed **AFK + JUDGMENT** — most decisions are mechanical
wire-ups (mirror slice 138 / 175 patterns), but four design calls
shape the endpoint and are recorded below. Decisions log follows
the slice 175 / 174 template.

## D1 — Encoders in-package vs extension to `internal/export/`

**Decision:** The three multi-panel encoders (json, csv-zip,
multi-sheet xlsx) live in
`internal/api/dashboardexport/encoders.go`. We do NOT extend
`internal/export/` to grow a multi-panel API.

**Why:**

- **Library design.** `internal/export/` ships single-table
  encoders by construction (one CSV body, one XLSX sheet, one JSON
  array). The slice 135 D1 JUDGMENT call deliberately kept the
  XLSX writer single-sheet-text-only — extending it to multi-sheet
  would force a wider API surface that no other caller needs.
- **Slice 269 is the only multi-panel export today.** Every other
  export endpoint (controls, controls-history, risks, evidence,
  policies, exceptions, samples, anchors, vendors, audit-periods,
  audit-log, audit-log-unified) is single-domain. Building a
  multi-panel API in the shared library for one caller would be
  premature abstraction (canvas anti-pattern).
- **Streaming guarantees scale at the caller layer.** Each encoder
  here streams via `archive/zip.Writer` (csv + xlsx) or
  `json.NewEncoder` directly to the response. The AC-10 50K-row
  memory test runs against the in-package encoders directly,
  asserting heap delta < 200 MB across all three formats.
- **OWASP CSV sanitizer is a 6-line predicate.** We copied it
  inline rather than depending across packages for one call site.
  Same OWASP rule, same threat-model coverage.

**Rejected:**

- **Extend `export.Exporter` interface to multi-panel.** Would
  break every existing single-table caller's signature.
- **Use a third-party `xuri/excelize` for the xlsx path.** Slice
  135 D1 explicitly rejected this for supply-chain reasons; same
  argument applies here.

## D2 — Risks panel projection: which fields to surface

**Decision:** The risks panel emits
`{id, title, treatment, category, methodology, residual_score, created_at}` —
NOT a synthesised severity scalar.

**Why:**

- **Risk has no top-level severity field.** The risk score lives
  in the JSONB `residual_score` blob, shaped by the methodology
  (NIST 800-30 5x5 carries `{likelihood, impact, value}`; FAIR
  carries an annualized-loss-expectancy struct). Synthesizing a
  scalar would force the export to take a side on methodology
  semantics — the dashboard's risks panel sorts by
  `sort=residual,age` which is a comparator over the JSONB blob,
  not a scalar field.
- **Operators import the export into a methodology-aware tool.**
  An auditor pulling the CSV / XLSX into Excel is doing
  methodology-specific math (5x5 grid colouring, FAIR PERT
  reduction); they want the raw `residual_score` blob, not a
  collapsed scalar.
- **The dashboard's risks panel uses the same
  `treatment=mitigate&sort=residual,age` filter** the BFF at
  `web/app/api/dashboard/risks/route.ts` passes through. The
  export composes the SAME filter so what shows up in the export
  matches what the operator sees in the live UI.

**Rejected:**

- **Surface a `severity_band` enum (P0/P1/P2/P3).** Bands are
  methodology-specific; the dashboard doesn't display them either.
- **Drop `residual_score` entirely.** The whole point of the risks
  panel is residual-ranked-by-age; dropping the score makes the
  export's ordering opaque.

## D3 — Role gate: admin + approver only (narrower than slice 156)

**Decision:** The handler-level `hasDashboardExportAccess` admits
`IsAdmin || IsApprover`. The OPA admit (`auditor.rego`
`auditor_readable_resources["dashboard"]`) admits the auditor
role in addition. Control-owner is DENIED.

**Why:**

- **Bulk-handoff is a higher-sensitivity surface than the in-app
  read.** The slice 066 + slice 156 dashboard reads admit
  control-owner because a control-owner credential lands on the
  dashboard at sign-in and needs the page to render. The export
  is a different operation: "package the full posture for handoff
  to a non-atlas consumer (auditor email, board PDF, archive)."
  That consumer cannot reason about scope cells / RLS — they see
  whatever is in the file. Narrowing the export admit beyond the
  read admit is the same posture every per-domain export takes
  (slices 137 / 138 / 175 all narrow vs. the underlying read).
- **`IsAdmin || IsApprover` matches the slice 138 evidence-export
  admit shape.** Consistency across export surfaces.
- **Auditor admit goes through OPA only** (not the handler-level
  predicate). Mirrors the slice 156 "auditor sees the program
  rollup" admit — an auditor doing audit-period work needs the
  full posture to seed their working notes.

**Rejected:**

- **Admit control-owner.** The slice 156 admit was a "let them
  render the page" call; the export is "let them email the
  posture out" which is a different threat model.
- **Admin-only.** Too narrow — auditors and grc_engineers have a
  legitimate need (board prep, audit handoff, archive).

## D4 — `dashboard.Store` exported wrappers vs. exporting `keyset`

**Decision:** Added two new exported methods on `dashboard.Store`:
`ActivityFeedFirstPage(ctx, limit)` and
`UpcomingItemsFirstPage(ctx, categoryFilter, limit)`. We do NOT
export the package-private `keyset` type.

**Why:**

- **The cursor wire format is intentionally opaque.** Slice 066's
  `pagination.go` doc says the cursor is "an opaque
  base64url-encoded string" — exposing the struct shape would
  invite callers to peek inside, which would lock the encoding
  into the public API.
- **Slice 269 only needs the first page.** The export composes
  "what the dashboard renders at sign-in" — the first page,
  newest-first / soonest-first. A future caller that needs N
  pages can still construct the wire cursor and call the existing
  `ActivityFeed(ctx, cursor, limit)`; this wrapper is a
  convenience, not a replacement.
- **Two functions, ~5 lines each.** Cheap to add; no
  cross-package coupling cost.

**Rejected:**

- **Export `Keyset` as a struct.** Locks the wire shape forever.
- **Have slice 269 reconstruct the cursor.** Forces slice 269 to
  know about `farFuture` / `farPast` sentinel timestamps that are
  dashboard-package implementation details.

## CI-delta scan record

Slice 269 touches the chi router (httpserver.go), the OpenAPI
route list, the OPA auditor.rego, and the coverage thresholds.
The CI-delta scan confirms:

- **httpserver.go**: new dashboardexport mount declared per the
  parallel-batch convention (chi.Mux rejects two Mounts at "/"), so
  the add is a `root.Get("/v1/dashboard/export", ...)` appended to
  the existing dashboard mount block. No shadowing of any
  existing route — `/v1/dashboard/export` is a fresh top-level
  path (the slice 066 reads are at `/v1/frameworks/posture`,
  `/v1/activity`, `/v1/upcoming`, not under `/v1/dashboard/`).
- **OpenAPI routes**: one new entry at the alphabetically-correct
  position; the openapi-drift-check CI guard passes locally.
- **OPA bundle**: `policies/authz/auditor.rego` updated AND the
  embed copy `internal/authz/rego_bundle/auditor.rego` synced.
  The `TestSlice269_DashboardExportAdmitMatrix` unit test pins
  the admit shape; the slice 035 `TestAuthzMatrix_AllRolesAllEndpoints`
  integration test (run under the integration tag) covers the
  full role × resource matrix including the new admit.
- **Coverage thresholds**: `internal/api/dashboardexport/` added
  to the excludes list — consistent with every other
  `internal/api/*export*` package (slices 137 / 138 / 174 / 175
  precedent).
- **CI integration list**: `./internal/api/dashboardexport/...`
  added to the `tests-integration` job's `go test -tags=integration`
  invocation so the cross-tenant + meta-audit + memory tests
  actually run in CI.

## Pre-commit checks (slice-process JUDGMENT)

- `pre-commit run --all-files` — pass (CHANGELOG bullet present).
- `go test ./internal/api/dashboardexport/...` — pass (unit suite).
- `go test ./internal/authz/...` — pass (slice 269 OPA matrix
  test green).
- `go build ./...` — pass.
- `golangci-lint run ./internal/api/dashboardexport/...` — pass
  (no findings).
- `just openapi-generate` + `just openapi-drift-check` — pass
  (one new route registered, drift check green).
