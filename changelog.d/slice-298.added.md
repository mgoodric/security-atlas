- **test(coverage):** slice 298 — `connectors/aws/internal/awsauth`
  coverage lift from 66.7% to 97.2% merged (unit-only profile; the
  package has no integration tests so unit-only equals merged). A new
  `connectors/aws/internal/awsauth/awsauth_more_test.go` exercises the
  branches the existing `awsauth_test.go` (slice 004) leaves
  untouched: `Assume`'s success path (region + role-arn both set —
  wires `config.LoadDefaultConfig` + STS client + AssumeRoleProvider
  - `aws.NewCredentialsCache` without any network IO);
    `ResolveIdentity`'s STS-error wrap, empty-account-id guard, nil
    `OrgAPI` skip, Environment-tag-present-but-empty fall-through to
    flag, and no-Environment-tag-among-other-tags fall-through;
    `isAccessDenied`'s terminal `return false` for an `APIError` whose
    code (`ThrottlingException`) isn't in the access-denied list; and
    `OrgClient` / `STSClient` constructor non-nil smoke tests. Per-
    function coverage moves `Assume` 36.4% → 90.9%, `ResolveIdentity`
    88.2% → 100%, `isAccessDenied` 83.3% → 100%, `OrgClient` 0% → 100%,
    `STSClient` 0% → 100%. No vendor-prefixed AWS tokens
    (no `AKIA*` / `ASIA*` / etc.) appear in fixtures, per
    `CLAUDE.md`'s hard rule — neutral 12-digit dummy account ids
    (`111122223333`) and `test-*` strings only. Floor in
    `cmd/scripts/coverage-thresholds.json` ratchets from `64` to `95`
    per slice 069's `floor(measured - 2pp)` methodology. Spillover from
    slice 279's coverage audit (`docs/coverage-audit-2026-05.md`).
