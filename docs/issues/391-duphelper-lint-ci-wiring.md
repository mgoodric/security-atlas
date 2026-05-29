# 391 — Wire `duphelper-lint` into CI as a hard-failure step

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** MECHANICAL
**Status:** `ready`

## Narrative

Surfaced during slice 369, captured per continuous-batch policy.

Slice 369 created `cmd/scripts/duphelper-lint` — a `golang.org/x/tools/go/analysis`
analyzer (mirror of slice 367's `errleak-lint`) that rejects new package-local
`writeJSON` / `writeError` / `writeServerErr` free-function declarations in
`internal/api/*`, the regression guard for the H-1 helper consolidation. Slice
369 wired it into the `justfile` `lint` target (`lint-duphelper`) but could NOT
wire it into `.github/workflows/ci.yml`: batch-164 reserved `ci.yml` for slice
345 that batch to avoid a merge collision (the batch directive explicitly
instructed deferring the CI lint-guard to a spillover slice).

This slice adds the CI step so the guard is enforced as a hard failure on every
PR, completing slice 369 AC-4.

## What ships

A new step in the `lint-go` job of `.github/workflows/ci.yml`, immediately after
the existing slice-367 errleak step (lines ~523-524), mirroring it exactly:

```yaml
- name: Go · duphelper (slice 369 H-1 guard)
  run: go run ./cmd/scripts/duphelper-lint ./internal/api/...
```

A hit exits 3 and fails the CI check, identical to the errleak step's contract.

## Acceptance criteria

- [ ] **AC-1.** `.github/workflows/ci.yml` `lint-go` job runs `go run ./cmd/scripts/duphelper-lint ./internal/api/...` as a hard-failure step.
- [ ] **AC-2.** The step mirrors the slice-367 errleak step's shape (same `go run` invocation form, same `setup-go` cache reuse).
- [ ] **AC-3.** `pre-commit run --all-files` passes; CI green on the PR.

## Dependencies

- **#369** (httpresp consolidation + duphelper-lint analyzer) — must merge first; this slice references the analyzer it created.
- **#345** (ci.yml owner this batch) — sequence after to avoid the merge collision slice 369 was steering around.

## Anti-criteria

- **P0-391-1.** Does NOT change the analyzer logic — CI wiring only.
- **P0-391-2.** Does NOT auto-merge.

## Notes

Trivial mechanical addition. Confirm the `lint-go` job's `setup-go` step is in
scope (the errleak step reuses the same cached toolchain — the new step sits
beside it and inherits the same setup).
