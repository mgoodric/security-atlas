# 438 — ISO 27001:2022 crosswalk loader (2nd framework — proves the UCF graph)

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `ready`

## Narrative

Today the platform crosswalks exactly **one** framework: SOC 2 v2017 (TSC),
landed by slice 007. That means the project's most load-bearing architectural
invariant — **invariant #1, "one control, N framework satisfactions"** — is
asserted but **unproven**: a graph with a single framework edge-set cannot
demonstrate that one SCF anchor satisfies requirements from two different
frameworks at once. The roadmap (canvas §10.2) names ISO 27001:2022 as the
typical next audit after SOC 2 ("prospect-driven"), so it is the right second
framework to de-risk the whole UCF thesis.

The good news: the slice-007 importer is already nearly framework-agnostic.
`internal/api/soc2import/loader.go` carries `FrameworkSlug` + `FrameworkVersion`
fields, a generic `Load(path)` + validation path, and emits
`framework_requirements` rows + `fw_to_scf_edges` rows keyed by framework
version — it is not hard-wired to SOC 2 anywhere structural. This slice does
two things: (1) **generalize** the loader package so its name + docs reflect a
framework-agnostic crosswalk importer (no behavioural change to SOC 2
ingestion), and (2) **ship ISO 27001:2022 → SCF STRM crosswalk data** as a
DRAFT YAML crosswalk plus a 20-anchor spot-check audit log, using the identical
JUDGMENT pattern slice 007 used for SOC 2. The proof point:
`GET /v1/requirements/{slug}/anchors` returns ISO anchors for an ISO
requirement code, and an SCF anchor shared between a SOC 2 criterion and an ISO
Annex A control resolves to **both** framework satisfactions through the one
anchor — the graph invariant, demonstrated.

**Scope discipline.** This is the **first thin vertical slice**: one framework
version (ISO 27001:2022), DRAFT-tier crosswalk data, the generic-loader
generalization, and the read-path proof. It does **not** ship a crosswalk
review/conflict UI (canvas §10.2 "crosswalk validation tooling" — separate
slice), does **not** ship coverage-strength visualization, and does **not**
attempt full 93-Annex-A-control coverage — a curated high-signal subset
(target ~30-40 controls covering the SOC 2 overlap zone plus ISO-unique
controls) is enough to prove the graph and seed the catalog. **Follow-on
slices:** full Annex A coverage completion; crosswalk-review UI; NIST CSF 2.0
loader (same generic path). Slice 447 (PCI DSS v4.0) depends on the generic
loader this slice extracts.

## Threat model (STRIDE)

The crosswalk loader is a catalog-write path run by an operator/maintainer via
`atlas-cli`; the read path serves tenant-scoped framework data. Catalog data
(SCF anchors, framework requirements, STRM edges) is **shared reference data**,
not tenant-confidential — but the read endpoint still serves it through
RLS-aware handlers and must not become a tenant-bypass.

**S — Spoofing.** No new authenticated endpoint: the importer is a CLI/admin
path reusing slice 007's auth boundary; the read endpoint
`GET /v1/requirements/{slug}/anchors` already exists and keeps its existing
bearer/role gate. No new unauthenticated surface.
**Mitigation:** reuse slice 007's import auth; no new ingress.

**T — Tampering.** The YAML crosswalk is operator-supplied input that becomes
`framework_requirements` + `fw_to_scf_edges` rows. A malformed or malicious
crosswalk could (a) inject a requirement code colliding with SOC 2's, or
(b) point an edge at a non-existent SCF anchor, corrupting the graph.
**Mitigation:** the loader validates framework_slug/version non-empty, rejects
duplicate requirement codes **within** the file (existing), and MUST reject an
edge whose `scf_anchor_id` does not resolve to a real anchor (FK + pre-insert
check). Requirement codes are namespaced by `(framework_slug, version)` so an
ISO `A.5.1` cannot collide with a SOC 2 `CC5.1`.

**R — Repudiation.** Catalog imports should be auditable: which crosswalk
version was loaded, when, mapping count.
**Mitigation:** import emits the same structured import-summary log slice 007
emits (framework, version, requirement count, edge count); the 20-anchor
spot-check audit log is committed as a durable JUDGMENT artifact.

**I — Information disclosure.** Catalog data is non-tenant-confidential
reference data, but the read endpoint is tenant-context-aware. Risk: a
framework-anchors read leaks **tenant-specific** control-satisfaction state
(which controls a tenant has mapped) where it should return only catalog-level
crosswalk facts.
**Mitigation:** the `/anchors` read returns crosswalk reference data only
(requirement → anchor edges + STRM type), NOT tenant control-implementation
state; tenant join state stays behind the existing `?include=state` opt-in
(slice 104) which carries RLS. No new field widens the payload.

**D — Denial of service.** A crosswalk file with an unbounded requirement count
or a read endpoint returning all anchors for a framework could blow up.
**Mitigation:** import is an offline CLI op (not request-hot-path); the read
endpoint resolves anchors for **one** requirement slug (bounded fan-out), the
same shape slice 007 shipped. No unbounded list path added.

**E — Elevation of privilege.** The importer crosses into catalog-write — an
admin/maintainer capability. Risk: a non-admin reaching the import path.
**Mitigation:** import reuses slice 007's admin/CLI role boundary; this slice
adds no new role and does not widen who can write catalog data.

## Acceptance criteria

**Backend — generalize the loader**

- [ ] **AC-1.** The slice-007 loader package is generalized to a
      framework-agnostic crosswalk importer: package/type docs no longer claim
      SOC 2-specificity; the public `Load` + validation surface is unchanged in
      behaviour for the existing SOC 2 crosswalk (proven by slice 007's tests
      still passing unmodified).
- [ ] **AC-2.** The importer rejects a crosswalk whose edge references a
      `scf_anchor` that does not resolve to a real anchor (pre-insert existence
      check or FK violation surfaced as a clear loader error, not a panic).
- [ ] **AC-3.** Requirement codes are namespaced by the framework
      `(slug, version)` pair; an ISO `A.x` code and a SOC 2 `CCx` code coexist
      without collision (integration-proven).

**Backend — ISO 27001:2022 data + read path**

- [ ] **AC-4.** A DRAFT ISO 27001:2022 crosswalk YAML ships (curated
      high-signal subset, target 30-40 Annex A controls) with
      `framework_slug: iso27001` + `framework_version: "2022"` and STRM-typed
      edges to SCF anchors.
- [ ] **AC-5.** Importing the ISO crosswalk creates `framework_requirements`
      rows + `fw_to_scf_edges` rows for ISO 27001:2022 without disturbing the
      SOC 2 rows.
- [ ] **AC-6.** `GET /v1/requirements/{slug}/anchors` for an ISO requirement
      slug (e.g. `iso27001:2022:A.5.1`) returns its SCF anchor(s) with STRM
      edge type.
- [ ] **AC-7.** **Graph proof:** for at least one SCF anchor shared between a
      SOC 2 criterion and an ISO Annex A control, the anchor resolves to BOTH
      framework satisfactions through the single anchor — asserted in an
      integration test (the invariant #1 demonstration).

**Tests**

- [ ] **AC-8.** Integration test (`//go:build integration`) covers ISO import →
      requirement-anchors read round-trip against a real Postgres.
- [ ] **AC-9.** Pure-Go unit test (`helpers_test.go` pattern) covers the
      loader's new validation branches (bad anchor ref, cross-framework code
      namespacing) without a DB.
- [ ] **AC-10.** SOC 2's existing slice-007 integration + unit tests pass
      unmodified (proves the generalization is behaviour-preserving).

**Docs / JUDGMENT artifact**

- [ ] **AC-11.** A 20-anchor spot-check audit log
      (`docs/audit-log/438-iso27001-crosswalk-decisions.md`) records the
      mapping-accuracy JUDGMENT calls (STRM type per edge, which subset of Annex
      A was included and why), confidence per cluster, and the "Revisit once in
      use" list — mirroring slice 007's pattern.
- [ ] **AC-12.** A changelog entry for the slice.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** This slice is the first
  empirical proof of the invariant: one SCF anchor, two frameworks.
- **#7 — SCF is the canonical control catalog; mappings go requirement → SCF
  anchor, never requirement → requirement.** The ISO crosswalk maps each Annex
  A control to an SCF anchor via an STRM edge; no ISO→SOC 2 direct edge exists.
- **#8 — OSCAL is the wire format, not the daily model.** Crosswalk data is
  ingested into the native graph; OSCAL export composes downstream, unchanged.

## Canvas references

- `Plans/canvas/03-ucf.md` — UCF graph, SCF anchors, STRM edges (the invariant
  this slice proves).
- `Plans/canvas/10-roadmap.md` §10.2 — "ISO 27001:2022 (the typical next audit
  after SOC 2, prospect-driven)".
- `Plans/UCF_GRAPH_MODEL.md` — graph diagrams + worked example.

## Dependencies

- **#007** (SOC 2 crosswalk loader) — `merged`. This slice generalizes its
  loader and proves the graph against its data.
- **#006** (SCF catalog importer) — `merged`. ISO edges target SCF anchors that
  slice 006 imports.

## Anti-criteria (P0 — block merge)

- **P0-438-1.** Does NOT create any requirement → requirement edge (ISO → SOC
  2 directly). All mappings go requirement → SCF anchor (invariant #7).
- **P0-438-2.** Does NOT duplicate controls per framework — no per-framework
  control rows; the SCF anchor is shared (invariant #1).
- **P0-438-3.** Does NOT regress SOC 2 ingestion — slice 007's tests pass
  unmodified.
- **P0-438-4.** Does NOT import an edge to a non-existent SCF anchor (validated
  pre-insert — threat-model T).
- **P0-438-5.** The `/anchors` read does NOT leak tenant control-implementation
  state into the catalog-reference payload (threat-model I).
- **P0-438-6.** Does NOT ship a crosswalk-review/conflict UI or coverage-
  strength visualization — those are separate canvas §10.2 slices.
- **P0-438-7.** Does NOT bundle pre-built SCF data (OQ #1 resolution — user
  imports SCF themselves; this slice ships only the ISO→SCF edge data).

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (idempotent crosswalk-import path) ·
`tdd` (integration-first; never mock the DB) · `security-review` (catalog-write

- tenant read path) · `simplify`.

## Notes for the implementing agent

- **Phase-2 grill output:** the loader is _already_ framework-agnostic in
  structure — resist a full rewrite. The work is (a) docs/naming so the surface
  reads as generic, (b) the anchor-existence validation, (c) the ISO data file,
  (d) the graph-proof test. Slice 447 (PCI) will reuse the same generic path, so
  keep the generalization clean and minimal.
- **JUDGMENT calls you own:** STRM edge-type selection per ISO control, and the
  curated subset of Annex A. Record both in the decisions log; do not block.
- AC-7 is the load-bearing one — pick a concrete shared anchor (e.g. an
  access-control SCF anchor that both SOC 2 CC6.x and ISO A.5.x map to) and
  assert both satisfactions resolve through it.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
