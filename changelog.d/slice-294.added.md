- **test(coverage):** slice 294 — `internal/metrics/eval` coverage lift
  from 33.0% to 78.4% merged. The package ships the 8 slice-076 starter
  metric evaluators (`program_effectiveness`, `evidence_freshness_pct`,
  `audit_readiness_score`, `open_risk_financial_exposure`,
  `policy_attestation_rate`, `vendor_risk_concentration`,
  `exception_expiration_runway`, `critical_findings_sla`). Pre-lift,
  every `Name()` was 100% covered (via the existing
  `registry_test.go`) and every `Compute()` was 0% because the
  package had no integration test exercising the SQL paths. This
  slice adds a `//go:build integration` `integration_test.go` that:
  (a) covers BOTH the empty-sample and populated branches for the
  6 evaluators whose v1 queries align with the v1 schema
  (`program_effectiveness`, `evidence_freshness_pct`,
  `open_risk_financial_exposure`, `vendor_risk_concentration`,
  `exception_expiration_runway`, `critical_findings_sla`); and (b)
  covers the wrapped-error path for the 2 evaluators whose v1
  queries reference schema artifacts that don't exist —
  `audit_readiness_score` queries `framework_scopes.framework_id`
  (the column was renamed to `framework_version_id`) and
  `policy_attestation_rate` queries a `policy_versions` table that
  the v1 schema does not carry (the actual table is `policies`).
  Both error branches are documented as v1-degraded in the slice's
  decisions log; the tests pin the wrapped-error contract so a
  future schema-aligning fix surfaces here. Test surface: 1 new
  file (~500 lines, 8 test functions + harness helpers), no
  production code changes, no new dependencies. The
  `./internal/metrics/eval/...` tree is now enrolled in the
  `Go · integration (Postgres RLS)` CI job. Floor in
  `cmd/scripts/coverage-thresholds.json` ratchets from `0` to `76`
  per slice 069's `floor(measured - 2pp)` methodology. Spillover
  from slice 279's coverage audit
  (`docs/coverage-audit-2026-05.md`).
