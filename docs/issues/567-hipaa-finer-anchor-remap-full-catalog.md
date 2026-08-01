# 567 — Re-point palette-bound HIPAA low-confidence rows at finer full-catalog SCF anchors

**Cluster:** Catalog
**Estimate:** S (<1d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `done`. Two passes.

- **Pass 1 (scoping)** found the blocker was not a test-harness gap but a
  **catalog-content governance decision**: the shared integration suite seeds one
  catalog and the importer rolls back on an unresolvable anchor, so a re-pointed
  row breaks all five frameworks' suites, not just its own assertion. The seed
  path was split out as slice 754
  (`docs/issues/754-full-scf-catalog-test-path.md`) and the per-row control
  judgement for all 21 residual rows was recorded as pre-work (decisions log D5).
- **Pass 2 (applied)** — the operator answered 754's governance question on
  2026-07-25: **option (A), explicitly INTERIM.** Grow the bundled sample fixture
  with the finer anchors the 21 rows need (62 → 77 anchors) so the shared seed
  resolves them. 16 rows re-pointed, 3 checked and left in place, 2 held; both
  test tiers assert all 21. See decisions log D6–D9.

Slice **754 stays open** and is no longer blocking: it now owns the durable
catalog-source question — **(B)** bundling a real SCF release (deferred pending
the SCF-redistribution legal review) and **(C)** per-crosswalk catalog
requirements — neither of which this slice decided (decisions log D7).

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

- [x] A test path seeds a catalog carrying the finer anchors so those edges
      resolve. Option (A): the shared `scfseed.EnsureSCFCatalog` palette itself
      grew, so the assertions run in CI on every PR rather than behind an opt-in
      path that would never execute. Two tiers —
      `internal/api/soc2import/hipaa_finer_anchor_test.go` (pure Go) and
      `hipaa_finer_anchor_integration_test.go` (`//go:build integration`).
- [x] The slice-516 palette-bound rows whose finer anchor now exists are
      re-pointed and their strength lifted where justified — 16 of 21. Five did
      not move (3 checked and left, 2 held); no row was lifted past what its
      rationale earns.
- [x] Decisions log records each re-map (D6, all 21 rows); the slice-516 residual
      table is updated in place with each row's disposition.
- [x] Anchor-palette resolution holds — zero dangling edges across all five
      bundled crosswalks, and `TestHIPAAImport_RejectsEdgeToNonexistentAnchor`
      still proves rollback-on-nonexistent-anchor against the grown palette.

## Dependencies

- **#516** (full HIPAA coverage) — merged first (this slice's parent).
- **#754** (a catalog-seed path carrying the finer anchors) — was blocking;
  resolved for this slice's purposes by the operator's interim option-(A) call.
  754 remains open for the durable catalog-source question ((B) real-SCF
  bundling, gated on legal review; (C) per-crosswalk catalog requirements).

## Anti-criteria (P0)

- Does NOT add a HIPAA-specific loader.
- Does NOT create requirement → requirement edges (invariant #7).
- Does NOT reference an anchor the seeded catalog lacks (would roll back).

Parent: slice 516.
