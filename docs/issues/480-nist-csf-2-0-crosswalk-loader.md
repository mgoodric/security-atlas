# 480 — NIST CSF 2.0 crosswalk loader (4th framework via the generic importer)

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `ready`

## Narrative

Roadmap §10.2 names four phase-2 frameworks: ISO 27001:2022 (shipped — slices
438 + 467), PCI DSS v4.0 (shipped — slice 447), **NIST CSF 2.0**, and HIPAA
Security Rule. This slice ships the **NIST Cybersecurity Framework 2.0**
crosswalk — the third of the four to land, and the one that most directly
exercises the UCF graph's value proposition: CSF 2.0 is a widely-adopted
outcome-oriented framework whose Subcategories (e.g. `PR.AA-01`, `GV.OC-01`,
`DE.CM-09`) map cleanly to SCF anchors, and a SaaS startup that already runs
SOC 2 + ISO is frequently asked for a CSF self-assessment by enterprise
customers and insurers. Adding CSF grows the catalog without new machinery: the
slice-438 generic loader (`internal/api/soc2import`) already imports any
`(framework_slug, framework_version)` crosswalk YAML, and slice 447 proved a
third framework drops in as pure data + a decisions log.

CSF 2.0's structure is a clean fit for the graph: the framework is organized
into six Functions (GOVERN, IDENTIFY, PROTECT, DETECT, RESPOND, RECOVER),
Categories, and Subcategories. The Subcategory is the requirement-grain node
(`nist_csf:2.0:PR.AA-01`); each maps to one SCF anchor via an STRM-typed edge.
The new GOVERN function (CSF 2.0's headline addition over CSF 1.1) maps to the
SCF governance families and is a high-signal demonstration that the graph
handles a function with no SOC 2 analog.

**Scope discipline.** This is the **first thin CSF vertical slice**: one
framework version (CSF 2.0), DRAFT-tier crosswalk data on the generic 438
loader, the read-path proof, and a spot-check decisions log. It does **not**
attempt full Subcategory coverage (curated high-signal subset, target ~30-40
Subcategories spanning all six Functions so every Function is represented),
does **not** ship CSF Tier/Profile assessment workflow (a CSF-specific maturity
construct — separate future slice, not catalog scope), and does **not** ship
crosswalk-review UI or coverage-strength visualization (slice 482 + the canvas
§10.2 crosswalk-validation tooling own those). **Follow-on slices:** full CSF
2.0 Subcategory coverage; CSF Profile/Tier assessment.

## Threat model (STRIDE)

Inherits the slice-438 catalog-loader threat model verbatim: the crosswalk
loader is an operator/maintainer catalog-write path run via `atlas-cli`; the
read path serves shared, non-tenant-confidential reference data through
RLS-aware handlers. CSF adds no new surface beyond a new data file.

**S — Spoofing.** No new authenticated endpoint. The importer reuses the 438
generic loader's CLI/admin auth boundary; the read endpoint
`GET /v1/requirements/{slug}/anchors` already exists and keeps its bearer/role
gate. No new ingress.
**Mitigation:** reuse 438's import auth; no new endpoint added.

**T — Tampering.** The CSF crosswalk YAML is operator-supplied input that
becomes `framework_requirements` + `fw_to_scf_edges` rows. A malformed file
could (a) inject a Subcategory code colliding with an existing framework's, or
(b) point an edge at a non-existent SCF anchor.
**Mitigation:** the 438 loader already validates `framework_slug`/`version`
non-empty, rejects duplicate requirement codes within the file, requires an
explicit `relationship_type` + `strength` on every row (no silent
`equal/1.0`), and rejects an edge whose `scf_anchor` does not resolve.
Requirement codes are namespaced by `(framework_slug, version)` so a CSF
`PR.AA-01` cannot collide with any SOC 2 / ISO / PCI code.

**R — Repudiation.** Catalog imports must be auditable: which crosswalk
version loaded, when, with what mapping count.
**Mitigation:** import emits the same structured import-summary log slices
007/438/447 emit; the spot-check decisions log is committed as a durable
JUDGMENT artifact.

**I — Information disclosure.** Catalog data is non-tenant-confidential
reference data, but the read endpoint is tenant-context-aware. Risk: a
framework-anchors read leaks tenant control-implementation state.
**Mitigation:** the `/anchors` read returns crosswalk reference data only
(requirement → anchor edges + STRM type), NOT tenant control state; tenant
join state stays behind the existing `?include=state` opt-in (slice 104) which
carries RLS. No new field widens the payload.

**D — Denial of service.** A crosswalk file with an unbounded requirement count
or an all-anchors read could blow up.
**Mitigation:** import is an offline CLI op (not request-hot-path); the read
endpoint resolves anchors for one requirement slug (bounded fan-out), the same
shape slices 438/447 shipped. No unbounded list path added.

**E — Elevation of privilege.** The importer crosses into catalog-write — an
admin/maintainer capability.
**Mitigation:** import reuses the 438 catalog-write boundary; this slice adds
no new role and does not widen who can write catalog data.

## Acceptance criteria

**Backend — CSF crosswalk data (on the 438 generic loader)**

- [ ] **AC-1.** A DRAFT NIST CSF 2.0 crosswalk YAML ships at
      `data/crosswalks/nist-csf-2.0.yaml` (curated high-signal subset, target
      ~30-40 Subcategories spanning all six Functions) with
      `framework_slug: nist_csf` + `framework_version: "2.0"` and STRM-typed
      edges to SCF anchors — loaded by the **438 generic loader** (no
      CSF-specific loader code).
- [ ] **AC-2.** Importing the CSF crosswalk creates `framework_requirements` +
      `fw_to_scf_edges` rows for CSF 2.0 without disturbing SOC 2 / ISO / PCI
      rows.
- [ ] **AC-3.** `GET /v1/requirements/{slug}/anchors` for a CSF requirement
      slug (e.g. `nist_csf:2.0:PR.AA-01`) returns its SCF anchor(s) with STRM
      edge type.

**Backend — graph proof extension**

- [ ] **AC-4.** The graph proof extends to four frameworks: an SCF anchor
      shared across SOC 2 + ISO + PCI + CSF resolves to all four framework
      satisfactions through the single anchor (invariant #1, four-framework).
- [ ] **AC-5.** At least one CSF GOVERN-function Subcategory (no SOC 2 analog)
      maps to an SCF governance-family anchor, asserted in an integration test
      — demonstrating the graph handles a Function with no overlap to the
      v1 framework.

**Tests**

- [ ] **AC-6.** Integration test (`//go:build integration`): CSF import →
      requirement-anchors read round-trip against real Postgres.
- [ ] **AC-7.** Pure-Go unit branch (`helpers_test.go` pattern) covers any new
      validation path the CSF data exercises (no new branch is expected — if
      none, assert the existing namespacing branch with a CSF code fixture).
- [ ] **AC-8.** SOC 2 / ISO / PCI existing integration + unit tests pass
      unmodified (proves the loader is unchanged).

**Docs / JUDGMENT artifact**

- [ ] **AC-9.** A spot-check decisions log
      (`docs/audit-log/480-nist-csf-crosswalk-decisions.md`) records the
      mapping-accuracy JUDGMENT calls (STRM type per Subcategory, which subset
      and why), confidence per cluster, the GOVERN-function coverage rationale,
      and the "Revisit once in use" list — mirroring 438/447. Include the
      `detection_tier_actual` / `detection_tier_target` header.
- [ ] **AC-10.** A changelog entry for the slice.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** Extends the graph proof to a
  fourth framework through one SCF anchor.
- **#7 — Mappings go requirement → SCF anchor, never requirement → requirement.**
  Every CSF Subcategory maps to an SCF anchor via an STRM edge; no
  CSF→other-framework direct edge exists.
- **#8 — OSCAL is the wire format, not the daily model.** Crosswalk data is
  ingested into the native graph; OSCAL export composes downstream, unchanged.

## Canvas references

- `Plans/canvas/03-ucf.md` §3.1 — UCF graph; CSF 2.0 is an example node-set
  (`NIST_CSF:2.0:PR.AA-01`) named directly in the canvas.
- `Plans/canvas/10-roadmap.md` §10.2 — "NIST CSF 2.0" named in framework
  expansion.
- `Plans/UCF_GRAPH_MODEL.md` — graph diagrams + worked example.

## Dependencies

- **#438** (generic crosswalk loader + ISO data) — `merged`. This slice loads
  CSF data through 438's framework-agnostic importer.
- **#006** (SCF catalog importer) — `merged`. CSF edges target SCF anchors that
  slice 006 imports.

## Anti-criteria (P0 — block merge)

- **P0-480-1.** Does NOT add a CSF-specific loader — loads through 438's generic
  importer.
- **P0-480-2.** Does NOT create requirement → requirement edges (invariant #7).
- **P0-480-3.** Does NOT duplicate controls per framework (invariant #1).
- **P0-480-4.** Does NOT import an edge to a non-existent SCF anchor (438's
  pre-insert validation — threat-model T).
- **P0-480-5.** The `/anchors` read does NOT leak tenant control-implementation
  state into the catalog-reference payload (threat-model I).
- **P0-480-6.** Does NOT ship a CSF Tier/Profile assessment workflow — separate
  future slice.
- **P0-480-7.** Does NOT bundle pre-built SCF data (OQ #1 — user imports SCF;
  this slice ships only the CSF→SCF edge data).
- **P0-480-8.** Does NOT copy verbatim copyrighted framework text — Subcategory
  identifiers + titles + original descriptions only (the 438/467 licensing
  posture). CSF 2.0 is a US-government work (public domain), but the loader's
  source_attribution discipline still applies (`community_draft` for the
  agent-authored mapping set).

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (idempotent crosswalk import) · `tdd`
(integration-first; four-framework graph proof) · `security-review`
(catalog-write + tenant read path) · `simplify`.

## Notes for the implementing agent

- The loader is already generalized (438). The work is (a) the CSF data file,
  (b) the four-framework graph-proof test, (c) the GOVERN-function no-SOC-2-
  analog assertion, (d) the decisions log. Resist any loader change.
- **JUDGMENT calls you own:** STRM edge type per Subcategory, the curated
  subset (ensure all six Functions are represented), and which GOVERN
  Subcategory anchors the no-analog proof. Record in the decisions log.
- AC-5 is the load-bearing differentiator vs. 438/447: CSF's GOVERN function
  has no SOC 2 counterpart, so it proves the graph isn't quietly assuming
  framework overlap.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
