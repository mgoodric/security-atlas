# 567 — Re-point palette-bound HIPAA low-confidence rows at finer full-catalog SCF anchors

**Cluster:** Catalog
**Estimate:** S (<1d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `blocked` behind **#754** (the catalog-seed path). The original Status
said "depends on a test path that seeds the operator's FULL SCF catalog, not the
53-anchor sample fixture" — scoping that path sharpened the diagnosis: the
dependency is a **catalog-content governance decision**, not a test-harness gap.
The shared integration suite seeds one catalog and the importer rolls back on an
unresolvable anchor, so a re-pointed row breaks all five frameworks' suites, not
just its own assertion. Split out as slice 754
(`docs/issues/754-full-scf-catalog-test-path.md`); the per-row control judgement
for all 21 residual rows is already done and recorded in
`docs/audit-log/567-hipaa-finer-anchor-remap-decisions.md` (D5), so this slice
becomes a mechanical application once 754 lands.

## Narrative

Slice 516 completed HIPAA Security Rule coverage (67 standards + implementation
specifications). Because the integration suite seeds only the 53-anchor sample
fixture (`migrations/fixtures/scf-sample.json`), several HIPAA rows had to map to
the _closest covering_ anchor in that palette rather than the finer, better-fit
anchor that exists only in the operator's full SCF catalog (slice 006). Slice
516's decisions log (D5, D7, and the 21-row residual table) documents each of
these honestly with the residual gap named.

This slice re-points the subset of those rows for which a genuinely finer anchor
exists in the FULL SCF catalog — once a test path exists that seeds the full
catalog (not the sample fixture), so the re-pointed edges resolve. The canonical
examples called out in slice 516 D5/D7:

- **§164.312(c)(1) Integrity** and **§164.312(c)(2) Mechanism to Authenticate
  ePHI** — currently DCH-01 (Data Classification & Handling) @ 0.65 / 0.60. The
  full SCF catalog carries a dedicated data-integrity anchor; re-point + lift.
- **§164.308(a)(5)(ii)(D) Password Management** and **§164.312(a)(2)(iii)
  Automatic Logoff** — currently IAC-01 @ 0.60. The full catalog has finer
  authenticator-management / session-management anchors.
- **§164.312(a)(2)(ii) Emergency Access Procedure** — currently IAC-21 @ 0.60;
  a dedicated break-glass anchor may exist in the full catalog.
- **§164.312(e)(2)(i) Transmission Integrity** — currently NET-04 @ 0.60; a
  transmission-integrity anchor may exist in the full catalog.

Pure data + decisions-log update; no loader change.

## Acceptance criteria

- [ ] A test path seeds the operator's full SCF catalog so finer-anchor edges
      resolve (or this slice establishes that path).
- [ ] The slice-516 palette-bound rows whose finer anchor exists in the full
      catalog are re-pointed and their strength lifted where justified.
- [ ] Decisions log records each re-map; the slice-516 residual table is updated.
- [ ] Anchor-palette resolution holds against whatever catalog the test seeds
      (zero dangling edges; rollback-on-nonexistent-anchor still proven).

## Dependencies

- **#516** (full HIPAA coverage) — merged first (this slice's parent).
- **#754** (a catalog-seed path carrying the finer anchors) — BLOCKING. The
  soc2import integration suite seeds one catalog via `scfseed.EnsureSCFCatalog`
  (`migrations/fixtures/scf-sample.json`, now 62 anchors, not the 53 recorded in
  slice 516). 754 must answer which catalog source backs the finer anchors before
  a single row here can move.

## Anti-criteria (P0)

- Does NOT add a HIPAA-specific loader.
- Does NOT create requirement → requirement edges (invariant #7).
- Does NOT reference an anchor the seeded catalog lacks (would roll back).

Parent: slice 516.
