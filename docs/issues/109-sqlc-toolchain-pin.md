# 109 — Pin the sqlc toolchain version so `sqlc generate` is reproducible across the tree

**Cluster:** Infra
**Estimate:** 0.25d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 106, captured as follow-up per continuous-batch policy.

Slice 106 (evidence list backend extension) needed a new sqlc query (`ListEvidenceForTenant`) added to `internal/db/queries/control_detail.sql`. The expected workflow is: edit `.sql`, run `sqlc generate`, commit the regenerated `internal/db/dbx/*.go`. The implementing engineer hit a toolchain-drift problem: their local `sqlc` (v1.31.1) regenerated DIFFERENT bytes for files that other slices had committed, because those earlier slices were generated with an earlier sqlc version. Running `sqlc generate` would have spuriously modified ~12 files in `internal/db/dbx/` that the slice did NOT touch logically.

The engineer's workaround: hand-mirror the generated method into `internal/db/dbx/control_detail.sql.go` + add a `Querier` interface entry by hand, instead of running `sqlc generate`. This works for the single new method but defeats the point of the codegen — future slices touching adjacent queries hit the same drift and resort to the same hand-editing pattern. The drift compounds over time until somebody does a clean regen + reviews the diff carefully (which is the painful CI fix that this slice prevents).

The fix is to pin sqlc to a single version in the repo so all contributors regenerate identically:

1. Pin `sqlc` to a specific version in `justfile` (or wherever the toolchain installs land) — likely `v1.31.x` or whatever the most-recent stable release is at slice-run-time.
2. Document the pin in `CLAUDE.md` (tech-stack table already names sqlc + Atlas) and `CONTRIBUTING.md` (the "Local CI parity" subsection mentions `sqlc generate` adjacent).
3. Run `sqlc generate` once with the pinned version against the current tree and commit the (presumably non-trivial) diff as a single `chore(sqlc): regenerate with pinned vX.Y.Z toolchain` commit. This is the painful one-time reset that makes the future state safe.
4. Optionally: add a `Go · sqlc generate diff` informational CI job (slice-069 stub-job pattern) that runs `sqlc generate` and `git diff --exit-code internal/db/dbx/` so any future drift trips an explicit failure during PR CI rather than at slice-execution time.

## Acceptance criteria

- [ ] AC-1: `justfile` (or equivalent toolchain manifest) pins sqlc to a specific version. The pin is the single source of truth; agents and contributors install from it.
- [ ] AC-2: `CLAUDE.md` tech-stack table updated with the pinned version (or a pointer to the justfile pin so the version lives in one place).
- [ ] AC-3: `CONTRIBUTING.md` "Local CI parity" subsection updated with the `sqlc generate` regen pattern + the pin reference.
- [ ] AC-4: Single `chore(sqlc): regenerate with pinned vX.Y.Z toolchain` commit lands the reset diff. The PR body lists the touched files + a one-line "no behavioral change — sqlc serializer drift only" note.
- [ ] AC-5: Optional `Go · sqlc generate diff` informational CI job added (slice-069 stub pattern) that fails on any `internal/db/dbx/` drift. NOT added to required-checks initially.
- [ ] AC-6: Decisions log at `docs/audit-log/109-sqlc-toolchain-pin-decisions.md` records the chosen pin version + rationale + the AC-4 diff scope.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes"** — the pin is a one-line config change; the regen is a one-time reset. No new abstractions.
- **No behavioral change** — sqlc regen affects file bytes, not runtime behavior.

## Canvas references

- `Plans/canvas/09-tech-stack.md` (sqlc + Atlas line)
- `internal/db/queries/*.sql` + `internal/db/dbx/*.go` (the touched surfaces)
- Slice 106 decisions log P1 entry (the surfacing context)

## Dependencies

- None — pure infra slice; no new functionality.

## Anti-criteria (P0)

- **P0-A1**: Does NOT change any sqlc query logic. The regen MUST be byte-identical to a clean run of the pinned version against the existing queries.
- **P0-A2**: Does NOT add a Querier method or DB call that the queries don't define. Pin + regen + document only.
- **P0-A3**: Does NOT promote the optional CI job (AC-5) to required-checks in this slice. Cadence + false-positive review first.
- **P0-A4**: Does NOT use vendor-prefixed tokens in any test fixture (carry-over convention).

## Skill mix

- `justfile` toolchain pin patterns (slice 069 has examples of pinning Go tools)
- sqlc version semantics (1.31.x vs 1.32.x serializer differences)
- `chore(sqlc): regenerate` commit hygiene (large generated-file diffs reviewed by file count rather than line-by-line)

## Notes for the implementing agent

- The regen diff (AC-4) is the load-bearing part — expect ~10-20 files in `internal/db/dbx/` to change. The behavioral assertion is the existing test suite continuing to pass; if any Go test breaks after regen, the pin candidate is wrong and a different version should be selected.
- Surface as a P1 finding in `docs/audit-log/106-evidence-list-backend-extension-decisions.md` when the regen lands — the slice-106 decisions log already references this gap and should be cross-linked.
