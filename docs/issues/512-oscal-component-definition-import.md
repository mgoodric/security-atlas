# 512 — OSCAL component-definition import (vendor control-implementation evidence)

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** L (3-4d)
**Type:** JUDGMENT (how a vendor's control-implementation claims reconcile
against the SCF spine + how they surface as evidence are subjective calls)
**Status:** `ready`
**Parent:** #492 (OSCAL catalog import) — `merged`. This slice reuses #492's
bridge-ingest direction + the catalog → SCF-anchor reconciliation pattern.

## Narrative

Slice #492 shipped **catalog import** and scoped out component-definition import
as a follow-on, because a component-definition is a meaningfully different OSCAL
model with its own semantics.

An OSCAL **component-definition** is the vendor-side artifact: a software/service
vendor ships it to describe **how their product implements specific controls**
(`control-implementations` → `implemented-requirements`, each with a statement
and target control id). The customer ingests it as **control-implementation
evidence** — "Vendor X asserts their product satisfies AC-2 / SCF:IAC-01, here is
their statement." This is the inbound complement to the platform's own SSP export.

This slice adds an `ImportComponentDefinition` RPC on the existing `oscal-bridge`
(extending it, not forking it) that deserializes + validates a component-definition
via compliance-trestle and returns a normalized projection of components +
their implemented-requirements; a Go caller persists each component's
implemented-requirements as **provenance-labeled vendor-asserted evidence**
mapped **requirement → SCF anchor** (invariant #7), reusing #492's provenance shape.

**Critical boundary (AI-assist + invariant).** A vendor's implemented-requirement
is an **assertion**, not platform-verified evidence. It is persisted as a
vendor-attributed claim (with the vendor's identity + the source document hash),
and it does NOT auto-satisfy a control or fabricate control coverage. Surfacing
it as "satisfied" requires the existing human operator action — never automatic.
This keeps the import inside the `CLAUDE.md` AI-assist / fabricate-coverage
boundary even though no LLM is involved (the principle is "no fabricated
coverage," which a naive auto-accept of vendor claims would violate).

**Scope discipline.** Component-definition import only.

## Threat model (STRIDE)

A component-definition is untrusted vendor input asserting control coverage —
the spoofing/elevation edge is sharper than catalog import.

- **S — Spoofing.** A component-definition impersonates a trusted vendor or
  over-claims coverage. **Mitigation:** authenticated, tenant-scoped,
  `grc_engineer`-gated; persisted as a vendor-attributed CLAIM with provenance
  (source = `oscal-component-import`, importer, source SHA-256, declared vendor
  label); the claim is distinguishable from platform-verified evidence and from
  the SCF spine.
- **T — Tampering.** Malformed component-definition corrupts the import.
  **Mitigation:** bridge validates against OSCAL v1.1.x before persistence;
  transactional all-or-nothing; source SHA-256 recorded.
- **I — Information disclosure.** A `link.href` / back-matter resource points at a
  host file or remote URL. **Mitigation:** the bridge never dereferences any
  `href` — links/resources are opaque metadata; document-size cap + parse timeout.
- **E — Elevation of privilege (PRIMARY for this model).** An imported vendor
  claim auto-satisfies a control or fabricates coverage. **Mitigation:** imported
  implemented-requirements land as vendor-attributed CLAIMS only. They do NOT
  auto-activate, do NOT mark a control satisfied, and do NOT author authz/eval
  policy. Operator action (existing) is required to act on a claim. This is the
  load-bearing P0.
- **D — Denial of service.** A pathologically large component-definition.
  **Mitigation:** document-size cap + component/requirement-count cap + timeout.
- **R — Repudiation.** Append-only audit row (importer, source hash, component +
  requirement counts, vendor label).

## Acceptance criteria (outline)

- **AC-1.** `ImportComponentDefinition(...)` RPC added to
  `proto/oscal/v1/oscal.proto`; request carries the component-definition JSON +
  a source/vendor label; response carries components + implemented-requirements
  - a validation result.
- **AC-2.** The bridge deserializes + validates via compliance-trestle; no
  `href` dereferenced.
- **AC-3.** Document-size cap + component/requirement-count cap + parse timeout.
- **AC-4.** A Go caller persists each implemented-requirement as a
  provenance-labeled, **vendor-attributed** claim mapped requirement → SCF anchor
  (reuse #492 provenance shape).
- **AC-5.** Transactional: a validation failure commits nothing.
- **AC-6.** Tenant-scoped + `grc_engineer`-gated.
- **AC-7.** Imported claims do NOT auto-satisfy controls / fabricate coverage —
  they are CLAIMS requiring operator action (the dominant invariant).
- **AC-8.** Append-only audit row written.
- **AC-9.** CLI `atlas-oscal import-component-definition <file>` (text + `--json`).
- **AC-10..13.** Integration tests: a real component-definition imports
  end-to-end as vendor-attributed claims; a malformed one rolls back; tenant
  isolation holds; a claim does NOT mark any control satisfied; bridge unit test
  proves no dereference + cap enforcement. Operator docs + changelog.

## Anti-criteria (P0 — block merge)

- **P0-512-1.** Does NOT auto-satisfy a control or fabricate control coverage
  from a vendor claim (fabricate-coverage boundary).
- **P0-512-2.** Does NOT dereference any external `href`.
- **P0-512-3.** Does NOT create requirement → requirement mappings (invariant #7).
- **P0-512-4.** Does NOT persist partially on failure (transactional).
- **P0-512-5.** Does NOT overwrite the SCF spine or an imported catalog.
- **P0-512-6.** Does NOT allow anonymous / cross-tenant import.
- **P0-512-7.** Does NOT add a second bridge process — extend `oscal-bridge`.

## Dependencies

- **#492** (OSCAL catalog import) — provides the bridge-ingest direction + the
  provenance/persistence shape this slice reuses.

## Skill mix

`grill-with-docs` · `tdd` (integration-first; the no-auto-satisfy + no-dereference
tests are load-bearing) · `database-designer` (vendor-claim table + provenance) ·
`security-review` (vendor over-claim / fabricate-coverage is the dominant risk) ·
`simplify`.
