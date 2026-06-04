# 305 — Coverage lift (round 2) — `connectors/aws/cmd/aws-connector` doRun to 70%+

**Cluster:** Quality
**Estimate:** 1-2d (small package; requires a seam refactor)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 299. Slice 299 lifted
`connectors/aws/cmd/aws-connector` from 9.0% merged to 65.8% merged
by covering every branch reachable without refactoring production
logic (cobra glue + enum mapping + env-var resolution + dial/auth
helpers + the doRun entry-error path via a cancelled context).

The remaining gap — ~6pp short of the 70% target — sits entirely in
`doRun`'s post-`ResolveIdentity` body: the S3 inspection call and
the per-record push loop. Slice 299's hard rule explicitly forbade
refactoring production logic just to hit a coverage number, so the
push-loop and S3-inspect branches stay uncovered at unit-level. They
ARE exercised by the self-host bundle e2e job (the connector is
booted against a localstack S3 and a real platform binary), so the
behavior is integration-tested — just not counted by the coverage
gate.

**Disposition:** `unit-add` (requires a small seam refactor)

**Notes:** the seam shape is intentional and small — introduce
package-level function variables (`var inspectFn = awss3.Inspect`,
`var newSDKClientFn = sdk.NewClient`) that doRun reads via, so the
test can swap them for fakes. This is the same pattern used by the
GitHub + Okta connectors' cmd packages (which sit at 13% + 18%
respectively — both are spillover candidates with the same shape).

## What ships in this slice

1. **Minimal seam refactor** in `cmd_run.go` introducing one or two
   `var fn = realFn` package-level indirections so doRun can be
   driven against fake awss3/sdk implementations.
2. **New unit tests** under `connectors/aws/cmd/aws-connector/*_test.go`
   covering the S3-inspect-error, push-error, and push-success
   branches of doRun.
3. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   the current `63` to `floor(measured - 2pp)`.

The two changes ship in the SAME PR per slice 069's ratchet
contract.

## Acceptance criteria

- [ ] **AC-1.** New unit tests for `connectors/aws/cmd/aws-connector`
      move its merged coverage to ≥ 70%.
- [ ] **AC-2.** Each test exercises real branches with real
      assertions (no vacuous patterns).
- [ ] **AC-3.** Each new test file's first comment block names the
      package's load-bearing functions + the branches the file is
      designed to cover.
- [ ] **AC-4.** `coverage-thresholds.json` ratchets the
      `connectors/aws/cmd/aws-connector` floor to
      merged-measured minus 2pp.
- [ ] **AC-5.** The seam refactor is minimal (one or two
      `var fn = realFn` indirections); no architectural changes
      to doRun's control flow.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md "Testing discipline" section).**
  Ratchet contract: no floor raised without test added.
- **Slice 069 methodology.** Floors ratchet at
  `max(0, floor(measured - 2pp))`.

## Dependencies

- **#299** (round-1 coverage lift) — must merge first; this slice
  flips from `not-ready` to `ready` after #299 lands.

## Anti-criteria (P0 — block merge)

- **P0-305-1.** Does NOT raise the
  `connectors/aws/cmd/aws-connector` floor without writing the
  unit tests that hit the new bar.
- **P0-305-2.** Does NOT lower any existing floor — every change to
  `thresholds` is monotonically ↑.
- **P0-305-3.** Does NOT modify `_STATUS.md` from inside this
  slice's own commits — orchestrator's surface.
- **P0-305-4.** Does NOT alter doRun's externally observable
  behavior — the seam is a refactor, nothing more.

## Notes for the implementing agent

Slice 299 left the per-function gap as:

```
doRun     15.2%   (only the awsauth.Assume + ResolveIdentity entry
                   covered via TestDoRun_FailsFastOnAlreadyCancelledContext)
main      0.0%    (uncoverable — calls os.Exit)
```

To close the gap without touching control flow, introduce one or
two seam vars at file scope in `cmd_run.go`:

```go
var (
    awsauthAssume       = awsauth.Assume
    awsauthResolveID    = awsauth.ResolveIdentity
    awss3Inspect        = awss3.Inspect
    newSDKClient        = sdk.NewClient
)
```

Tests then assign fakes inside `t.Cleanup`-guarded restorers and
drive doRun directly. Aim for ~75% on doRun (the `defer cancel()`
and a few error-wrap statements stay uncovered, which is fine).

The merged target on the package is 70%+; with main.go's 5
permanently-uncoverable statements out of ~111 total, the practical
ceiling is ~95%.
