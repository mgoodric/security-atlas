- **test(coverage):** slice 302 — `connectors/okta/cmd/atlas-okta`
  coverage lift from 20.7% to 64.9% merged. A new
  `connectors/okta/cmd/atlas-okta/cmd_coverage_test.go` adds 28 unit
  tests covering the cobra glue (`newRootCmd` smoke + subcommand
  wiring + persistent-flag presence; `newRegisterCmd` PreRunE missing-
  env error path + RunE unreachable-endpoint RPC-error path;
  `newRunCmd` PreRunE all four flag-missing branches — `--org`,
  `--environment`, `--okta-base-url`, and the resolveCommon
  fall-through; `newScopesCmd` Run path renders all four documented
  scopes through the tabwriter), the two enum mappers
  (`mapPolicyResult` 40% → 100% and `mapUsersResult` 40% → 100% via
  table-driven coverage of PASS/FAIL/INCONCLUSIVE + the UNSPECIFIED
  default), env-var + global-flag resolution (`resolveCommon` 0% →
  100% via six branches: endpoint/token × flag/env/missing), both
  dial-transport branches (`dialConnectorRegistry` 0% → 87.5% — TLS
  - insecure both lazy-dial against 127.0.0.1:1 with no network IO),
    the `authedContext` metadata wiring (0% → 100% with Authorization
    header assertion + cancel-func verification), `sdkOpts` insecure
    vs secure paths (0% → 100%), `connectorVersion` smoke, `actorID`
    shape pinning across all three services (policy / apps / users),
    the optional-field branches of `buildUserLifecycleRecord` (62.5%
    → 93.8% — `primary_email`, `created_at`, `activated_at`,
    `last_login_at`, and the DEPROVISIONED-gated `deactivated_at`),
    the `asAny` early-return + populated round-trip paths (83.3% →
    100%), and the doRun auth-resolve-error branch (the only doRun
    branch unit-coverable without a seam refactor — the post-resolve
    Pull + push loop sits behind `oktaauth`/`oktapolicy`/`oktaapps`/
    `oktausers` API calls and is exercised by the existing
    `integration_test.go` suite plus the self-host bundle e2e job).
    Mirrors the pattern slice 299 established for
    `connectors/aws/cmd/aws-connector` (9.0% → 65.8% via the same
    "test what's testable without refactor" rule; both leave the
    same architectural seam-refactor gap which slice 305 captures for
    AWS and which a follow-on slice will capture for okta if the seam
    refactor proves worth the carry). No vendor-prefixed Okta tokens
    (the 42-char `00...` API-token pattern) appear in fixtures, per
    `CLAUDE.md`'s hard rule — neutral `test-*` strings only. Floor in
    `cmd/scripts/coverage-thresholds.json` ratchets from `18` to `62`
    per slice 069's `floor(measured - 2pp)` methodology. Spillover from
    slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
