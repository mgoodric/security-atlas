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

| Leg    | What                                                                                                                                                  | Serial? | Own DB? |
| ------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- | ------- | ------- |
| **A**  | Shared-seed / platform-layer cluster: the SCF global-catalog seeders + the eval/control consumers coupled to the freshly-seeded catalog.              | `-p 1`  | yes     |
| **B1** | Auth + admin handler families (OAuth AS, OIDC, users, tenants, super-admins, creds, SSO).                                                             | `-p 1`  | yes     |
| **B2** | Audit-workflow + board + metrics + policy + risk + vendor.                                                                                            | `-p 1`  | yes     |
| **B3** | Dashboard + evidence-read + remaining `api/*` read handlers + oscal + cross-cutting (mcp, observability, drift, freshness, exception, questionnaire). | `-p 1`  | yes     |

## The assignment rule

- **Leg A** = packages that seed or import the **global SCF catalog**
  (`scf_anchors`, which has **no `tenant_id`**) and/or the catalog rows of
  `evidence_kind_schemas`, plus the eval/control consumers tightly coupled
  to that freshly-seeded catalog. These genuinely collide cross-binary on
  the platform layer, so they stay on one serial leg (P0-2). Leg A is also
  where the migration round-trip (slice 297), the slice-461
  order-independence guard, the slice-417 shard-coverage guard, and the
  slice-393 wall-clock watermark run — **once**, not per shard.
- **Leg B1/B2/B3** = the **tenant-scoped** handler families. Their
  isolation is the transaction-scoped `SET LOCAL app.current_tenant` RLS
  pattern (slice 334 documented this as the **real**, non-`-p 1` isolation
  mechanism), so they do not collide cross-binary on the platform layer.
  They run on separate runners in parallel, each still `-p 1` internally.

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
(`go-integration-coverage-<leg>`). The fan-in `tests-integration` job
downloads the unit profile + **every** leg's integration profile, merges
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
now stamped per-leg), move package args between B1/B2/B3 in
`scripts/integration-shards.txt`. The coverage-union guard makes the move
safe: it fails CI if you drop a package or double-assign one.
