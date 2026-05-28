# 340 — Investigate + re-enable chromedp `TestRender_ProducesRealPDF` flake

**Cluster:** Quality / Infra
**Estimate:** 1-2d (investigation)
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during batch 129 (slice 320 demoseed coverage lift): the
chromedp integration test `TestRender_ProducesRealPDF` at
`internal/policy/pdf/render_integration_test.go:24` has failed on
**5 consecutive CI runs** across batches 125-129:

| Slice | PR                          | Flake count                                            |
| ----- | --------------------------- | ------------------------------------------------------ |
| 312   | #714                        | 1× chromedp                                            |
| 314   | (oauth — not yet attempted) | —                                                      |
| 315   | #721                        | 2× chromedp                                            |
| 320   | #753                        | 4× chromedp (on a 5-attempt run) + 1× scheduler timing |

The error pattern is consistent:

```
--- FAIL: TestRender_ProducesRealPDF (20.04s)
FAIL	github.com/mgoodric/security-atlas/internal/policy/pdf	20.13s
```

20-second timeout suggests chromedp's headless-Chrome websocket
handshake never completes within the test deadline.

**Quarantine** landed in the same PR as this slice spec (or in a
sibling hotfix PR): `t.Skip("chromedp flake — see slice 340 for
re-enable")` at the top of the test.

**Disposition:** investigation + re-enable.

## Investigation hypotheses (ranked)

1. **StepSecurity Harden-Runner egress block (slice 117).** Slice 117
   tightened the runner's DNS/egress posture. chromedp may be making
   outbound DNS or HTTP requests that get audited but not blocked,
   and the audit-mode latency causes the 20s timeout. Audit-mode +
   slow runners is a known plausible failure mode.
2. **GitHub-hosted runner Chromium version drift.** The runner's
   default Chromium may have updated to a version chromedp 0.x is
   incompatible with. Check `chromedp` version in `go.mod` vs the
   shipped Chromium on the runner image.
3. **CI runner image regression.** GitHub may have updated
   `ubuntu-latest` between 2026-05-26 (when slice 312's CI was first
   green) and 2026-05-27 (when 4 consecutive runs failed). Check
   GitHub's runner-image changelog.
4. **chromedp test code is racy.** The test uses
   `chromedp.NewExecAllocator(ctx, chromedp.DefaultExecAllocatorOptions[:]...)`
   without explicit `chromedp.Flag("disable-dev-shm-usage", true)` or
   `chromedp.Flag("no-sandbox", true)` — both known requirements for
   running Chrome headless in CI containers.

## What ships in this slice

1. Diagnose root cause (hypothesis 1-4 above; iterate)
2. Apply the fix
3. Remove the `t.Skip` guard
4. Run the test 10 consecutive times in CI to verify it passes
5. Document the root cause in `docs/audit-log/340-chromedp-flake-decisions.md`

## Acceptance criteria

- [ ] **AC-1.** Root cause identified by name (one of 1-4 above OR a
      new hypothesis). Document in decisions log.
- [ ] **AC-2.** Fix applied (e.g., add Chrome flags / pin runner image /
      switch chromedp version / etc.)
- [ ] **AC-3.** `t.Skip` removed from `render_integration_test.go`
- [ ] **AC-4.** CI runs the test 10 consecutive times on a single PR
      without flaking. (Use a tight smoke loop in CI, or rerun the
      Go-integration job 10× on the unblock PR.)
- [ ] **AC-5.** Decisions log captures the diagnostic procedure +
      reproducer command, so future occurrences are diagnosable.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md):** the test exists to verify
  slice 022's AC-5 (PDF render via chromedp). Removing it would
  regress that AC. Quarantining (with a re-enable plan) preserves
  the gate.
- **No dependency on test-skip silencing real bugs.** AC-4's "10
  consecutive runs" stress-test is the unblock criterion, not just
  "passes once".

## Dependencies

- **#340 itself** must merge BEFORE #753 (slice 320 demoseed) can
  merge — that's the immediate driver. Quarantine is in this same PR.

## Anti-criteria (P0 — block merge)

- **P0-340-1.** Does NOT permanently delete the test. The
  `render_integration_test.go` file stays, with `t.Skip` as the
  temporary guard.
- **P0-340-2.** Does NOT swap chromedp for a stub renderer in
  production code. The runtime renderer keeps chromedp; only the
  test is quarantined.
- **P0-340-3.** Does NOT touch `internal/policy/pdf/render.go` (the
  production renderer). Quarantine is test-only.
- **P0-340-4.** Does NOT add a new dependency to dodge chromedp
  (e.g., switch to `gotenberg` or `weasyprint`) without an explicit
  ADR. That's a separate design decision.

## Skill mix

- CI runner investigation (GitHub Actions runner-image changelogs;
  StepSecurity Harden-Runner audit mode)
- chromedp / headless-Chrome configuration
- Slice 117 (StepSecurity), slice 022 (PDF render integration)
  context

## Notes for the implementing agent

**Reproducer (start here):**

```bash
# Run the integration test locally against the same Chromium version
# the GHA runner uses
cd internal/policy/pdf
go test -tags=integration -run TestRender_ProducesRealPDF -v ./...
```

If it passes locally but fails in CI, the issue is runner-specific
(hypothesis 1 / 2 / 3). If it fails locally too, the issue is in the
test code or chromedp version (hypothesis 4).

**Fastest unblock path:** add `chromedp.Flag("no-sandbox", true)` +
`chromedp.Flag("disable-dev-shm-usage", true)` + 60s test timeout (up
from 20s). These are the canonical "make chromedp work in CI"
incantations. If those don't fix it, escalate the investigation.

**Re-enable verification:** before removing `t.Skip`, push a draft
PR that runs the Go integration job 10 times via:

```yaml
strategy:
  matrix:
    iteration: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10]
```

on a temporary branch. All 10 must pass to consider the flake fixed.
