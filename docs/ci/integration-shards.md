# Integration-job sharding (slice 417)

The `Go · integration` tier used to be a single serial job —
`go test -tags=integration -p 1 ./internal/...` over ~89 packages — and
the merge-queue wall-clock bottleneck (~9-13 min, re-run on every
update-branch). Slice 417 **shards it across runners**. This document is
the reference for the package→leg assignment rule and the guards that keep
it honest.

## What sharding is (and is not)

- **Sharding** = split the package set across multiple GitHub Actions
  runners (a `strategy.matrix`). Each leg is a **separate runner with its
  own Postgres + MinIO + NATS + seed**, so two parallel legs cannot race
  on the shared platform-layer rows the old `-p 1` serialized — they have
  separate databases.
- **NOT `-p N`.** Slice 334 settled `-p N` (in-process parallelism on one
  runner against one shared DB) as out of scope. **Every leg still runs
  `-p 1` internally.** The parallelism is across machines, not within one.

See `docs/audits/334-test-framework-review.md` ("-p 1 rationale review")
for why `-p 1` is load-bearing within a single shared DB.

## The legs

Slice 747 re-balanced the legs for wall-clock (time-to-live), splitting two
monoliths that were the critical-path floors and adding two parallel legs
(B4, B5). The drivers, measured on a 2026-06-14 main run, were Leg A's
`internal/api/controls` (~228s) and Leg B3's four-package OSCAL import cluster
(profileimport ~122s + oscal ~83s + componentimport ~81s + catalogimport
~61s ≈ ~347s). See "Re-balancing" below for the methodology.

| Leg    | What                                                                                                                                                                                                                     | Serial? | Own DB? |
| ------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------- | ------- |
| **A**  | Shared-seed / platform-layer cluster: the SCF global-catalog seeders + the eval/control consumers coupled to the freshly-seeded catalog. (Slice 747 moved `api/controls` — a self-seeding, non-pinned consumer — off A.) | `-p 1`  | yes     |
| **B1** | Auth + admin handler families (OAuth AS, OIDC, users, tenants, super-admins, creds, SSO).                                                                                                                                | `-p 1`  | yes     |
| **B2** | Audit-workflow + board + metrics + policy + risk + vendor.                                                                                                                                                               | `-p 1`  | yes     |
| **B3** | Heavy OSCAL imports (profileimport + catalogimport) + cosign + questionnaire + dashboard/artifact read handlers.                                                                                                         | `-p 1`  | yes     |
| **B4** | `api/controls` (the ~228s monolith moved off A) + cross-cutting read handlers (mcp, llm, freshness, drift, staleness, exception, observability, platform).                                                               | `-p 1`  | yes     |
| **B5** | The other half of the OSCAL import cluster (`oscal` + componentimport) + scope/feature read handlers.                                                                                                                    | `-p 1`  | yes     |

## The assignment rule

- **Leg A** = packages that seed or import the **global SCF catalog**
  (`scf_anchors`, which has **no `tenant_id`**) and/or the catalog rows of
  `evidence_kind_schemas`, plus the eval/control consumers tightly coupled
  to that freshly-seeded catalog. These genuinely collide cross-binary on
  the platform layer, so they stay on one serial leg (P0-2). Leg A is also
  where the migration round-trip (slice 297), the slice-461
  order-independence guard, the slice-417 shard-coverage guard, and the
  slice-393 wall-clock watermark run — **once**, not per shard.
- **Leg B1/B2/B3/B4/B5** = the **tenant-scoped** handler families (plus the
  self-seeding, non-pinned OSCAL-import + `api/controls` packages, which
  create their own `scf_anchors` fixtures and never truncate/globally reseed
  the catalog). Their isolation is the transaction-scoped
  `SET LOCAL app.current_tenant` RLS pattern (slice 334 documented this as
  the **real**, non-`-p 1` isolation mechanism), so they do not collide
  cross-binary on the platform layer. They run on separate runners in
  parallel, each still `-p 1` internally.

The authoritative package→leg map is **`scripts/integration-shards.txt`**
(the source of truth). The CI matrix reads it via
`scripts/run-integration-shard.sh <LEG> <coverprofile>`, which always
passes `-p 1`.

## The guards (why a bad split cannot ship)

A bad shard split is a coverage/correctness-integrity risk: a package can
fall through the split and run in **no** leg (silently untested), or
coverage can fragment so the slice-279 ratchet under-counts. Two automated
guards close that:

1. **`scripts/check-integration-shard-coverage.sh`** (CI job: the
   `Assert shard-coverage union completeness (slice 417)` step on Leg A;
   `just check-integration-shard-coverage`). Proves:
   - **Union completeness (T-1 / P0-1):** the union of all legs ==
     the `//go:build integration`-tagged package set **exactly** (the same
     extraction the slice-345 enrolment guard uses). No tagged package is
     assigned to no leg.
   - **Disjointness (T-2):** no package is on more than one leg.
   - **Phase-A pin (P0-2 / T-2):** the catalog-seed cluster is pinned to
     Leg A so a re-balance cannot move a seeder into a parallel Phase B
     shard.
     Self-test: `scripts/check-integration-shard-coverage_test.sh`.
2. **`scripts/audit-integration-enrolment.sh`** (slice 345) — now reads
   the package list from **both** ci.yml and the shard manifest, so the
   "every tagged package is enrolled somewhere" guard keeps working after
   the package list moved into the manifest.

## Coverage fan-in (slice 279 ratchet preserved)

Each leg uploads a distinct coverage artifact
(`go-integration-coverage-<leg>`, one per leg A/B1/B2/B3/B4/B5). The fan-in
`tests-integration` job downloads the unit profile + **every** leg's
integration profile (the `download-artifact` pattern `go-integration-coverage-*`
picks up the new legs automatically), merges
them all with `gocovmerge`, and runs the slice-279 per-package gate on the
**union**. Running the gate on a single leg's fragment would under-count
(threat T-3); the merge is load-bearing, not polish. This slice preserves
the union-coverage number — it does **not** lift any floor.

## The required check is unchanged

The branch-protection contexts list still names exactly
`Go · integration (Postgres RLS)`. That name is now reported by the
**fan-in** job, which is green only when every leg passed (a fail on any
leg fails the required check). The per-leg `Go · integration (shard ...)`
checks are **not** required and are **not** in branch protection — so no
maintainer branch-protection update is required for this slice.

## Re-balancing

If one shard becomes the persistent critical-path leg (visible in the
slice-393 watermark `docs/integration-wallclock.tsv`, where each row is
now stamped per-leg), move package args between the B legs in
`scripts/integration-shards.txt`. The coverage-union guard makes the move
safe: it fails CI if you drop a package or double-assign one.

**Balance for TIME, not package count (slice 747).** Package count is a
proxy at best — a single heavy package can dominate a leg. On a 2026-06-14
main run the slowest leg was A at ~814s with only 24 packages, because
`internal/api/controls` alone ran ~228s; B3 ran ~762s because four OSCAL
import packages summed to ~347s of test time. Slice 747:

1. **Measured the split.** Read per-step timings from the leg's CI job
   (`gh api repos/<owner>/<repo>/actions/jobs/<id>` → `.steps[]`) and the
   per-package `ok ... <pkg> <seconds>` lines from the job log. This showed
   Leg A is **execution-bound, not seed-bound**: its SCF seed + migrations
   were only ~47s; the 800s was per-package test execution (`controls` 228s
   - `anchors` 68s) plus the ~128s Leg-A-only order-independence guard.
2. **Moved the two monoliths off their critical-path legs.** `api/controls`
   (self-seeding, NOT in the P0-2 pinned catalog cluster) moved to a new
   Leg B4; the four-package OSCAL import cluster was split across B3 (heavy
   profileimport + catalogimport) and a new Leg B5 (oscal + componentimport).
3. **Added parallel legs (B4, B5).** Runner-minutes are free for OSS, so
   adding legs is a free wall-clock lever; the maintainer prioritizes
   time-to-live over billing. Adding a leg means: this manifest, the leg
   `case` in `scripts/run-integration-shard.sh`, and **both** ci.yml
   `matrix.leg` lists (the PR-time `tests-integration-shard` matrix and the
   `tests-integration-main-canary` matrix) — but **NOT** branch protection
   (see "The required check is unchanged").

A floor caveat: a leg cannot go below its irreducible overhead — for Leg A
that is the ~47s seed/migration setup + the ~128s order-independence guard

- the SCF-coupled packages that P0-2 pins there. There is no point driving
  the parallel B legs below A's floor; balance them **to** it.

**Recursive-glob foot-gun.** `./internal/oscal/...` expands **recursively**
to `internal/oscal` + all its subpackages (catalogimport, componentimport,
cosign, profileimport). `go test` deduplicates within one invocation, so
the recursive form was safe when one leg owned the whole `oscal` tree. Once
the tree is split across legs, the recursive glob on one leg silently
re-runs the subpackages assigned to another leg (a cross-leg double-run the
token-level coverage guard does NOT catch, because it normalizes
`./internal/oscal/...` and `./internal/oscal` to the same token). Slice 747
addresses the top-level package as the non-recursive `./internal/oscal`
(no `/...`) on Leg B5 for exactly this reason. When splitting any package
tree across legs, prefer explicit non-recursive args and verify with:
`comm -12 <(xargs go list -tags=integration <legX args>) <(... legY ...)`
returns empty.
