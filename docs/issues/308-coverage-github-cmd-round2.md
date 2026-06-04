# 308 — Coverage lift (round 2) — `connectors/github/cmd/atlas-github` doRun + doWebhook to 70%+

**Cluster:** Quality
**Estimate:** 1-2d (requires seam refactor per slice 305 pattern)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 301 (`connectors/github/cmd/atlas-github`
15.1% → 58.8%, 11pp short of 70%). The remaining gap sits entirely
in `doRun`'s post-`githubauth.Resolve` body (7.0% coverage) plus
`doWebhook`'s `ListenAndServe` (0% coverage). Slice 301 was barred
from doing a seam refactor by its hard rule.

Slice 305 established the seam refactor pattern for the AWS connector
(`var inspectFn = realFn` + narrow client interface, +
`cmd_run_seam_test.go` driving doRun against fakes). The same
pattern applies cleanly to github: the github SDK's `Push` /
`ListRepos` / `ListSCIMUsers` / `ListAuditEvents` calls + the
webhook server can be hidden behind package-level fn-vars + a
narrow interface.

**Disposition:** seam refactor + unit-add

## What ships in this slice

1. **Minimal seam refactor** in
   `connectors/github/cmd/atlas-github/cmd_run.go` (or whatever
   files own doRun + doWebhook):
   - Package-level fn-vars for the SDK call sites (mirror slice
     305's `awss3Inspect`, `newSDKClient` shape).
   - One or two narrow interfaces for clients the loop calls
     into (analogue of `sdkPushClient`).
   - Same `t.Cleanup`-wrapped override pattern in tests.
2. **New `cmd_run_seam_test.go`** driving doRun + doWebhook against
   the fakes.
3. **Floor ratchet** in `cmd/scripts/coverage-thresholds.json` from
   56 to `floor(measured - 2pp)` (monotonically ↑).

## Acceptance criteria

- [ ] **AC-1.** Merged coverage of
      `connectors/github/cmd/atlas-github` lifts to ≥ 70%.
- [ ] **AC-2.** Tests exercise real doRun branches (push success,
      push failure, scim list, audit events list) + doWebhook
      branches (listener setup, request handling, error path).
- [ ] **AC-3.** Seam refactor is minimal: only the fn-vars + interfaces
      needed to drive the tests. No broader cleanup of slice 301's
      existing tests.
- [ ] **AC-4.** Floor ratchets to `floor(measured - 2pp)`.

## Constitutional invariants honored

- Testing discipline (CLAUDE.md): floor + tests in same PR.
- Slice 069 ratchet methodology.
- Slice 305 seam pattern: production code stays externally
  observable-equivalent; the fn-var indirections are
  internal-only.

## Dependencies

- **#301** (github cmd round 1) — merged at `5131f95f`.
- **#305** (aws-connector seam pattern, the pre-spec) — merged at
  `b9868ede`.

## Anti-criteria (P0 — block merge)

- **P0-308-1.** Does NOT broaden the seam refactor beyond what's
  needed for the test. No package re-architecture.
- **P0-308-2.** Does NOT lower any existing floor.
- **P0-308-3.** Does NOT modify `_STATUS.md` from inside the slice.
- **P0-308-4.** Does NOT use vendor-prefixed tokens (`ghp_*`,
  `github_pat_*`); use neutral `test-*` strings.
- **P0-308-5.** Does NOT break slice 044's GitHub connector
  integration test (the package's existing real-API smoke).

## Skill mix

- Slice 305 seam pattern (mandatory reading)
- Slice 301 test file (mandatory reading)
- httptest for the webhook listener

## Notes for the implementing agent

Slice 305 is the canonical template. The github cmd shape diverges
slightly because it has a webhook server (doWebhook) on top of the
push loop (doRun). Both need their own seams. Consider whether one
test file or two is cleaner.
