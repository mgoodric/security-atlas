# 754 — A test path that seeds a catalog carrying the FINER SCF anchors (the full-catalog seed)

**Cluster:** Catalog
**Estimate:** M-L (needs a governance answer first, then 1-2d)
**Type:** JUDGMENT (the catalog-source call is a governance decision, not a lookup)
**Status:** `needs-decision` (blocked on the SCF catalog-source governance call — see
"The decision this slice needs" below)

> Split out of slice 567 (OPENENGINE-403). Slice 567 asked for the 21 residual
> HIPAA rows to be re-pointed at finer SCF anchors AND for the test path that
> makes those edges resolvable. Scoping the test path showed it is the larger,
> riskier half and that it turns on an unresolved governance question, so it is
> its own slice. Slice 567 is blocked behind this one.

## Narrative

**The shape of the problem.** Every bundled crosswalk (SOC 2, ISO 27001, PCI DSS,
NIST CSF, HIPAA) is imported by the integration suite against ONE seeded catalog:
`migrations/fixtures/scf-sample.json`, seeded through `scfseed.EnsureSCFCatalog`.
The importer rolls the entire transaction back when a mapping references an anchor
that catalog lacks (`crosswalk: scf_anchor "X" not found`, proven by
`TestHIPAAImport_RejectsEdgeToNonexistentAnchor`). So a crosswalk row can only
point at an anchor the seeded catalog carries — there is no per-crosswalk catalog
selection, and adding one is not a small change.

That makes "re-point HIPAA rows at finer anchors" **strictly downstream of a
catalog-content decision**. It is not a test-harness gap that can be papered over
with a second, opt-in test: as long as the shared suite seeds the sample fixture,
a re-pointed row breaks the whole `internal/api/soc2import` suite, not just its own
assertion.

**Why the obvious paths do not work as-is.**

| Path                                                                                | Why it is not a drop-in                                                                                                                                                                                                                                                                                                                                                                     |
| ----------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Bundle the real SCF release JSON as a second fixture                                | Blocked on an open question the project has deliberately not resolved: "SCF redistribution terms (legal review)" — CLAUDE.md lists it as _decide before bundling the SCF catalog in releases_. Slice 006 imports an operator-supplied release; nothing in-repo ships one.                                                                                                                   |
| Author a ~1,400-control full-catalog fixture in-repo                                | Slice 641's decisions log already recorded that SCF's authoritative per-control prose is not retrievable in-repo or from public pages. Authoring 1,400 control identifiers + titles from memory would fabricate an audit-facing catalog — exactly what the project's verification discipline forbids.                                                                                       |
| Env-var-gated path (`ATLAS_SCF_FULL_CATALOG=...`, `t.Skip` when unset)              | The assertions then never run in CI, so slice 567's AC-1 ("exists **and runs**") is unmet — and it does not help at all, because the _shared_ suite still seeds the sample fixture and the re-pointed edges still roll back there.                                                                                                                                                          |
| Expand `migrations/fixtures/scf-sample.json` with the ~13 finer anchors HIPAA needs | Mechanically the smallest change and it has precedent (slices 635 / 641 seeded the nine THR-domain anchors for exactly this class of gap). But slice 654's decisions log explicitly recorded catalog expansion as **"a separate governance call, deliberately not done"** — and it would grow the shared palette that five crosswalks resolve against. That is Matt's call, not an agent's. |

## The decision this slice needs (the reason it is `needs-decision`)

**Which catalog source backs the finer anchors?** Three answers, each with a
different downstream slice:

- **(A) Grow the bundled sample fixture** — add the specific finer anchors the
  crosswalks need (the 635/641 pattern, applied deliberately rather than
  incidentally), with a stated rule for what earns a place in the sample palette.
  Cheapest; reverses slice 654's "deliberately not done" posture, so it needs an
  explicit decision and probably an ADR.
- **(B) Bundle the real SCF catalog** — resolve the SCF-redistribution legal
  review first, then ship the release JSON as a second fixture and give
  `scfseed` a full-catalog seed mode. Highest fidelity, gated on legal review.
- **(C) Per-crosswalk catalog requirements** — let a crosswalk declare a minimum
  catalog and let the suite seed accordingly. Most flexible, most invasive; it
  reopens the slice-461 seed-order coupling the `scfseed` sentinel guard was
  written to close.

## Acceptance criteria

- [ ] AC-1: The catalog-source question above is answered and recorded (ADR if the
      answer is (A) or (C); legal-review outcome referenced if (B)).
- [ ] AC-2: A seeding path exists such that a crosswalk row pointing at a finer
      anchor resolves in the integration suite, and it **runs in CI** — not skipped.
- [ ] AC-3: `migrations/fixtures/scf-sample.json` is preserved, not weakened or
      replaced (slice 567 boundary). Whatever lands is additive.
- [ ] AC-4: The slice-461 invariant holds — `scfseed`'s completeness guard stays
      order-independent under `-p 1`, and no package's catalog assumptions break.
      The existing SOC 2 / ISO / PCI / CSF / HIPAA suites pass unmodified.
- [ ] AC-5: `TestHIPAAImport_RejectsEdgeToNonexistentAnchor` (rollback-on-
      nonexistent-anchor) still proves out against whatever catalog is seeded.

## Dependencies

- **#006** (SCF catalog importer) — merged; supplies `scfimport.Load` / `Import`.
- **#461** (integration seed-order coupling) — merged; `scfseed` is the seam any
  new seed mode must extend without breaking.
- Blocks **#567** (HIPAA finer-anchor re-point).

## Anti-criteria (P0)

- Does NOT weaken, shrink, or replace `migrations/fixtures/scf-sample.json`.
- Does NOT author SCF control identifiers or prose from memory — every anchor in
  any new fixture traces to a real SCF release the operator supplies.
- Does NOT bundle the SCF catalog ahead of the pending legal review.
- Does NOT change any crosswalk YAML (that is slice 567's job, downstream).

Parent: slice 567.
