- **test(coverage):** slice 292 — `internal/api/oscalexport` coverage lift
  from 37.0% to 100% merged. Spillover from slice 279's coverage audit
  (docs/coverage-audit-2026-05.md). The audit flagged
  `internal/api/oscalexport` at 39.4% merged (33 stmts) as a `unit-add`
  target — the OSCAL export HTTP shell over `internal/oscal.Exporter`.
  Two changes ship in this slice. (1) Narrow the `Handler.exporter`
  field from `*oscal.Exporter` to a package-private `oscalExporter`
  interface with the single `Export(ctx, ExportInput) (*Bundle, error)`
  method the handler actually calls — `*oscal.Exporter` satisfies it
  implicitly, the production wiring at `internal/api/httpserver.go`
  does not change. (2) Add `internal/api/oscalexport/handler_more_test.go`
  injecting a `fakeExporter` to drive every branch in `writeExportError`
  (`ErrPeriodNotFrozen` → 409, wrapped `ErrPeriodNotFrozen` → 409 via
  `errors.Is`, `ErrPeriodNotFound` → 404, `ErrBridgeUnavailable` → 502,
  `ErrRoundTripFailed` → 422, `ErrSigningFailed` → 500, unrecognized
  sentinel → 500 default) and the body-marshalling code in `Export`
  (happy path with full body / empty-body short-circuit /
  `RequestedBy` from `authctx`-attached credential id / `RequestedBy`
  fallback to `"api"` when no credential or when credential id is
  empty / URL UUID forwarded to `ExportInput.AuditPeriodID` / all four
  bundle members marshalled into `bundleMemberJSON` with the OSCAL
  payload preserved as `json.RawMessage`). The constitutional invariants
  the handler enforces are unchanged (invariant 8 — OSCAL as wire
  format; invariant 10 — only frozen periods exportable; AI-assist
  boundary — handler imports no inference client). Floor ratchets
  37 → 98 (`floor(100 - 2pp)`). Slice 069 ratchet contract honored.
  Closes [#292](docs/issues/292-coverage-internal-api-oscalexport.md).
