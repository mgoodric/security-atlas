# 511 — OSCAL profile import (resolve import / merge / modify directives)

**Cluster:** evidence-pipeline (OSCAL)
**Estimate:** L (3-4d)
**Type:** JUDGMENT (profile-resolution semantics + how a resolved profile
reconciles against the SCF spine are subjective calls)
**Status:** `ready`
**Parent:** #492 (OSCAL catalog import) — `merged`. This slice reuses #492's
bridge-ingest direction + the catalog → SCF-anchor reconciliation pattern.

## Narrative

Slice #492 shipped the first thin vertical slice of OSCAL ingestion — **catalog
import** — and deliberately scoped out profile import as a follow-on, because a
profile is a meaningfully different OSCAL model with its own resolution
semantics.

An OSCAL **profile** does not list controls directly; it _references_ one or
more catalogs (or other profiles) and applies **`import`**, **`merge`**, and
**`modify`** directives to produce a tailored control baseline. FedRAMP
Low/Moderate/High baselines are the canonical real-world example: each is a
profile over the NIST 800-53 catalog. To bring a customer's FedRAMP baseline
into security-atlas, the platform must **resolve** the profile against its
referenced catalog(s) into a concrete control set, then persist that set as a
provenance-labeled imported baseline.

This slice adds an `ImportProfile` RPC on the existing `oscal-bridge`
(extending, never forking, the bridge #492 already extended) that resolves a
profile via compliance-trestle's profile-resolver and returns the resolved
control projection; a Go caller persists the resolved baseline as a distinct,
provenance-labeled set mapped **requirement → SCF anchor** (invariant #7), reusing
#492's `internal/oscal/catalogimport` persistence + provenance shape.

**Scope discipline.** Profile import only. Component-definition import is the
sibling follow-on (#512). The catalog this profile resolves against must already
be importable (#492) or be referenced as a bundled catalog; resolving a profile
whose referenced catalog is absent is a clear, structured error — NOT a fetch of
an external `href` (see threat model).

## Threat model (STRIDE)

OSCAL profile import is a **new untrusted-input ingress** with a sharper edge
than catalog import: a profile **references** other documents, so the resolution
step is where an attacker tries to make the platform fetch or trust something it
should not.

- **S — Spoofing.** A profile claims to be a trusted FedRAMP baseline. **Mitigation:**
  authenticated, tenant-scoped, `grc_engineer`-gated import; resolved baseline
  carries provenance (source = `oscal-profile-import`, importer, source SHA-256,
  declared label) — the #492 provenance shape.
- **T — Tampering.** A malformed profile or a `modify` directive that corrupts
  the resolved baseline. **Mitigation:** the bridge validates the profile AND the
  resolved output against OSCAL v1.1.x before any persistence; persistence is
  transactional (all-or-nothing); source SHA-256 is recorded.
- **I — Information disclosure (PRIMARY).** A profile's `import.href` /
  `back-matter` resource points at a host file or remote URL. **Mitigation:**
  the resolver MUST resolve `import` references ONLY against catalogs the
  platform already holds (imported via #492 or bundled) — it MUST NOT dereference
  an external `href` / fetch a remote document. An `import` that names an unknown
  catalog is a structured error, not a fetch. Bounded document-size + resolution
  timeout cap expansion. This is the load-bearing difference from #492 and the
  dominant review focus.
- **D — Denial of service.** A profile with deeply chained imports or a
  pathological `merge` blows up resolution. **Mitigation:** document-size cap +
  resolved-control-count cap + resolution timeout + a max import-chain depth.
- **E — Elevation of privilege.** Resolved controls auto-activate or carry authz
  policy. **Mitigation:** resolved controls land as catalog/baseline rows only;
  no OPA policy authored; activation stays a separate operator action.
- **R — Repudiation.** Import writes an append-only audit row (importer, source
  hash, resolved-control count, label) — the #492 audit discipline.

## Acceptance criteria (outline)

- **AC-1.** `ImportProfile(ImportProfileRequest) returns (ImportProfileResponse)`
  RPC added to `proto/oscal/v1/oscal.proto`; request carries the profile JSON +
  the referenced catalog(s) (or their already-imported ids) + a source label;
  response carries the resolved control projection + a validation result.
- **AC-2.** The bridge resolves `import` / `merge` / `modify` via
  compliance-trestle's profile-resolver, validates the resolved output, and
  resolves `import` references ONLY against supplied/known catalogs — no external
  `href` dereference.
- **AC-3.** Document-size cap + resolved-control-count cap + resolution timeout +
  max import-chain depth enforced.
- **AC-4.** A Go caller persists the resolved baseline as a provenance-labeled
  imported set mapped requirement → SCF anchor (reuse #492 persistence).
- **AC-5.** Transactional: a resolution/validation failure commits nothing.
- **AC-6.** Tenant-scoped + `grc_engineer`-gated.
- **AC-7.** Append-only audit row written.
- **AC-8.** CLI `atlas-oscal import-profile <profile-file> [--catalog <file>...]`
  (text + `--json`).
- **AC-9..12.** Integration tests: a real FedRAMP-style profile resolves
  end-to-end; an unresolvable-import profile errors WITHOUT fetching; a malformed
  profile rolls back; tenant isolation holds; bridge unit test proves no external
  dereference + cap enforcement.
- **AC-13.** Operator docs + changelog.

## Anti-criteria (P0 — block merge)

- **P0-511-1.** Does NOT dereference any external `href` during resolution.
- **P0-511-2.** Does NOT create requirement → requirement mappings (invariant #7).
- **P0-511-3.** Does NOT persist partially on failure (transactional).
- **P0-511-4.** Does NOT overwrite the bundled SCF spine or an imported catalog.
- **P0-511-5.** Does NOT allow anonymous / cross-tenant import.
- **P0-511-6.** Does NOT add a second bridge process — extend `oscal-bridge`.

## Dependencies

- **#492** (OSCAL catalog import) — provides the bridge-ingest direction, the
  provenance + persistence shape, and the catalog set a profile resolves against.

## Skill mix

`grill-with-docs` · `tdd` (integration-first; resolution + no-dereference tests
are load-bearing) · `database-designer` (reuse #492 tables or a sibling baseline
table) · `security-review` (resolution is the dominant untrusted-input risk) ·
`simplify`.
