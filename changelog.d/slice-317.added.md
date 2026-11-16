- **test(coverage):** slice 317 — MCP write-proposals stack coverage lift
  to 70%+ merged. Round-3 coverage spillover surfaced during slice 312's
  audit (`docs/coverage-audit-2026-05-round-3.md`) — two MCP
  write-proposals packages measured below 70%: `internal/api/
mcpwriteproposals` (108 stmts, 0.9% merged) and `internal/mcp/
writeproposals` (218 stmts, 1.8% merged). Both already shipped
  comprehensive integration suites — the HTTP handler's
  `integration_test.go` drives the chi router + slice-190 JWT bearer
  against real Postgres (POST create, confirm + canonical-row verify,
  reject, 403-not-approver, cross-tenant RLS isolation); the store's
  `integration_test.go` exercises Create/Get/List/Confirm/Reject + the
  schema-level `mcp_wp_ai_assist_invariant` CHECK + the per-(tenant,
  user) pending-cap + Applier-error rollback. Neither was enrolled in
  CI's `tests-integration` job, so the merged audit measured the
  unit-only floor. New unit suites added: `internal/mcp/writeproposals/
helpers_test.go` covers `stateToEvidenceResult` (all four canonical
  enum values + invalid fallback), `nullableString` / `nullableSubject`
  (nil + value branches), `PendingCap` (default + override),
  `WithApplier` (replacement semantics), and `Store.Create` /
  `Store.Confirm` pre-tx validation against a nil pool (unknown tool,
  empty/whitespace/malformed tool_input, missing ai_model_name /
  ai_model_version / created_by, empty approver).
  `internal/mcp/writeproposals/appliers_test.go` covers every applier's
  pre-DB validation branch with a nil tx: `ApplyCreateRisk` (bad JSON +
  empty title + empty category), `ApplyUpdateControlState` (bad JSON +
  missing control_id + missing state + non-UUID control_id + invalid
  state enum), `ApplyPushEvidence` (bad JSON + missing required fields
  - non-UUID control_id + invalid result enum), `ApplyUpdateRiskTreatment`
    (bad JSON + null-body + missing risk_id + missing treatment + non-UUID
    risk_id), and `RegisterDefaultAppliers` (all four tool names wired).
    `internal/api/mcpwriteproposals/handlers_test.go` covers the pre-store
    branches against `Handler{store: nil}`: every endpoint's
    401-when-unauthenticated, `CreateProposal` 400 on bad JSON / unknown
    tool / missing model fields, `GetProposal` 400 on non-UUID, `Confirm`
    / `Reject` 403 when caller is not approver / not admin + 400 on
    non-UUID, every branch of `writeCreateErr` (UnknownTool +
    InvalidInput + PendingCapExceeded → 429 + fallthrough 500),
    `writeTransitionErr` (NotFound + WrongState + UnknownTool +
    fallthrough), `writeServerErr` direct, `proposalWireFrom` empty +
    populated `tool_input` rendering, and `tenantCredContext`
    no-credential + empty-tenant-id + missing-tenancy-GUC + happy path.
    Together with the CI integration-enrolment they lift `internal/mcp/
writeproposals` from 1.8% → 77.5% merged and `internal/api/
mcpwriteproposals` from 0.9% → 84.3% merged. Floors in
    `cmd/scripts/coverage-thresholds.json` add two new entries
    (`internal/mcp/writeproposals: 75`, `internal/api/mcpwriteproposals:
82`) per slice 069's `floor(measured - 2pp)` methodology. No new
    functional logic; testing-only PR. AI-assist boundary unchanged
    (CLAUDE.md §"AI-assist boundary (hard)" remains the canonical
    contract — these tests are testing the gate itself, never relaxing
    it). Same enrolment-as-load-bearing-move playbook as slices 290 /
    297 / 310 / 315. See `docs/issues/317-coverage-mcp-write-proposals.md`.
