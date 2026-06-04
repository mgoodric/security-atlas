# Slice 461 — decisions log

Integration suite SCF-seed state coupling fix. JUDGMENT-type slice: the
build-time calls below were made by Claude and recorded here. This log does
NOT block merge.

- detection_tier_actual: integration
- detection_tier_target: integration

(A bug WAS surfaced during this slice — the `controls_scf_anchor_fk`
RESTRICT coupling under the full alphabetical wildcard, plus the spillover
`admindemo` dirty-DB fragility. Both were caught by running the integration
suite in a non-curated order during verification — i.e. the integration
tier, which is also the cheapest tier that could have caught them. No
production / fix-forward escape.)

## Decisions made

### D1 — Fix shape: make the seed order-INDEPENDENT (not just re-pin the CI order)

- **Options:** (1) harden the lazy-seed guard to be completeness-aware; (2)
  make scoped-DELETE packages restore full seed in teardown; (3) document +
  enforce the CI package order only.
- **Chosen:** (1), the spec's own recommendation. The seed guard now probes
  catalog COMPLETENESS (sentinel anchor `GOV-01` resolvable in the CURRENT
  SCF version) instead of raw row count, and reseeds fully when absent OR
  partial.
- **Rationale:** (3) is a workaround — it leaves local wildcard runs broken
  and the coupling latent. (2) spreads the burden across every future
  scoped-DELETE author (easy to forget; no enforcement). (1) makes each
  consumer self-correcting regardless of invocation order, which is the
  property the suite actually wants, and it composes: a package that does a
  partial DELETE no longer has to know about downstream consumers. The
  orchestrator directive explicitly forbade the re-pin-only workaround.
- **Confidence:** high. Proven: the previously-failing alphabetical wildcard
  (`./internal/api/...`) is green on a pristine DB, exit 0, 57 packages.

### D2 — Completeness probe keys on the sentinel anchor `GOV-01`, via the SOC 2 importer's exact query

- **Options:** (a) compare `count(*)` against an expected-full count constant;
  (b) probe a sentinel anchor through the production resolution path.
- **Chosen:** (b) — `dbx.GetSCFAnchorBySCFID("GOV-01")`, the identical query
  the SOC 2 importer uses (`slug='scf' AND status='current'`).
- **Rationale:** (a) is brittle — the expected count is a magic number that
  drifts whenever the sample fixture changes, and it would still pass on a
  catalog with the RIGHT count but the WRONG anchors (e.g. a demoted current
  version). (b) tracks production semantics exactly: if the importer can
  resolve the sentinel, the importer will succeed; if not, reseed. The
  failure mode of a stale sentinel (fixture drops GOV-01) is a redundant
  reseed, never a false "complete" — fail-safe.
- **Confidence:** high.

### D3 — Centralize in a shared `internal/api/scfseed` helper (not fix each guard in place)

- **Options:** (a) edit the `if anchorCount == 0` guard inline in each of the
  3+ consumer test files; (b) extract one shared helper package.
- **Chosen:** (b), mirroring the established `internal/api/testjwt`
  test-helper precedent (a non-build-tagged importable package).
- **Rationale:** the guard was duplicated across `anchors` (×2 files),
  `ucfcoverage`, and `soc2import` — exactly the drift surface that let the
  bug exist. A single helper means the completeness semantics live in one
  place and a future consumer gets them for free. Net LOC went DOWN in the
  consumers despite adding the helper.
- **Confidence:** high.

### D4 — Two helper entry points: `EnsureFullCatalog` vs `EnsureSCFCatalog`

- **Decision:** `EnsureFullCatalog` seeds SCF + imports the SOC 2 crosswalk;
  `EnsureSCFCatalog` seeds SCF only (no crosswalk).
- **Rationale:** the `soc2import` tests import the crosswalk themselves as the
  unit under test, so they must NOT have the helper import it for them — they
  need the SCF catalog present but the crosswalk to be their own action. The
  other consumers want the full pair. Splitting avoids either a flag argument
  (less readable) or forcing soc2import to hand-roll its own SCF seed (the
  duplication we are removing).
- **Confidence:** high.

### D5 — Reseed wipe `TRUNCATE controls CASCADE` first

- **Decision:** the full-reseed path (and the two test-file catalog wipes)
  TRUNCATE `controls` CASCADE before deleting `scf_anchors`.
- **Rationale:** surfaced during verification, NOT in the original spec. Under
  the full alphabetical wildcard the `internal/api/controls*` packages run
  before `scfimport`/`scfseed` and leave `controls` rows whose
  `scf_anchor_id` FK is `ON DELETE RESTRICT`, which blocked the anchor wipe
  (`controls_scf_anchor_fk` violation). This is the SAME class of order
  coupling (shared platform-layer state) so it is in scope for "make the
  suite order-independent." A reseed assigns new anchor IDs anyway, so any
  control FK pointing at the old anchors is stale by definition — wiping them
  is correct, not destructive. Mirrors `ucfcoverage`'s existing
  `wipeTenantControls` (`TRUNCATE controls CASCADE`) for the same reason.
- **Confidence:** high. Without it, AC-1's verbatim command fails.

### D6 — Make seed idempotent AND order-independent, not just idempotent

- **Decision:** the helper is both — the complete-path is a near no-op
  (idempotent), and the incomplete-path fully reseeds (order-independent).
- **Rationale:** idempotency alone (the old `content-equality-aware` import)
  did not fix the bug, because the guard SKIPPED the import entirely on a
  partial catalog. Order-independence requires the guard to detect partial
  state and act — which is the completeness probe. Idempotency keeps the
  hot path cheap; order-independence is the correctness property.
- **Confidence:** high.

### D7 — Guard shape: a runtime non-curated-order re-run in the integration job

- **Options:** (a) a grep/static lint forbidding the `if anchorCount == 0`
  anti-pattern; (b) a CI step that re-runs the catalog-sensitive packages in
  a deliberately NON-curated order against a fresh catalog and asserts green;
  (c) docs-only comment in ci.yml.
- **Chosen:** (b), `scripts/check-integration-seed-order-independence.sh`,
  wired into the existing integration job (which already has Postgres up).
- **Rationale:** (c) is not a guard. (a) is tempting but has a real
  false-positive problem — legitimate `count(*) FROM scf_anchors` test
  assertions and the regression test's own precondition count would trip a
  naive grep, and a grep precise enough to flag only the seed-skip shape is
  fragile. (b) is authoritative with zero false positives: it directly
  verifies the PROPERTY (order-independence) rather than proxying for it via
  a code pattern. It reuses existing infra so the cost is a few seconds and
  no new Postgres job. The offline env-misconfig paths get a `_test.sh`
  companion wired into the lint job, per repo convention.
- **Confidence:** medium-high. The guard's subset is hand-listed (the
  catalog-sensitive packages); a NEW package that introduces a fresh
  order-coupled seed but is not in the subset would not be caught by THIS
  guard (though the enrolment audit forces it into the main suite, and the
  next contributor running the full wildcard locally would still see it).
  See revisit item R3.

### D8 — Enrol `scfseed` adjacent to `scfimport`/`anchors` in the CI list

- **Decision:** added `./internal/api/scfseed/...` right after `anchors` in
  the curated `tests-integration` list (early, with the catalog packages).
- **Rationale:** the slice-345 enrolment audit requires every
  `//go:build integration` package to be enrolled. Placing it early (before
  any `controls`-seeding package) keeps it on the cheapest path even though
  the helper is order-independent by design. Position is not load-bearing
  for correctness — only for the curated run's own tidiness.
- **Confidence:** high.

## Revisit once in use

- **R1.** If the SCF sample fixture (`migrations/fixtures/scf-sample.json`)
  is ever reshaped to drop or rename `GOV-01`, update `scfseed.SentinelAnchor`
  in lockstep. The fail-safe (redundant reseed) means a stale sentinel never
  produces a false "complete", but it would make EVERY run reseed — a silent
  perf regression worth catching. Consider asserting the sentinel exists in
  the fixture in a fast unit test if the fixture starts churning.
- **R2.** The `TRUNCATE controls CASCADE` in the reseed path is a broad
  hammer. It is correct today because no package relies on another package's
  `controls` rows surviving, and a reseed only fires when the catalog was
  already broken. If a future package DOES depend on cross-package control
  state (it should not, per the per-test-seed norm), revisit whether the
  reseed should scope its controls wipe more narrowly.
- **R3.** The order-independence guard re-runs a HAND-LISTED subset of
  catalog-sensitive packages. When a new package starts seeding/consuming the
  SCF catalog, add it to `PACKAGES` in
  `scripts/check-integration-seed-order-independence.sh`. If this subset
  starts drifting from reality, consider deriving it (grep for packages that
  import `scfseed` or `scfimport`) rather than hand-maintaining it.
- **R4.** Spillover slice 462 (`admindemo` leaks a `demo` tenant on abort)
  is a sibling fragility in the same hardening direction — close it next so
  local dirty-DB re-runs are fully clean.

## Confidence summary

| Decision                               | Confidence  |
| -------------------------------------- | ----------- |
| D1 order-independent fix               | high        |
| D2 sentinel-via-importer-query probe   | high        |
| D3 shared scfseed helper               | high        |
| D4 two entry points                    | high        |
| D5 TRUNCATE controls CASCADE in reseed | high        |
| D6 idempotent + order-independent      | high        |
| D7 runtime non-curated-order guard     | medium-high |
| D8 scfseed CI enrolment placement      | high        |
