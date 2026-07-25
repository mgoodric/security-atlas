# 682 — anchor demo controls to SCF + seed framework requirements/STRM edges so "Framework posture" renders — decisions log

- detection_tier_actual: manual_review
- detection_tier_target: integration

The gap was caught at **manual_review** (the 2026-06-10 demo-tenant UI audit,
item ATLAS-037: a seeded tenant's dashboard "Framework posture" tiles render
"No active framework versions yet"), then root-caused in slice 671's decisions
log D4. It SHOULD have been caught at the **integration** tier: the slice-205
demoseed suite asserted the seed writes ~50 controls but never asserted that
those controls participate in the SCF-anchor coverage spine (invariant #1), so
the broken `controls.scf_anchor_id` FK + the missing
`framework_requirements`/`fw_to_scf_edges` were invisible to CI. The missing
assertion is exactly the one this slice adds
(`TestApply_FrameworkPostureRendersCoverage`), which fails on the pre-682 seed
and passes on the fixed one. `actual=manual_review, target=integration` → an
integration-coverage gap in the demoseed suite, now closed.

---

## Context

Slice 671 made a seeded demo tenant show real per-control STATE/FRESHNESS by
driving the evaluator after the seed. But the dashboard "Framework posture"
tiles still rendered "No active framework versions yet" — and slice 671 proved
(D4) that this is INDEPENDENT of evaluation: even with `control_evaluations`
fully populated, `FrameworkPosture` (`internal/db/queries/dashboard.sql`)
returns zero rows for the demo tenant.

`FrameworkPosture` computes coverage through the SCF-anchor spine
(constitutional invariant #1):

```
framework_versions (status='current')
  -> framework_requirements
    -> fw_to_scf_edges (STRM, non-no_relationship)
      -> scf_anchors
        <- controls.scf_anchor_id  (the tenant's active controls)
```

The pre-682 demo seed broke this chain in two places: (1) `writeControls`
(`internal/demoseed/writers.go`) set `scf_id` (free-form TEXT) but NOT
`scf_anchor_id` (the UUID FK the posture query joins on), so the
`covering_control` CTE found zero controls; and (2) the demo framework version
had no `framework_requirements` and no `fw_to_scf_edges`, so the `version_reqs`
and `req_anchor` CTEs were empty.

This slice adds `writeFrameworkPostureSpine` (writers.go), called from
`Seeder.Apply` after `writeAuditPeriodsAndSamples`, which seeds the whole spine
and back-fills `controls.scf_anchor_id`.

## D1 — AC-4 (LOAD-BEARING): self-contained spine, adopting the global SCF catalog when present

**Decision.** The demo seed is **self-contained**: it builds the entire SCF
spine (anchors + requirements + STRM edges) on a **tenant-scoped demo
framework_version**, so the dashboard renders coverage even when the full
global SCF catalog (the slice-006 import — `framework_versions` /
`scf_anchors` with `tenant_id IS NULL`) is NOT loaded. When the global catalog
IS present, the seed **adopts** the real global `scf_anchors` rows (resolved by
`scf_id`) for the edges instead of duplicating them; the demo
`framework_requirements` + `fw_to_scf_edges` still hang off the tenant-scoped
demo `framework_version`, so the global catalog is never written or mutated.

**Options considered.**

- **(a) Require the global SCF catalog be loaded, anchor demo controls to it.**
  Rejected as the _primary_ path: the CI integration DB does NOT load the SCF
  catalog (the existing AC-4 teardown test at `integration_test.go:716` relies
  on `frameworks`/`framework_versions` being tenant-scoped demo rows, and
  `globalCatalogCount()` is 0 in CI), and a fresh self-host has no catalog
  before the operator's first import. Making the demo seed depend on a catalog
  precondition would make the demo a dead end in exactly the two environments
  that matter most (CI + first-run self-host).
- **(b) Synthesize a self-contained tenant-scoped anchor + requirement + edge
  set.** Chosen. The demo owns its spine; no external precondition. The
  synthesized anchors use **real SCF control codes** (IAC-06, CRY-05, BCD-11,
  TDA-09, TPM-03, MON-01, VPM-02, IRO-04 — drawn verbatim from the SCF
  catalog), so the demo anchors to real SCF controls, never a parallel
  taxonomy (invariant #7). They are `scf_anchors` rows pointing at the demo
  framework_version, distinguished from official catalog rows by the
  `fw_to_scf_edges.source_attribution = 'org_internal'` mark.
- **(c) Hybrid: adopt-when-present, synthesize-when-absent.** Adopted ON TOP of
  (b): the resolve-or-synthesize loop checks for a global anchor by `scf_id`
  first and reuses it when found. This keeps the demo correct in a real
  install (it anchors to the operator's actual SCF catalog) without ever
  requiring it.

**Trade-off (global-catalog dependency vs demo self-containment).** Self-
containment wins because the demo's job is to render a coherent program out of
the box, with zero preconditions, in CI and on first-run self-host. The cost is
that the synthesized anchors are demo-namespaced `scf_anchors` rows (8 of them)
that exist only because the demo framework_version exists — but they CASCADE-
delete with it on teardown, carry the honest `org_internal` attribution, and
collapse to the real catalog the moment one is imported.

**Confidence: high.** The branch is verified both ways: the default CI test
exercises the synthesize branch; a manual run with a partial global IAC-06
anchor confirmed the adopt branch resolves the real global anchor and creates
NO duplicate (only one IAC-06 anchor exists post-seed).

## D2 — Which SCF anchors, and how the controls map to them

**Decision.** A fixed thin slice of 8 SCF anchors spanning common program
families (auth, crypto, BCDR, secure-dev, third-party, monitoring, vuln-mgmt,
IR). Each anchor gets one `framework_requirement` (`DEMO-01`..`DEMO-08`) and
one `equal` STRM edge. The demo controls round-robin across the FIRST SIX
anchors; the last two anchors' requirements are deliberately left **uncovered**
so the coverage tile renders a realistic partial number (the seed lands ~75%,
not a flat 100% green wall).

**Rationale.** A handful of anchors keeps the demo within `PopulatedRowCap`
intent and produces a legible tile. Leaving 2 of 8 requirements uncovered is
the honest demo state — a real program has gaps, and a 100% tile teaches the
operator nothing. `equal` is the strongest STRM relationship (NIST IR 8477) and
the simplest defensible crosswalk for a demo; it is non-`no_relationship`, which
is what the posture query's `req_anchor` CTE requires.

**Confidence: medium.** The 8-anchor set + the 6-covered/2-gap split are demo
aesthetics, not domain truth. They are a reasonable first pass; the maintainer
may want to tune which families show as gaps once the demo is in front of
real evaluators.

## D3 — Spine placement + idempotency

**Decision.** `writeFrameworkPostureSpine` runs inside the existing single
BYPASSRLS `Apply` transaction, after `writeAuditPeriodsAndSamples` (the
framework-version-creation order is settled there) and before
`writeFrameworkScopes` (which reuses the demo framework_version the spine
guarantees is `status='current'`). It back-fills `controls.scf_anchor_id` via
UPDATE (the controls already exist from `writeControls`) rather than reordering
the writers.

**Idempotency (AC-5).** Every spine row hangs — directly or via FK
`ON DELETE CASCADE` — off the tenant-scoped demo `framework_versions` row, which
`Teardown` deletes with `DELETE FROM framework_versions WHERE tenant_id = $1`.
A re-seed rebuilds the spine from scratch with identical cardinality; the
integration test (`TestApply_PostureSpineIdempotent`) asserts the edge count is
unchanged across a teardown + re-apply. The `org_internal` synthesized anchors
CASCADE-delete with the demo framework_version; the adopted global anchors are
never touched (only the demo requirements/edges pointing at them are removed).

**Confidence: high.** Verified by the idempotency test + the existing AC-4
teardown test (global catalog count unchanged across teardown).

---

## Revisit once in use

- **D2 — anchor selection + coverage shape.** Re-evaluate which 8 SCF anchors
  the demo wires and which render as gaps once a security leader is looking at
  the seeded dashboard. The current 6-covered/2-gap split is an aesthetic
  guess; the realistic demo may want the gaps in higher-signal families
  (e.g. leave a monitoring or IR gap visible because those are the ones a board
  asks about).
- **D2 — STRM relationship type.** All demo edges are `equal`. Once a real SCF
  crosswalk is loaded, consider varying the demo edges (`subset_of`,
  `intersects_with`) so the demo also teaches the STRM-typed-edge concept, not
  just binary coverage.
- **D1 — adopt-branch family coverage.** When the global SCF catalog IS loaded,
  the adopt branch resolves anchors by `scf_id`. If a future SCF release renames
  or retires one of the 8 demo codes, that anchor silently falls back to the
  synthesize branch (a demo-namespaced anchor alongside the real catalog).
  Re-check the 8 codes against the bundled SCF release when the catalog importer
  pins a specific SCF version.
- **D2 — control-to-anchor mapping fidelity.** The demo controls round-robin
  across anchors by index, so a control titled "TLS 1.2+ enforced" may anchor to
  IAC-06 (auth) rather than CRY-05 (crypto). This is fine for a coverage demo
  but is not a faithful control→SCF mapping. If the demo ever needs to teach
  _accurate_ crosswalking, map each control to its semantically-correct anchor
  by family instead of round-robin.
