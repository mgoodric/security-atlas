# 406 — Integration-enrolment drain · batch 6 (auth substrate + keystore)

**Cluster:** infra
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `not-ready`
**Parent:** 390 (drain the 38-package integration-enrolment backlog)

## Narrative

Drain batch 6 of slice 390. Enrols the next group of packages
carrying `//go:build integration` tests that have never run in CI (slice 345
catalog). Same procedure as batch 1 (slice 401); see 390 for full context.

`not-ready` until slice 405 (the prior drain batch) merges — batches run
in sequence so each PR's CI stays green and the failure-rate learned from
earlier batches informs the later ones.

## Packages in this batch (4)

```
internal/auth
internal/auth/keystore/fsstore
internal/mcp
internal/observability/otel
```

## Per-package procedure (390 §Approach — same as slice 401)

For EACH package: (1) add its `./internal/<pkg>/...` entry to the integration
job's package list in `.github/workflows/ci.yml`; (2) run its integration
suite locally (`go test -tags=integration -p 1 ./internal/<pkg>/...`) and FIX
any silently-broken test correctly (no skip/delete; spillover a genuine
product bug rather than mask it); (3) remove it from `KNOWN_UNENROLLED` in
`scripts/audit-integration-enrolment.sh` in the SAME PR (monotonic shrink);
(4) if also on `cmd/scripts/coverage-thresholds.json` `excludes`, lift to a
per-package floor (slice 348 track 2).

## Acceptance criteria

- [ ] **AC-1.** All packages in this batch enrolled in the integration job.
- [ ] **AC-2.** Each package's integration suite GREEN in CI (real fixes, no skips).
- [ ] **AC-3.** All removed from `KNOWN_UNENROLLED`; allowlist shrinks by this batch's count.
- [ ] **AC-4.** Any package also on coverage `excludes` lifted to a per-file floor.
- [ ] **AC-5.** `pre-commit run --all-files` + CI green.

## Dependencies

- **#390** (parent tracking slice).
- **#405** (prior drain batch) — must merge first; this batch flips to
  `ready` when 405 lands.

## Anti-criteria (P0)

- **P0-406-1.** Does NOT skip/delete a failing integration test to reach green.
- **P0-406-2.** Does NOT grow `KNOWN_UNENROLLED` — monotonic shrink only.
- **P0-406-3.** Enrols ONLY this batch's packages.
