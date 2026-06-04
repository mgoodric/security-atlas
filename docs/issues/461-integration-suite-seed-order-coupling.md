# 461 — integration suite SCF-seed state coupling when run outside CI's curated package order

**Cluster:** Infra / Testing
**Estimate:** S (0.5d)
**Type:** JUDGMENT

**Status:** `ready`

> Surfaced during slice 454 (go-otel observability group bump). Not in scope
> for that slice — filed for follow-up.

## Narrative

The Go integration suite (`go test -tags=integration -p 1 ...`) is **order-coupled
on shared SCF-catalog seed state** when invoked over a broad package wildcard
(`./internal/api/...`) rather than CI's hand-curated, dependency-ordered package
list (`.github/workflows/ci.yml`, the `tests-integration` job: `db`, then
`scfimport`, `anchors`, `schemaregistry`, …).

The coupling chain:

1. Several `internal/api/*` integration tests seed the SCF catalog lazily with
   a guard of the shape `if anchorCount == 0 { Load(scf-sample.json); Import() }`
   (e.g. `internal/api/anchors/integration_test.go`,
   `internal/api/ucfcoverage/integration_test.go`). The guard is an intentional
   runtime bound — re-importing a loaded catalog is a no-op, so the test skips
   the reseed when rows already exist.
2. Other packages' setup issues a **partial** `DELETE FROM scf_anchors WHERE …`
   (scoped wipes, not a full `TRUNCATE`), leaving a **subset** of anchors behind
   (observed: 13 of the sample's full set, missing `GOV-01`).
3. Under `-p 1` with an alphabetical wildcard order, a downstream package
   (`soc2import`, `ucfcoverage`) then sees `anchorCount > 0`, **skips its
   reseed**, and its SOC2 crosswalk import fails with
   `soc2import: scf_anchor "GOV-01" not found — import the SCF catalog first (slice 006)`.

CI does not hit this because its package list is **ordered so the full SCF
import runs first** and the partial-DELETE packages run after. But anyone
running the suite locally over `./internal/api/...` (a natural thing to do)
gets spurious red that has nothing to do with their change — exactly what
happened during slice 454's verification (the bump was innocent; the failures
were 100% this seed-state artifact, proven by `TRUNCATE scf_anchors CASCADE`
→ clean reseed → green).

## Why this is worth fixing

- **False-red tax.** A contributor validating an unrelated change locally sees
  4 packages fail with a misleading "import the SCF catalog first" message and
  has to reverse-engineer that it is a harness ordering issue, not their bug.
- **Latent CI fragility.** The green CI run depends on the _manual ordering_ of
  the package list. A future reorder (alphabetizing the list, or an
  enrolment-script that sorts) would silently reintroduce the failure.

## Candidate fixes (pick one in the JUDGMENT call)

1. **Harden the lazy-seed guard** — change the `anchorCount == 0` guard to
   `anchorCount < expectedFullCount` (or assert a sentinel anchor like `GOV-01`
   is present), so a partial-DELETE leftover triggers a full reseed instead of
   being mistaken for "already seeded." Lowest-blast-radius.
2. **Make the scoped-DELETE packages restore full seed in teardown** — any test
   that partially deletes `scf_anchors` reseeds on cleanup so it leaves the
   shared platform-layer rows in the state the next package expects.
3. **Document + enforce the package order** — add a comment + a lightweight
   guard so the integration job's package list cannot be reordered without
   re-validating the SCF-seed dependency. Weakest (does not fix local
   wildcard runs).

Recommendation: (1) is the cleanest — it makes each consumer's seed guard
**self-correcting** regardless of invocation order, which is the property the
suite actually wants.

## Acceptance criteria

- [ ] **AC-1.** `go test -tags=integration -p 1 ./internal/api/...` (full
      wildcard, alphabetical order, single fresh DB) is green — no
      `scf_anchor "…" not found` failures from seed-order coupling.
- [ ] **AC-2.** The chosen fix is robust to a partial `DELETE FROM scf_anchors`
      by any package earlier in the run (regression-guarded).
- [ ] **AC-3.** CI's curated `tests-integration` package list still passes
      unchanged.

## Notes

- Reproduce: bring up a fresh Postgres + apply forward migrations, then
  `go test -tags=integration -p 1 ./internal/observability/... ./internal/api/...`
  — observe `anchors`, `scfimport`, `soc2import`, `ucfcoverage` fail; then
  `TRUNCATE scf_anchors CASCADE` and rerun the failing four in
  scfimport-first order — all green. This isolates the cause to seed-state
  ordering, not any product/library change.
- Cross-ref the slice 345 integration-enrolment discovery primitive and slice
  390 (KNOWN_UNENROLLED drain) — both touch the integration package list this
  coupling is sensitive to.
