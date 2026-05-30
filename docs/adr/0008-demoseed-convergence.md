# ADR 0008 — Demoseed convergence for integration-test data

**Status:** Proposed — DESIGN ONLY. This ADR scopes the convergence; it does NOT migrate any seed. Execution is a follow-on slice (see "Migration plan" below).

**Date:** 2026-05-29

**Resolves (scopes):** slice 333 finding **Q-17** (`docs/audits/333-qa-strategy-gap-analysis.md`) — "three test-data realities is two too many." Reinforced by slice 334 finding **I-4** (15 `TestMain` blocks each creating their own pools + seeding package-local rows).

**Implements-the-scope-through:** `docs/issues/353-qa-strategy-tactical-round-1.md` (sub-theme D). Slice 353 ships this design doc; future slices execute it.

**Slot note:** Slots 0001–0007 occupied (0003 is a pre-existing double-occupied collision). Next free slot is 0008.

---

## Context

The project has three independent test-data realities:

| Reality                 | Where                                                       | Shape                                                                                |
| ----------------------- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------ |
| Per-package Go fixtures | 15 `TestMain` blocks across `internal/*` integration suites | Each builds its own `appPool` + `adminPool` and seeds package-local rows via INSERT. |
| `internal/demoseed`     | slice 205; `Apply` / `Teardown` / `NewSeeder`               | Global, idempotent demo dataset. Used by docker-compose bring-up + `e2e-audit/`.     |
| Playwright fixtures     | `web/e2e/fixtures.ts` (post-slice-201)                      | Real per-worker JWT, **mocked** upstream atlas responses (57 `route.fulfill`).       |

The asymmetry: a test asserting "the dashboard shows N controls" reads a
different N in each surface. The per-package seeds re-seed rows that
`demoseed` already owns canonically — SCF anchors, framework versions, the
default tenant identity row. This duplication is the seam where the `-p 1`
serial-integration collision surface lives (slice 334's analysis of
`evidence_kind_schemas`, `scf_anchors`, `evidence_records`).

`demoseed` is the natural canonical reality (slice 333 Strength #5): it
already exists, is idempotent, and has a measured 81% coverage floor
(slice 320). The convergence extends that investment rather than starting
a new one.

## Decision (scoped — design only)

Designate `internal/demoseed` as the canonical integration-test data reality
and converge per-package seeds onto it over **two cycles**. This ADR fixes
the SHAPE of that convergence; it does not perform it.

### (a) Which per-package seeds duplicate shared rows

The convergence target is the set of rows seeded redundantly across 2+
packages. From a grep of the `internal/*` integration suites, the shared-row
candidates are:

| Shared row class        | Owning canonical source   | Packages that re-seed it (candidates)                                         |
| ----------------------- | ------------------------- | ----------------------------------------------------------------------------- |
| `scf_anchors`           | demoseed anchor writer    | `internal/control`, `internal/oscal`, `internal/questionnaire`, `internal/db` |
| `framework_versions`    | demoseed framework writer | `internal/control`, `internal/frameworkscope`, `internal/db`                  |
| default `tenants` row   | demoseed tenant writer    | nearly every `TestMain` (the `app.current_tenant` anchor)                     |
| `evidence_kind_schemas` | demoseed schema registrar | `internal/evidence/*`, `internal/db`                                          |

The authoritative list is produced mechanically by the executing slice
(grep each `integration_test.go` for `INSERT INTO <shared_table>`); the
table above is the design-time candidate set, not the final inventory.

### (b) The two-cycle migration plan

**Cycle 1 — introduce `demoseed.ApplyMinimal()`.** A new exported helper
that seeds ONLY the rows shared across multiple packages (SCF anchors,
framework versions, default tenant, the baseline `evidence_kind_schemas`) —
the intersection above, not the full demo dataset. It is idempotent (same
contract as `Apply`) and composes: a package's `TestMain` calls
`ApplyMinimal()` for the shared substrate, then seeds only its
package-specific rows on top. `ApplyMinimal()` ships with its own
integration test and a coverage floor (slice 069 ratchet). No existing
`TestMain` changes in cycle 1 — the helper lands first, proven in isolation.

**Cycle 2 — migrate per-package seeds onto `ApplyMinimal()`.** Package by
package (one slice per cluster, or a small batch), replace the redundant
shared-row INSERTs in each `TestMain` with a single `ApplyMinimal()` call,
keeping only the package-specific seed rows. Each migration slice (i) proves
the package's integration suite still passes against the converged substrate,
(ii) does NOT change any assertion's expected N (or, if N must change because
the canonical substrate differs from the old hand-seeded one, records the
delta explicitly). Tests needing a deterministic minimal state that
`ApplyMinimal()` does not provide use `Teardown`-then-seed-fresh rather than
fighting the shared substrate.

### (c) The `-p 1` collision-surface mapping

The convergence's payoff is at exactly the `-p 1` serial collision surface.
Today the integration job runs `-p 1` (serial across packages) in part
because multiple packages INSERT into the same shared tables and would race
under parallelism. Mapping:

| Collision table         | Today (`-p 1` reason)                                                      | After convergence                                                             |
| ----------------------- | -------------------------------------------------------------------------- | ----------------------------------------------------------------------------- |
| `scf_anchors`           | N packages INSERT overlapping anchor IDs → unique-violation under parallel | `ApplyMinimal()` is the single idempotent writer; packages read, don't write. |
| `framework_versions`    | same                                                                       | same — single writer.                                                         |
| `tenants` (default)     | every package re-creates the anchor tenant                                 | `ApplyMinimal()` owns it; RLS context set against the canonical row.          |
| `evidence_kind_schemas` | parallel registrars collide                                                | single registrar in `ApplyMinimal()`.                                         |

This does NOT by itself unlock parallel integration — RLS context,
per-test data isolation, and pool-per-package concerns remain — but it
removes the shared-write collision as one of the `-p 1` justifications, which
is a prerequisite for the future Phase-A/B split (slice 334's relaxation
path, tracked by the Q-8 wall-clock watermark, ADR-adjacent `docs/integration-wallclock.tsv`).

## Anti-criteria (this ADR / slice 353)

- **Does NOT** add `ApplyMinimal()` — that is cycle-1 execution work.
- **Does NOT** modify any `TestMain` or per-package seed.
- **Does NOT** change the `-p 1` flag (slice 334's load-bearing decision stands).
- **Does NOT** add mocks to the integration tier (Article IX; slice 353 P0-3).

## Consequences

- The executing slices inherit a fixed shape: `ApplyMinimal()` first
  (cycle 1), then per-package migration (cycle 2). No re-litigation of the
  canonical-reality choice.
- The Playwright/`e2e-audit` fixture convergence (slice 333 Q-18) is a
  separate downstream that composes with this once the Go side converges;
  it is tracked under the Q-10 ui-honesty promotion path (ADR 0009), not here.

## Follow-on slice

File a cycle-1 slice (`demoseed.ApplyMinimal()` + its integration test +
coverage floor) when this ADR is accepted. The cycle-2 per-package migration
slices follow once `ApplyMinimal()` is on main.
