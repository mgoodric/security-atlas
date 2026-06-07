# 492 — OSCAL import: catalog / profile / component-definition ingestion

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** L (3-4d)
**Type:** JUDGMENT (OSCAL-tooling mapping decisions + how an imported catalog reconciles against the SCF spine are subjective calls)
**Status:** `ready`

## Narrative

Invariant #8 states OSCAL is the wire format and names **both** directions
explicitly: "**Ingest** catalogs/profiles/component-definitions; **export**
SSP/AP/AR/POA&M." The export half is built and signed (`internal/oscal` +
`oscal-bridge` serialize SSP/AP/AR/POA&M, slices 030 / 400 / 413 / 414 / 423).
**The ingest half does not exist.** The OSCAL bridge surface
(`proto/oscal/v1/oscal.proto`) exposes only `SerializeSSP`,
`SerializeAssessment`, `SerializePOAM`, and `RoundTripValidate` — there is **no**
`ImportCatalog`, `ImportProfile`, or `ImportComponentDefinition` RPC, and no Go
caller anywhere in `internal/` or `cmd/` parses an inbound OSCAL document. The
SCF catalog is loaded through a **custom** JSON importer (`internal/api/scfimport`),
not through OSCAL catalog ingestion.

This is a direct, load-bearing gap against a constitutional invariant. The
practical consequence: a customer who already maintains a NIST 800-53 / FedRAMP
control program **as OSCAL** (the format's home turf) cannot bring it into
security-atlas. A vendor who ships an OSCAL component-definition for their
product cannot have it ingested as control-implementation evidence. The OSS
thesis ("OSCAL is the wire format, not the daily data model") is only half-true
while ingestion is missing — today OSCAL is an export-only format here.

This slice ships the **first thin vertical slice of OSCAL ingestion: catalog
import**, end to end. A new `ImportCatalog` RPC on the bridge deserializes an
inbound OSCAL `catalog` JSON document via compliance-trestle (the same Python
bridge that already serializes), validates it against OSCAL v1.1.x, and returns
a normalized catalog projection; a Go caller persists the imported controls as
catalog rows mapped to SCF anchors (the mapping itself reuses the existing
SCF-anchor crosswalk path — an imported control maps **requirement → SCF
anchor**, never requirement → requirement, per invariant #7). A CLI
(`atlas-oscal import-catalog <file>`) exercises the path.

**Scope discipline.** Catalog import only in this slice. **Profile import**
(which resolves `import` + `merge` + `modify` directives against a catalog) and
**component-definition import** (vendor-supplied control implementations) are the
two follow-on slices — each is a meaningfully different OSCAL model with its own
resolution semantics, and bundling all three would be a 2-week slice that hides
three distinct sets of judgment calls. This slice proves the **bridge-ingestion
direction + the catalog → SCF-anchor reconciliation pattern**; the follow-ons
reuse it. Out of scope: profile resolution, component-definition import, OSCAL
catalog _re-export_ of an imported catalog (round-trip fidelity is the
RoundTripValidate concern, already built).

## Threat model (STRIDE)

OSCAL import is a **new untrusted-input ingress**: the platform parses an
externally-authored document and writes derived rows into a tenant's catalog.
This is the classic "import a file, get persistence" attack surface, and it
crosses the Go↔Python bridge boundary.

**S — Spoofing.** An attacker submits a catalog that impersonates a trusted
framework ("this is NIST 800-53 rev5") to get spoofed controls trusted.
**Mitigation:** import is an authenticated, tenant-scoped operation gated to the
`grc_engineer` (catalog-author) role — never anonymous. The imported catalog is
labeled with its **provenance** (source = `oscal-import`, importing user,
import timestamp, content hash of the source document) so a spoofed catalog is
attributable and distinguishable from the bundled SCF spine. Imported controls
do NOT silently overwrite SCF anchors; they are persisted as a distinct
imported-catalog set that maps **to** SCF anchors.

**T — Tampering.** A malformed or maliciously-crafted OSCAL document corrupts
the catalog, or import partially applies and leaves the catalog inconsistent.
**Mitigation:** the bridge validates the document against the OSCAL v1.1.x schema
before any persistence (reuse the existing trestle validation the serializer
relies on); persistence is **transactional** (all imported controls commit or
none); the source document's sha256 is recorded so the imported set is
tamper-evident after the fact. Invariant #7 is enforced: mappings go
requirement → SCF anchor, so an imported catalog cannot inject a
requirement → requirement edge that bypasses the SCF spine.

**R — Repudiation.** "Who imported this catalog and when?"
**Mitigation:** the import writes an audit-log entry (importing user, source
hash, control count, target framework label) — the same audit discipline as
every other catalog-mutating operation.

**I — Information disclosure (PRIMARY for the bridge boundary).** The Go↔Python
bridge carries the inbound document; a parser bug (XXE-style external-entity
resolution, billion-laughs entity expansion, or a path-traversal in any
file-href the OSCAL doc references) could read host files or exfiltrate.
**Mitigation:** OSCAL is JSON on this path (not XML) — no external-entity vector;
the bridge MUST NOT dereference any `href` / external resource the document
references during import (back-matter resources are treated as opaque
metadata, not fetched). A bounded document-size limit + parse timeout caps
expansion attacks. The bridge runs in its existing distroless container with no
outbound network need for import. **Cross-tenant:** the imported catalog is
written under the importing tenant's `app.current_tenant`; an integration test
proves Tenant A's imported catalog never lands in Tenant B's namespace.

**D — Denial of service.** A pathologically large OSCAL catalog (tens of
thousands of controls, deeply nested groups) exhausts memory in the bridge or
the import transaction.
**Mitigation:** a document-size cap + control-count cap (reject with a clear
error above the cap) + a per-import timeout; the import is streamed/bounded, not
loaded-then-unbounded-expanded.

**E — Elevation of privilege.** Import becomes a way to inject controls that
silently change authorization or control-eval behavior.
**Mitigation:** imported controls land as catalog rows only; they do NOT carry
or author OPA authz policy, and they do NOT auto-activate as evaluated controls
(activation is a separate, existing operator action). Import is gated to the
catalog-author role; no new self-elevation path.

## Acceptance criteria

**Bridge (proto + Python)**

- [ ] **AC-1.** A new `ImportCatalog(ImportCatalogRequest) returns
(ImportCatalogResponse)` RPC is added to `proto/oscal/v1/oscal.proto`; the
      request carries the inbound OSCAL catalog JSON bytes + a declared source
      label; the response carries a normalized catalog projection (controls with
      ids, titles, statements, group structure) + a validation result.
- [ ] **AC-2.** The Python bridge (`oscal-bridge`) implements `ImportCatalog`:
      deserializes the OSCAL `catalog` via compliance-trestle, validates against
      OSCAL v1.1.x, and returns the normalized projection (or a structured
      validation error). No `href` / external resource is dereferenced.
- [ ] **AC-3.** A bounded document-size limit + parse timeout are enforced in the
      bridge; over-cap documents are rejected with a clear error (threat-model
      D).

**Go side (persistence + provenance)**

- [ ] **AC-4.** A Go `Importer` calls the bridge, then persists imported controls
      as a provenance-labeled imported-catalog set (source=`oscal-import`,
      importing user, source sha256, import timestamp), mapping each imported
      control **to** SCF anchors (requirement → SCF anchor, never requirement →
      requirement — invariant #7).
- [ ] **AC-5.** Persistence is transactional: a validation failure or partial
      error commits **nothing**.
- [ ] **AC-6.** The import is tenant-scoped (writes under `app.current_tenant`)
      and gated to the catalog-author role.
- [ ] **AC-7.** The import writes an audit-log entry (user, source hash, control
      count, target label).

**CLI**

- [ ] **AC-8.** `atlas-oscal import-catalog <file>` reads an OSCAL catalog JSON,
      calls the bridge + importer, and reports controls imported / validation
      errors (text + `--json`).

**Tests**

- [ ] **AC-9.** Integration test (`//go:build integration`): a real OSCAL v1.1.x
      catalog JSON fixture is imported end-to-end against a real Postgres + the
      bridge; the expected controls appear as a provenance-labeled imported set
      mapped to SCF anchors.
- [ ] **AC-10.** Integration test: a malformed / schema-invalid catalog is
      rejected and persists **nothing** (transactional rollback proven).
- [ ] **AC-11.** Tenant-isolation integration test: Tenant A's imported catalog
      never appears under Tenant B (threat-model I).
- [ ] **AC-12.** Bridge unit test (Python): an over-cap document is rejected; an
      `href`-bearing document does not trigger any external dereference.

**Docs**

- [ ] **AC-13.** Operator docs document `import-catalog`, the provenance
      labeling, and that profile + component-definition import are follow-ons; a
      changelog entry for the slice.

## Constitutional invariants honored

- **#8 — OSCAL is the wire format (ingest direction).** This slice ships the
  first half of the ingest commitment the invariant names explicitly.
- **#7 — SCF is the canonical control catalog.** Imported controls map
  requirement → SCF anchor; no requirement → requirement edge is created.
- **#6 — Tenant isolation via RLS.** Import writes under `app.current_tenant`;
  proven by AC-11.
- **Anti-pattern: closed proprietary connectors.** OSCAL ingest is the open,
  standards-based import path — the opposite of a proprietary lock-in format.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.2 + the "OSCAL covers security
  primitives only" callout — the OSCAL model family.
- `Plans/canvas/03-ucf.md` §3.4 (OSCAL wire format) + §3.5 (SCF anchor mapping
  discipline).
- `CLAUDE.md` invariant #8 ("Ingest catalogs/profiles/component-definitions").

## Dependencies

- **#006** (SCF catalog importer) — `merged`. The SCF-anchor crosswalk an
  imported catalog maps onto.
- **#030** (OSCAL SSP/POA&M export + the Go↔Python bridge) — `merged`. The
  bridge + trestle plumbing this slice extends with an ingest RPC.
- **Profile import** + **component-definition import** — **follow-on slices**
  (file when this lands); they reuse this slice's bridge-ingest direction.

## Anti-criteria (P0 — block merge)

- **P0-492-1.** Does NOT create requirement → requirement mappings — imported
  controls map to SCF anchors only (invariant #7).
- **P0-492-2.** Does NOT dereference any external `href` / resource referenced by
  the imported document (threat-model I).
- **P0-492-3.** Does NOT persist partially on a validation failure — import is
  transactional (AC-5).
- **P0-492-4.** Does NOT silently overwrite the bundled SCF spine — imported
  catalogs are a distinct, provenance-labeled set.
- **P0-492-5.** Does NOT allow anonymous / cross-tenant import — gated +
  tenant-scoped (AC-6, AC-11).
- **P0-492-6.** Does NOT bundle profile + component-definition import into this
  slice — catalog import only (scope discipline).

## Skill mix (3-5)

`grill-with-docs` · `tdd` (integration-first; transactional-rollback + tenant
isolation tests are load-bearing) · `database-designer` (provenance columns +
transactional import) · `security-review` (untrusted-input ingress across the
bridge boundary) · `simplify`.

## Notes for the implementing agent

- **JUDGMENT calls you own:** how an imported catalog's control statements
  reconcile against existing SCF anchors when there is no obvious crosswalk
  (proposal: import unmapped, flag for operator mapping — reuse the
  questionnaire AI-assist "map once, canonical thereafter" pattern shape, minus
  the AI); the provenance schema shape; the document-size / control-count caps.
  Record in the decisions log with confidence levels.
- The Go↔Python bridge already exists for export — extend it, do not add a
  second bridge process.
- Detection-tier: a bridge `href`-dereference vuln caught in `security-review`
  would be `target=manual_review, actual=manual_review`; a transactional-rollback
  miss caught by AC-10 is `target=integration, actual=integration`.
