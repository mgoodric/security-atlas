# 417 — Shard the `-p 1` integration job (Phase A serial / Phase B matrix)

**Cluster:** Infra
**Estimate:** 3d+
**Type:** JUDGMENT
**Status:** `ready` (deps merged — slice 334 `-p 1` rationale, slice 279 merged-coverage gate, slice 393 wall-clock watermark all on main)

## Narrative

**WHY.** The `Go · integration (Postgres RLS)` job runs ~95 packages **serially** under
`go test -tags=integration -p 1 ./internal/...`. The `-p 1` is load-bearing — slice 334's
framework review (`docs/audits/334-test-framework-review.md`) confirmed it serializes
test _binaries_ so they cannot race on shared **platform-layer** rows: `evidence_kind_schemas`
(schema-registry seed), `scf_anchors` (slice-006 seed), `evidence_records` (append-only
ledger uniqueness). The comment block at `ci.yml:312-353` documents this in detail. The
cost: the serial integration tier is the **dominant CI wall-clock** (~9-13 min), and slice
393's watermark (`scripts/measure-integration-wallclock.sh`, recorded on every clean `main`
push) trips a documented split-investigation at the 20-minute mark — but **no slice exists
to do the split.** This is that slice.

**NEW ANGLE vs slice 334.** Slice 334 deliberately KEPT `-p 1` and, in its "future
relaxation path (v3, NOT now)" note at `ci.yml:340-348`, only considered `-p N` on a single
runner for the tenant-scoped packages. This slice takes a _different_ tack that does NOT
re-open the `-p N` question: **shard across multiple GHA runners**, not parallelize within
one. Two legs:

- **Phase A (serial, `-p 1`, one runner)** over the shared-seed / platform-layer packages
  that genuinely collide: the `db`, `schemaregistry`, `evidence/ingest`, `catalog` cluster
  (the exact packages `ci.yml:341-344` names as the `-p 1`-load-bearing set). These keep
  `-p 1` — `-p 1` is untouched for them.
- **Phase B (matrix across 2-3 runners, each still `-p 1` _within_ its leg)** over the
  tenant-scoped packages (controls, risks, evidence/records-post-seed, policies, audit,
  vendor, the `internal/api/*` handler families). These do not collide cross-binary on the
  platform-layer rows (their isolation is the transaction-scoped `SET LOCAL app.current_tenant`
  RLS pattern, which `-p 1` was _never_ protecting — `ci.yml:330-339` says so explicitly), so
  they can run on **separate runners in parallel** while each runner still runs `-p 1`
  internally. Sharding ≠ `-p N`: each leg is serial; the parallelism is across machines, each
  with its own Postgres/MinIO/NATS bring-up and its own seed.

Coverage must still gate. Slice 279's merged-coverage gate consumes one integration
coverage profile; this slice must **merge the coverage profiles across all legs** (Phase A +
each Phase B shard) with `gocovmerge` into the single profile the slice-279 gate reads,
BEFORE the gate runs — otherwise the gate sees only a fragment and the ratchet breaks.

**WHAT.** Restructure the integration job into Phase A + a Phase B `strategy.matrix` of 2-3
shards; each leg brings up its own stack and runs its package subset under `-p 1`; a
fan-in step downloads every leg's coverage artifact, merges them, and runs the slice-279
gate on the union. The slice-393 watermark is updated to measure the _critical-path_ leg
(the slowest of Phase A vs the Phase B shards) rather than the old serial total.

**SCOPE DISCIPLINE.** Does NOT touch product code, schema, or tests' behavior — pure CI
restructure. Does NOT re-litigate `-p 1` (slice 334 settled it; each leg stays `-p 1`).
Does NOT change the merged-coverage _thresholds_ (slice 279's floors are a ratchet; this
slice preserves the union-coverage number, it does not lift floors). The package→leg
assignment is the JUDGMENT call and is recorded in the decisions log.

## Threat model

STRIDE pass. Sharding a test job is a correctness/coverage-integrity surface: a bad shard
split can drop a package's tests entirely (a control silently goes untested) or fragment
coverage so the ratchet under-counts. Those are real Integrity/Information-disclosure
threats for a tool whose v1 thesis is "diligence the diligence tool."

**S — Spoofing.** No endpoint, no identity. Each shard's stack bring-up uses CI-scoped
role passwords exactly as today. No change.

**T — Tampering (coverage/test integrity — the load-bearing cluster).**

- **T-1 (a package falls through the shard split and runs in NO leg).** If the union of
  Phase A's package list + every Phase B shard's package list is not EXACTLY the current
  `./internal/...` set, some package's integration tests silently stop running — a coverage
  _and_ correctness regression that looks green. Mitigation: AC-6 asserts the leg union ==
  the current enrolled set via an automated completeness check (cross-reference slice 345's
  `scripts/audit-integration-enrolment.sh`); P0-1 forbids any package being unassigned.
- **T-2 (cross-binary platform-row race re-introduced by mis-assignment).** If a shared-seed
  package (e.g. `schemaregistry`) is mistakenly placed in a parallel Phase B shard, two legs
  race on `evidence_kind_schemas` / `scf_anchors` and flake. Mitigation: P0-2 pins the
  `ci.yml:341-344` shared-seed cluster to Phase A (serial) only; AC-3 documents the
  assignment rule.
- **T-3 (coverage fragment under-counts the ratchet).** If the slice-279 gate runs on one
  leg's profile instead of the merged union, package floors evaluate against partial data and
  the ratchet either falsely passes or falsely fails. Mitigation: AC-7 + AC-8 — merge ALL
  legs' profiles before the gate; prove the union number == the pre-shard number on an
  unchanged commit.

**R — Repudiation.** No audit-trail surface. CI logs per-shard are retained as today.

**I — Information disclosure.** No tenant data crosses the CI boundary; each shard's
Postgres is ephemeral and CI-scoped. The merged coverage profile exposes nothing new. A
green integration check must continue to _mean_ the full suite ran (T-1 guards this).

**D — Denial of service (the dependency note).** **D-1 (a flaky test makes sharding
strictly worse).** Under serial `-p 1`, a flake fails one job; under a 3-way matrix, a flake
on any shard fails the whole integration check AND the maintainer must distinguish which
shard flaked. The scheduler timing-flake (`TestRun_FiresInlineSweepAndExitsOnCancel`,
`internal/metrics/scheduler/integration_test.go` — the same flake that blocked slice 346's
PR #788) lives in a Phase-B-eligible package; sharding multiplies its blast radius across
re-runs. Mitigation: this slice PAIRS WITH the scheduler-flake fix (file/track separately) —
AC-9 requires the decisions log to name every known-flaky integration test and its leg
assignment, and to flag whether the flake must be fixed BEFORE the shard lands. The "no
retry, investigate every flake" policy (CLAUDE.md Q-16) is unchanged — sharding does not add
a retry.

**E — Elevation of privilege.** No role boundary moves; `atlas_app` / `atlas_migrate`
role model per leg is identical to today's single-job model.

**Verdict: has-mitigations.** Three real Integrity threats (T-1 package-drop, T-2 race
re-introduction, T-3 coverage fragment) + the D-1 flake-amplification dependency. All are
addressed by ACs/P0s; the package→leg split being a JUDGMENT call is exactly why this is a
3d+ JUDGMENT slice, not AFK.

## Acceptance criteria

- [ ] **AC-1.** `Go · integration (Postgres RLS)` is restructured into a Phase A job
      (serial, `-p 1`, shared-seed packages) + a Phase B `strategy.matrix` of 2-3 shards,
      each running `-p 1` over its assigned tenant-scoped package subset.
- [ ] **AC-2.** Each Phase B shard brings up its OWN Postgres + MinIO + NATS + role-bootstrap + migrations + seed (reusing the bring-up; pairs with slice 418's composite action if
      that lands first — see Dependencies).
- [ ] **AC-3.** The package→leg assignment is encoded explicitly (a checked-in list or matrix
      `include:` block, not an implicit glob), and the assignment RULE (shared-seed →
      Phase A; tenant-scoped → Phase B) is documented in `docs/ci/` and the decisions log.
- [ ] **AC-4.** Each leg still runs `-p 1` _internally_ (no `-p N`); the parallelism is
      across runners only. The `-p 1` flag is present in every leg's `go test` invocation.
- [ ] **AC-5.** Each Phase B shard uploads its integration coverage profile as a distinct
      workflow artifact (distinct artifact names per shard).
- [ ] **AC-6.** A completeness check asserts that the UNION of Phase A's packages + all
      Phase B shards' packages EQUALS the current `./internal/...` integration-enrolled set
      (no package unassigned, none double-assigned). This check fails CI on drift
      (cross-reference slice 345 `scripts/audit-integration-enrolment.sh`).
- [ ] **AC-7.** A fan-in step downloads the unit-coverage artifact + Phase A's profile + every
      Phase B shard's profile and merges them with `gocovmerge` into the single profile the
      slice-279 merged-coverage gate consumes.
- [ ] **AC-8.** On an unchanged commit, the merged-union coverage number equals (within
      rounding) the pre-shard serial-job number — proven by a side-by-side in the decisions
      log. The slice-279 ratchet floors are NOT lifted by this slice.
- [ ] **AC-9.** The decisions log lists every known-flaky integration test, its assigned leg,
      and whether it must be fixed before the shard lands (scheduler flake
      `TestRun_FiresInlineSweepAndExitsOnCancel` explicitly addressed).
- [ ] **AC-10.** The slice-393 wall-clock watermark is updated to measure the CRITICAL-PATH
      leg (max of Phase A vs the Phase B shards) rather than the old serial total, so the
      20-minute trip-wire reflects real merge-blocking latency.
- [ ] **AC-11.** The `Go · integration (Postgres RLS)` REQUIRED check still reports a single
      green/red status that is green only when ALL legs pass (a fail on any shard fails the
      required check) — branch protection's contexts list is unchanged.
- [ ] **AC-12.** The migration round-trip step (`ci.yml` "Migration round-trip — down then
      up", slice 297) runs exactly ONCE (on Phase A or a dedicated leg), not redundantly per
      shard.
- [ ] **AC-13.** The slice-061 stub-twin for the integration check still resolves correctly
      on docs-only PRs (the fast-path is preserved across the new multi-job structure).
- [ ] **AC-14.** Total billable CI minutes for the integration tier are documented
      before/after in the decisions log (sharding trades wall-clock for parallel minutes; the
      net trade-off is recorded honestly, not assumed favorable).
- [ ] **AC-15.** `bash scripts/measure-integration-wallclock_test.sh` (the watermark
      self-test) still passes after the watermark step is rewired.
- [ ] **AC-16.** New `uses:` lines (if any) are SHA-pinned (slice 128 `actions-pin-check`
      invariant holds across all legs).
- [ ] **AC-17.** Decisions log at `docs/audit-log/417-integration-shard-decisions.md` records
      the shard count (2 vs 3) chosen, the package→leg map, the coverage-merge approach, and a
      revisit list (re-balance shards if one becomes the persistent critical path).

## Constitutional invariants honored

- Invariant #6 (RLS tenant isolation): each shard's tenant-scoped tests still set
  `app.current_tenant` per transaction; sharding does not weaken RLS — it relies on the same
  transaction-scoped isolation slice 334 documented as the _real_ (non-`-p 1`) isolation
  mechanism.
- Testing discipline (CLAUDE.md "four enforced surfaces"): the integration surface stays a
  single required check; coverage stays gated by the slice-279 ratchet on the merged union.
- Q-16 (no integration retry): unchanged — each leg keeps "investigate every flake," no
  `-retry` added.

## Canvas references

- CLAUDE.md "Testing discipline" + "Integration tier retry policy (Q-16)" + "Integration
  enrolment policy (Q-7)".
- `docs/audits/334-test-framework-review.md` ("-p 1 rationale review" section — the KEEP
  decision this slice does NOT re-open) + `ci.yml:312-353` (the inline `-p 1` rationale +
  the "future relaxation path" note this slice supersedes with the sharding angle).

## Dependencies

- **#334** — `merged` (`-p 1` KEEP rationale; this slice keeps `-p 1` per leg and builds the
  sharding angle 334 did not consider).
- **#279** — `merged` (merged-coverage gate; this slice must feed it the union profile).
- **#393** — `merged` (wall-clock watermark; this slice rewires it to the critical-path leg).
- **#345** — `merged` (integration-enrolment discovery primitive; reused for AC-6
  completeness check).
- **#418** — composite stack-up action. **Soft pair, NOT a hard blocker**: if 418 lands
  first, each shard calls the composite action instead of copy-pasting bring-up; if 417 lands
  first, the shards inline the bring-up and 418 later consolidates. Sequence either way.
- **Scheduler-flake fix** — soft pair (D-1). Tracked separately; AC-9 forces the
  fix-before-shard decision to be explicit, not implicit.

## Anti-criteria (P0 — block merge)

- **P0-1 (security — T-1).** No integration-enrolled package may be left UNASSIGNED to a leg.
  The union of all legs MUST equal the current enrolled set; AC-6's check enforces this and
  must be green.
- **P0-2 (security — T-2).** The shared-seed cluster (`db`, `schemaregistry`,
  `evidence/ingest`, `catalog` per `ci.yml:341-344`) MUST stay in Phase A (serial). It MUST
  NOT be placed in a parallel Phase B shard.
- **P0-3 (security — T-3).** The slice-279 gate MUST run on the MERGED union of all legs'
  profiles, never on a single leg's fragment. Coverage floors MUST NOT be lifted by this
  slice (ratchet is monotonic; lifting floors is a separate PR with the tests).
- **P0-4.** Does NOT introduce `-p N` on any leg — sharding is across runners, each leg
  stays `-p 1`. (Does not re-litigate slice 334.)
- **P0-5.** Does NOT change product code, schema, migrations, or any test's assertions —
  pure CI restructure.
- **P0-6.** Does NOT add `-retry` to the integration tier (Q-16 unchanged).
- **P0-7.** Does NOT auto-merge; maintainer reviews the package→leg split.

## Skill mix (3-5)

`ci-cd-pipeline-builder` · `performance-profiler` · `monorepo-navigator` ·
`grill-with-docs` · `Security` (coverage-integrity verification pass).

## Notes for the implementing agent

**Grill output (Phase 2):**

- _Terminology._ "Shard" here = a Phase B matrix leg on its own runner, each running `-p 1`.
  Do NOT conflate with `-p N` (in-process parallelism on one runner) — slice 334 settled
  `-p N` as out-of-scope; this slice never uses it. The distinction is the whole point of the
  "NEW angle" framing.
- _Scope._ The coverage-merge fan-in (AC-7/AC-8) is NOT optional polish — it is load-bearing,
  because slice 279's ratchet reads one profile. Drop it and the ratchet silently
  under-counts (T-3).
- _Already-built check._ `rg -l "shard|matrix.*integration" docs/issues/` returns only
  coverage-lift and integration-drain slices that _mention_ the integration job, none that
  shard it. This is the first.

**Threat-model context (Phase 3).** The three Integrity threats all reduce to "did every
package run, and was its coverage counted?" Build AC-6 (union == enrolled set) and AC-8
(union coverage == pre-shard) as the two automated guards; everything else follows.

**Sharp edges.**

- The slice-345 `audit-integration-enrolment.sh` + its `KNOWN_UNENROLLED` ratchet is the
  natural source-of-truth for "the current enrolled set" in AC-6 — reuse it rather than
  hand-maintaining a parallel list.
- Each Phase B shard needs its own seed; some tenant-scoped packages depend on slice-006
  `scf_anchors` seed rows existing. Confirm whether each shard must run a _subset_ of the
  seed or the full seed; if full-seed-per-shard is too slow, that is a re-balance question for
  the revisit list, not a blocker.
- Pair with slice 418: if you inline the bring-up across 2-3 shards, you have just created
  the exact 4×→Nx duplication 418 exists to kill. Prefer landing/using the composite action.

**Provenance.** Filed 2026-06-03 in the CI-backlog batch (415-420). Closes the
slice-393-watermark-trips-but-no-slice-exists gap with an angle slice 334 explicitly did not
take (across-runner sharding, not in-process `-p N`).
