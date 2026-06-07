# 481 — HIPAA Security Rule crosswalk loader (catalog-only; not the covered-entity workflow)

**Cluster:** Catalog
**Estimate:** M (1-2d)
**Type:** JUDGMENT (crosswalk-mapping accuracy is a subjective control call)
**Status:** `ready`

## Narrative

Roadmap §10.2 names HIPAA Security Rule as the fourth phase-2 framework
alongside ISO 27001:2022 (shipped), PCI DSS v4.0 (shipped), and NIST CSF 2.0
(slice 480). This slice ships **only the HIPAA Security Rule → SCF crosswalk
catalog** — the requirement nodes and their STRM-typed edges to SCF anchors —
on the generic 438 loader. It is **catalog data, not a workflow.**

**This framing is load-bearing and deliberate.** The canvas defers the
**HIPAA-specific covered-entity workflow** to phase 3 (canvas §10.3:
"HIPAA-specific covered-entity workflow primitives"; roadmap §10.1
"Deliberately deferred from MVP … HIPAA-specific covered-entity workflow").
That deferral is about _covered-entity / business-associate program mechanics_
— the §164.308 administrative-safeguard process flows, BAA tracking, the
required-vs-addressable implementation-specification decision workflow, breach
risk-assessment under §164.402. **None of that is in this slice.** What _is_ in
scope is the same thing slices 438/447/480 ship for their frameworks: the
HIPAA Security Rule standards/implementation-specifications as
requirement-grain catalog nodes, crosswalked to SCF anchors so an operator who
imports SCF can see "which of my SCF anchors satisfy 45 CFR §164.312(a)(1)"
the same way they already see it for SOC 2 / ISO / PCI / CSF.

The HIPAA Security Rule has a clean requirement grain for the catalog: three
safeguard categories (Administrative §164.308, Physical §164.310, Technical
§164.312) plus organizational requirements (§164.314) and
policies-and-documentation (§164.316). Each standard and its
implementation specifications (e.g. `45_cfr:164.312(a)(1)` Access Control,
`45_cfr:164.312(e)(1)` Transmission Security) is a requirement node mapping to
an SCF anchor via an STRM edge. Many of these anchors are already shared with
SOC 2 CC6.x and ISO A.8.x — so the catalog immediately demonstrates invariant
#1 across a fifth framework with strong existing overlap.

The HIPAA crosswalk also seeds the **FrameworkScope** story (invariant #5,
slice 018): HIPAA's scope (covered systems / ePHI environment) is distinct from
the SOC 2 system boundary and the PCI CDE — but proving that intersection is
the covered-entity-workflow's job, NOT this slice's. This slice notes the
FrameworkScope tie-in for the follow-on and ships only the catalog edges.

**Scope discipline.** First thin HIPAA vertical slice: one framework "version"
(HIPAA Security Rule, the current Federal Register text), DRAFT-tier crosswalk
on the generic 438 loader, the read-path proof, and a decisions log. It does
**not** ship the covered-entity workflow, BAA tracking, required-vs-addressable
decision flow, breach risk-assessment, or a HIPAA FrameworkScope CDE-style
example (that pairs with the deferred workflow). It does **not** attempt every
implementation specification (curated high-signal subset, target ~25-35 across
all three safeguard categories). **Follow-on slices:** full Security Rule
coverage; the phase-3 covered-entity workflow (separate, deferred); HIPAA
FrameworkScope ePHI-environment example.

## Threat model (STRIDE)

Inherits the slice-438 catalog-loader threat model: an operator/maintainer
catalog-write path via `atlas-cli`; a tenant-context-aware read path serving
shared, non-tenant-confidential reference data. HIPAA's regulatory sensitivity
(it governs ePHI) is a **reason to be precise about what the catalog payload
exposes** — but the catalog itself contains only public regulatory text + SCF
mapping facts, never any tenant's actual ePHI or control state.

**S — Spoofing.** No new authenticated endpoint. Importer reuses 438's
CLI/admin auth; the `GET /v1/requirements/{slug}/anchors` read keeps its
bearer/role gate. No new ingress.
**Mitigation:** reuse 438's import auth; no new endpoint.

**T — Tampering.** HIPAA crosswalk YAML is operator input → graph rows. A
malformed file could inject a colliding requirement code or an edge to a
non-existent SCF anchor.
**Mitigation:** the 438 loader validates slug/version non-empty, rejects
in-file duplicate codes, requires explicit `relationship_type` + `strength` per
row, and rejects unresolved anchor edges. Codes are namespaced by
`(framework_slug, version)` so a HIPAA `164.312(a)(1)` cannot collide with any
other framework's code.

**R — Repudiation.** Catalog import must be auditable.
**Mitigation:** 438's structured import-summary log + the committed spot-check
decisions log (durable JUDGMENT artifact).

**I — Information disclosure.** HIPAA's domain makes this the most important
STRIDE category for this slice: the `/anchors` read MUST return catalog
reference data only — NEVER any tenant's ePHI-related evidence or
control-implementation state. A leak here would be a confidentiality failure
with regulatory weight.
**Mitigation:** the `/anchors` read returns requirement → anchor edges + STRM
type only; tenant join state stays behind the `?include=state` opt-in (slice 104) carrying RLS. No new field widens the payload. The catalog rows contain
public CFR text + SCF mappings, not ePHI. **Explicitly assert in the
integration test that the HIPAA `/anchors` payload carries no tenant-scoped
field.**

**D — Denial of service.** Bounded offline import; bounded per-requirement
read.
**Mitigation:** reuse 438's bounds; no unbounded list path.

**E — Elevation of privilege.** Catalog-write is admin/maintainer.
**Mitigation:** reuse 438's catalog-write boundary; no new role.

## Acceptance criteria

**Backend — HIPAA crosswalk data (on the 438 generic loader)**

- [ ] **AC-1.** A DRAFT HIPAA Security Rule crosswalk YAML ships at
      `data/crosswalks/hipaa-security-rule.yaml` (curated high-signal subset,
      target ~25-35 standards + implementation specifications across
      Administrative §164.308 / Physical §164.310 / Technical §164.312) with
      `framework_slug: hipaa_security_rule` + a version identifier (e.g. the
      effective CFR revision date) and STRM-typed edges to SCF anchors — loaded
      by the **438 generic loader** (no HIPAA-specific loader code).
- [ ] **AC-2.** Importing the HIPAA crosswalk creates `framework_requirements` + `fw_to_scf_edges` rows without disturbing SOC 2 / ISO / PCI / CSF rows.
- [ ] **AC-3.** `GET /v1/requirements/{slug}/anchors` for a HIPAA requirement
      slug (e.g. `hipaa_security_rule:164.312(a)(1)`) returns its SCF anchor(s)
      with STRM edge type.

**Backend — graph proof + confidentiality assertion**

- [ ] **AC-4.** The graph proof extends: an SCF access-control anchor shared
      across SOC 2 CC6.x + ISO A.8.x + HIPAA §164.312(a)(1) resolves to all
      framework satisfactions through the single anchor (invariant #1).
- [ ] **AC-5.** The HIPAA `/anchors` payload is asserted (integration test) to
      contain **no** tenant-scoped field — only catalog reference data
      (threat-model I; regulatory-weight confidentiality check).

**Tests**

- [ ] **AC-6.** Integration test (`//go:build integration`): HIPAA import →
      requirement-anchors read round-trip against real Postgres.
- [ ] **AC-7.** SOC 2 / ISO / PCI / CSF existing tests pass unmodified.

**Docs / JUDGMENT artifact**

- [ ] **AC-8.** A spot-check decisions log
      (`docs/audit-log/481-hipaa-crosswalk-decisions.md`) records the
      mapping-accuracy JUDGMENT calls, the curated-subset rationale, confidence
      per cluster, an explicit "this is catalog-only; the covered-entity
      workflow is deferred (canvas §10.3)" note, and the "Revisit once in use"
      list. Include the `detection_tier_actual` / `detection_tier_target`
      header.
- [ ] **AC-9.** A changelog entry for the slice.

## Constitutional invariants honored

- **#1 — One control, N framework satisfactions.** HIPAA shares many SCF
  anchors with SOC 2 / ISO; the catalog demonstrates the invariant across a
  fifth framework.
- **#7 — Mappings go requirement → SCF anchor, never requirement → requirement.**
- **#8 — OSCAL is the wire format, not the daily model.**

## Canvas references

- `Plans/canvas/03-ucf.md` §3.1–3.2 — UCF graph; `PCI:8.3 intersects_with
HIPAA:164.312(d)` is a canvas STRM example, confirming HIPAA is in the
  catalog vocabulary.
- `Plans/canvas/10-roadmap.md` §10.2 — "HIPAA Security Rule" named in framework
  expansion; §10.1 + §10.3 — the covered-entity _workflow_ is the deferred part
  (NOT this slice).
- `Plans/canvas/05-scopes.md` §5.5 — FrameworkScope intersection (HIPAA covered
  systems ≠ SOC 2 system); noted for the follow-on, not built here.

## Dependencies

- **#438** (generic crosswalk loader) — `merged`.
- **#006** (SCF catalog importer) — `merged`. HIPAA edges target SCF anchors.

## Anti-criteria (P0 — block merge)

- **P0-481-1.** Does NOT ship the HIPAA covered-entity workflow, BAA tracking,
  required-vs-addressable decision flow, or breach risk-assessment — those are
  the canvas §10.3 deferred phase-3 work. This slice is catalog edges only.
- **P0-481-2.** Does NOT add a HIPAA-specific loader — loads through 438's
  generic importer.
- **P0-481-3.** Does NOT create requirement → requirement edges (invariant #7).
- **P0-481-4.** Does NOT duplicate controls per framework (invariant #1).
- **P0-481-5.** The `/anchors` read does NOT leak ANY tenant-scoped data — the
  HIPAA confidentiality bar makes this a hard P0 (threat-model I; AC-5).
- **P0-481-6.** Does NOT bundle pre-built SCF data (OQ #1).
- **P0-481-7.** Does NOT ship a HIPAA FrameworkScope ePHI-environment example —
  that pairs with the deferred covered-entity workflow.

## Skill mix (3-5)

`grill-with-docs` · `database-designer` (idempotent crosswalk import) · `tdd`
(integration-first; graph proof + confidentiality assertion) · `security-review`
(catalog-write + the regulatory-weight tenant-read confidentiality check) ·
`simplify`.

## Notes for the implementing agent

- The single most important framing call: **catalog, not workflow.** If the
  grill surfaces any pull toward covered-entity process flows, STOP and keep it
  out — that is the deferred phase-3 slice. This slice is data + edges + read
  proof, exactly like 480.
- **JUDGMENT calls you own:** STRM edge type per HIPAA standard/spec, the
  curated subset (cover all three safeguard categories), and how to represent
  required-vs-addressable in the catalog row WITHOUT building the decision
  workflow (a simple metadata tag at most; do not build UI). Record in the
  decisions log.
- AC-5 is the load-bearing confidentiality assertion — make the
  no-tenant-field check explicit in the test, given HIPAA's regulatory weight.
- Detection-tier classification: set both fields to `none` unless a bug
  surfaces during the build.
