# Slice 353 — QA strategy tactical round 1 — decisions log

**Slice:** 353 (`docs/issues/353-qa-strategy-tactical-round-1.md`)
**Type:** AFK (the slice doc carries judgment-shaped calls — component-test
scope, retry policy, exclude-justification phrasing — so this log records
them per the per-slice-template convention even though the slice is AFK).
**Date:** 2026-05-29
**Input contract:** slice 333 QA audit (`docs/audits/333-qa-strategy-gap-analysis.md`).
**Cross-ref:** slice 334 (`docs/audits/334-test-framework-review.md`).

## Detection-tier classification (Q-13 — applied to this slice)

- detection_tier_actual: none
- detection_tier_target: none

No bug surfaced during this slice — it is documentation, convention, and
advisory-tooling work with no runtime code path. The three new scripts are
self-tested; all self-tests pass on first authoring. Nothing leaked to a
later tier.

## Decisions made

### D1 — Q-3 component-test surface: decided OUT of scope (option b)

The slice doc offers (a) add a vitest `happy-dom` React component project,
or (b) document that the e2e tier is the de facto component tier and
component-vitest is out of scope. **Chose (b).** Rationale: `web/vitest.config.ts`
is deliberately `environment: "node"` (slice 069 P0-A3); introducing a second
DOM-env project is real new surface (config, deps, a second ratchet) for a
gap the e2e tier already covers. v1's accepted cost is "a component regression
costs an e2e spec's ~3-5s." The revisit trigger (component churn making e2e
wall-clock the bottleneck) is named in CLAUDE.md. **Confidence: high** —
matches the project's existing "no DOM in vitest" stance.

### D2 — Q-16 integration retry policy: decided NO retry (investigate every flake)

The slice doc offers `-retry 1` for the Go integration tier OR explicit
"investigate every flake" discipline. **Chose: no retry.** Rationale: the Go
integration tier's flakes are usually real bring-up ordering bugs (NATS
startup, `pg_isready`, MinIO bucket-create races — see the slice-200,
slice-202 MEMORY entries) worth fixing once. A retry would mask them. The
Playwright tier keeps its `retries: 1` because browser/timing flake there is
less often a real product bug. The slice-352 flake dashboard makes the
"investigate now?" call mechanical. **Confidence: high** — codifies existing
practice (the project already investigates every integration flake).

### D3 — Q-5 exclude-justification SHAPE: parallel `$`-prefixed map, not array-of-objects

The slice doc says "each exclude has a `$justification` field," which reads
like converting `excludes: ["a/", …]` to `excludes: [{path, $justification}, …]`.
**Rejected that** because `cmd/scripts/coverage-gate/main.go` parses
`Excludes []string` — changing the array element type would break the gate
(and risk P0-4). **Chose:** keep `excludes` as `[]string` and add a parallel
top-level `$exclude_justifications` map (`prefix → justification`). Go's
`encoding/json` ignores unknown `$`-prefixed fields, so the gate is byte-for-
byte unaffected (verified: `go build ./cmd/scripts/coverage-gate` passes,
round-trip parse OK). The lint enforces both-direction parity. **Confidence: high.**

### D4 — Q-5 justification CONTENT: category-derived, not per-package hand-authored

61 excludes. Hand-authoring 61 bespoke rationales would be high-effort and
error-prone. **Chose:** classify each exclude mechanically (generated /
sqlc / protoc / CLI-tooling / integration-tested-handler / thin-wrapper) and
apply a category justification, anchored to the file's existing
`$exclusions_comment`. The justifications are accurate at the category level;
the quarterly retirement pass (the Q-5 maintenance task) sharpens any that
need per-package precision. **Confidence: medium** — the category mapping is
correct for the dominant cases; a handful of edge packages may warrant a
finer justification at the first quarterly review (noted in revisit list).

### D5 — Q-5/Q-6/Q-8 CI wiring: deferred to a spillover slice

The slice doc's AC-2/AC-3/AC-4 say "CI lint/step added." My batch-166 brief
forbids touching `.github/workflows/ci.yml` (collision avoidance — only a
possibly-392 slice owns ci.yml this batch). **Chose:** ship the tooling
(scripts + self-tests + justfile targets, all passing locally) in this slice,
and defer the ci.yml hard-fail wiring to a spillover slice — exactly the
slice 369 → 391 precedent (369 built `duphelper-lint` + `just lint` guard;
391 wired the CI step in a later batch). The scripts are runnable today via
`just`. **Confidence: high** — established precedent, avoids the collision
the brief warns about. The three ACs are therefore "tooling shipped + locally
verified; CI step in spillover," not "ci.yml edited here."

### D6 — Q-8 wall-clock: a RECORDER, not a test assertion

Per the slice-381 lesson (no single-sample wall-clock assertions), the
wall-clock script records to an append-only watermark and only WARNS at the
20-min trigger — it never fails on a timing sample. The self-test uses
RECORD mode (`WALLCLOCK_SECONDS`) so it is fully deterministic and times
nothing. **Confidence: high.**

### D7 — Q-6 assertion-density: advisory-only, never fails

Mirrors the slice-350 security-critical coverage advisory stance: the
density proxy is noisy enough that hard-failing on it would generate
false-positive merge blocks. Exit 0 even with warnings; exit 2 only on
misconfig. **Confidence: high.**

### D8 — Q-17 / Q-10: ADRs at slots 0008 / 0009, design/plan only

Both are ADRs (the project's "new architectural decision → ADR" discipline)
rather than slice docs, because they record a decided SHAPE for future
execution, not an immediate build. Slots 0008/0009 are the next free numbers
(0003 has a pre-existing double-occupancy this slice does not touch).
Anti-criteria P0-1/P0-2 honored: no seed migrated, no gate promoted.
**Confidence: high.**

### D9 — Spillover number for the CI-wiring follow-on

Next free issue number computed via the slice-doc formula. 392 was already
filed (slice 349's spillover, per `_STATUS.md`); this slice files the next
free number. If a parallel batch-166 engineer races the same number, the
orchestrator de-collides per continuous-batch policy. **Confidence: high.**

## Revisit once in use

1. **(D4) Quarterly exclude-retirement pass.** At the first quarterly review,
   walk the `$exclude_justifications` map; sharpen any category-level
   justification that is too coarse for a specific package, and retire any
   exclude whose package now has a hand-written unit floor.
2. **(D1) Component-test surface.** Revisit if component-level churn ever
   makes the Playwright wall-clock the dev-feedback bottleneck — at that
   point a `happy-dom` vitest project becomes worth its weight.
3. **(D5) CI-wiring spillover.** Verify the spillover slice actually wires
   the three scripts as CI steps once it lands; until then they are
   `just`-only and run on demand, not on every PR.
4. **(Q-6) Assertion-density threshold tuning.** The 1-per-20-LOC default is
   a guess; once run across the whole tree, tune `DENSITY_LOC` / `DENSITY_MIN_LOC`
   to the project's actual distribution before considering any promotion
   beyond advisory.
5. **(Q-8) First OVER_TRIGGER crossing.** When the watermark first records
   `OVER_TRIGGER`, file the Phase-A/B split slice (ADR 0008's collision
   mapping is the prerequisite analysis).

## Confidence summary

| Decision                           | Confidence |
| ---------------------------------- | ---------- |
| D1 component-test out-of-scope     | high       |
| D2 no integration retry            | high       |
| D3 parallel justification map      | high       |
| D4 category-derived justifications | medium     |
| D5 CI wiring → spillover           | high       |
| D6 wall-clock recorder not assert  | high       |
| D7 assertion-density advisory      | high       |
| D8 ADRs design/plan only           | high       |
| D9 spillover numbering             | high       |
