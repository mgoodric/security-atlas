- **test(coverage):** slice 300 — `connectors/jira/cmd/atlas-jira`
  coverage lift from 30.4% to 71.5% merged. A new
  `connectors/jira/cmd/atlas-jira/cmd_coverage_test.go` adds 26 unit
  tests covering the cobra glue (`newRootCmd` smoke + subcommand
  wiring for register + run + scopes + persistent-flag presence;
  `newRegisterCmd` PreRunE missing-env error path + RunE
  unreachable-endpoint RPC-error path; `newRunCmd` PreRunE all five
  flag-validation branches — invalid `--platform`, missing
  `--environment`, `--platform=jira` missing `--jql`,
  `--platform=linear` missing `--team-key`, plus the resolveCommon
  fall-through and the happy-flag-success path through every guard;
  `newScopesCmd` Run path renders the PLATFORM/NAME/ACCESS/GATES
  header plus rows for both jira and linear platforms), the
  ticket-status `classifyResult` mapper (50% → 100% via table-driven
  coverage of the empty-string UNSPECIFIED branch, every value in
  the terminal-list PASS branch — `Done`, `Resolved`, `Closed`,
  `Cancelled`, `Canceled`, `Completed` — and the default
  INCONCLUSIVE branch with non-terminal statuses), env-var +
  global-flag resolution (`resolveCommon` 0% → 100% via six
  branches: endpoint/token × flag/env/missing), both dial-transport
  branches (`dialConnectorRegistry` 0% → 87.5% — TLS + insecure
  both lazy-dial against 127.0.0.1:1 with no network IO), the
  `authedContext` metadata wiring (0% → 100% with Authorization
  header assertion + cancel-func verification), `sdkOpts` insecure
  vs secure paths (0% → 100%), `connectorVersion` smoke, `actorID`
  shape pinning across both jira and linear platforms, and the
  cross-platform `doRun` entry-branch coverage (0% → 90%): the
  unsupported-platform defense-in-depth branch via direct
  invocation, the runJira missing-`--jira-base-url` guard, the
  runJira `jiraauth.ResolveJira` error wrap when JIRA_EMAIL +
  JIRA_API_TOKEN are unset, and the runLinear
  `jiraauth.ResolveLinear` error wrap when LINEAR_API_KEY is unset
  (the only `runJira` / `runLinear` branches unit-coverable without
  a seam refactor — the post-resolve Pull + push loop sits behind
  `jiraauth`/`jiratickets`/`lineartickets` API calls and is
  exercised by the existing `integration_test.go` suite plus the
  self-host bundle e2e job). Mirrors the pattern slices 299 + 302
  - 303 established for `connectors/aws/cmd/aws-connector`,
    `connectors/okta/cmd/atlas-okta`, and
    `connectors/osquery/cmd/atlas-osquery` (the same "test what's
    testable without refactor" rule; the same architectural
    seam-refactor gap that slice 305 captures for AWS and that
    follow-on slices will capture for okta / jira if the seam refactor
    proves worth the carry). No vendor-prefixed Atlassian or Linear
    tokens appear in fixtures, per `CLAUDE.md`'s hard rule — neutral
    `test-*` strings only. Floor in
    `cmd/scripts/coverage-thresholds.json` ratchets from `28` to `69`
    per slice 069's `floor(measured - 2pp)` methodology. Spillover
    from slice 279's coverage audit
    (`docs/coverage-audit-2026-05.md`).
