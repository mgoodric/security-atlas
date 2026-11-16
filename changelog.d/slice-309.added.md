- **test(coverage):** slice 309 — `connectors/okta/cmd/atlas-okta`
  doRun seam refactor + coverage lift from 64.9% to 91.5% merged. Third
  application of slice 305's seam-refactor pattern (after slice 305 for
  `connectors/aws/cmd/aws-connector` and slice 308 for
  `connectors/github/cmd/atlas-github`). Closes the gap slice 302
  documented in its `TestDoRun_FailsOnMissingOktaToken` comment: the
  post-`oktaauth.Resolve` body of `doRun` — the three Pull loops
  (`oktapolicy.Pull` for `okta.mfa_policy.v1`, `oktaapps.Pull` for
  `okta.app_assignment.v1`, `oktausers.Pull` for `okta.user_lifecycle.v1`)
  - the SDK Push calls — was not unit-coverable without a seam, and
    slice 302's hard rule (mirroring slice 299) forbade the refactor. Now
    the seam lands. Production-side `connectors/okta/cmd/atlas-okta/cmd_run.go`
    gains four package-level function variables
    (`oktapolicyPull`, `oktaappsPull`, `oktausersPull`, `newSDKClient`) and
    one narrow `sdkPushClient` interface (`Push` + `Close`); `doRun`
    reaches through the variables instead of the direct package calls.
    Externally observable behavior is unchanged — slice 045's Okta
    connector integration test (`connectors/okta/cmd/atlas-okta/integration_test.go`,
    no build tag, calls the lower-level packages directly without going
    through `doRun`) still passes; the self-host bundle e2e job still
    exercises the live path. The new
    `connectors/okta/cmd/atlas-okta/cmd_run_seam_test.go` adds nine unit
    tests (`TestDoRun_SDKClientError` for the `sdk client: ` wrap;
    `TestDoRun_MFAPolicyPullError`, `TestDoRun_AppAssignmentPullError`,
    `TestDoRun_UserLifecyclePullError` for the three pull-error wraps;
    `TestDoRun_PushSuccessAllThree` for the full happy path through six
    records across all three branches with `defer-Close` verification;
    `TestDoRun_MFAPolicyPushError`, `TestDoRun_AppAssignmentPushError`,
    `TestDoRun_UserLifecyclePushError` for the three push-error wraps
    pinning the `push mfa_policy <id>: ` / `push app_assignment <id>: ` /
    `push user_lifecycle <id>: ` prefix shape; `TestDoRun_SkipAllFlags` for
    the all-skip-flags-true path that constructs the sdk client, pushes
    zero records, and returns nil with `defer-Close` firing). Each test
    installs the relevant seam(s) via `t.Cleanup` so package-level vars
    are restored between tests. `doRun` jumps from 15.4% → 94.7% per
    function; total package coverage moves from 64.9% → 91.5% merged
    (+26.6pp). Floor in `cmd/scripts/coverage-thresholds.json` ratchets
    from `62` to `89` per slice 069's `floor(measured - 2pp)` methodology
    (monotonic ↑; only the okta cmd line is touched — siblings 308 + 310
    edit different lines in parallel). No vendor-prefixed tokens (the
    Okta `00...` 42-char API-token pattern) appear in fixtures, per
    `CLAUDE.md`'s hard rule — neutral `test-*` strings only. Closes
    [#309](docs/issues/309-coverage-okta-cmd-round2.md).
