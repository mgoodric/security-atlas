# 515 — NIST CSF 2.0 Profile / Tier assessment workflow

**Cluster:** Catalog
**Estimate:** L (3-5d)
**Type:** JUDGMENT (the CSF maturity construct + assessment UX are subjective design calls)
**Status:** `blocked` (depends on #480 — CSF crosswalk data — merged first)

## Narrative

Slice 480 explicitly deferred the CSF Tier/Profile assessment workflow
(P0-480-6): it is a CSF-specific maturity construct, NOT catalog scope. This
slice ships it.

NIST CSF 2.0 defines two maturity constructs the crosswalk data alone does not
model:

- **Tiers (1-4):** Partial → Risk Informed → Repeatable → Adaptive — a
  characterization of how rigorous and risk-informed an organization's
  cybersecurity governance/practices are.
- **Profiles (Current / Target):** a selection of Subcategory outcomes the
  organization is targeting, with a Current-vs-Target gap view.

A CSF self-assessment that enterprise customers and insurers ask for is
typically expressed as a Current Profile + Target Profile + a Tier rating, with
a gap/roadmap view. This slice adds that assessment workflow on top of the CSF
crosswalk that slice 480 (+ 514's full coverage) lands.

**Design question to resolve (JUDGMENT):** whether CSF Tier/Profile is a
CSF-specific table set or a generalization of a maturity-assessment primitive
that other frameworks (ISO Annex A applicability, PCI compensating-controls)
could reuse. The grill-with-docs step against canvas §3 + §7 (metrics / board
reporting) owns this — over-generalizing prematurely violates the Simplicity
Gate (Article VII), but a CSF-only table set may duplicate a future
maturity-assessment need. Resolve and record.

## Threat model

This slice adds a NEW assessment surface (Profiles are tenant-scoped
assessment state, unlike the shared crosswalk reference data), so it adds real
new surface beyond slice 480.

- **S — Spoofing.** New authenticated endpoints for Profile CRUD + Tier rating.
  Mitigated by the existing bearer/role auth + the OPA RBAC/ABAC layer.
- **T — Tampering.** Profile selections + Tier ratings are tenant-supplied
  assessment input. Mitigated by input validation + the schema enum for Tiers
  (1-4) and Profile kind (current/target).
- **R — Repudiation.** Assessment changes must be auditable (who set which Tier,
  when, against which CSF version). Mitigated by the standard audit-log pattern.
- **I — Information disclosure (LOAD-BEARING).** Profiles + Tier ratings are
  **tenant-confidential** assessment state — unlike the shared crosswalk.
  Tenant A's Current Profile / gap view must NEVER leak to Tenant B. Mitigated
  by PostgreSQL RLS on every Profile/Tier table (invariant #6) with an
  RLS-isolation integration test as a P0.
- **D — Denial of service.** Bounded by the CSF Subcategory count per Profile.
- **E — Elevation of privilege.** Profile editing is a tenant-scoped role;
  define the role-permission cut explicitly.

## Acceptance criteria

- [ ] **AC-1.** Schema (migration) for CSF Profiles (current/target) +
      Subcategory selections + Tier ratings, every tenant-scoped table carrying
      RLS + FORCE (invariant #6).
- [ ] **AC-2.** API to create/read/update a Current Profile, a Target Profile,
      and a Tier rating against a CSF framework version.
- [ ] **AC-3.** A Current-vs-Target gap view derived from the Profiles over the
      CSF crosswalk (Subcategory → SCF anchor → evidence/coverage, reusing the
      UCF graph traversal).
- [ ] **AC-4.** RLS isolation integration test: Tenant A's Profiles + Tier never
      appear for Tenant B (P0).
- [ ] **AC-5.** Frontend surface for the Profile editor + gap view (shadcn/ui).
- [ ] **AC-6.** Decisions log (`docs/audit-log/515-*.md`) — the
      CSF-specific-vs-generalized-primitive call, the Tier-rating UX, the
      role-permission cut.
- [ ] **AC-7.** Changelog entry.

## Anti-criteria (P0 — block merge)

- **P0-515-1.** Profiles/Tiers MUST be tenant-isolated via RLS — never
  application-code-only (invariant #6).
- **P0-515-2.** Does NOT duplicate the crosswalk reference data — the gap view
  reads the existing CSF crosswalk + UCF graph (invariant #1).
- **P0-515-3.** Does NOT auto-rate a Tier without operator input (no AI-assist
  audit-binding artifact without human approval — the constitutional
  boundary).
- **P0-515-4.** Does NOT over-generalize into a framework-agnostic
  maturity-assessment engine without the grill step justifying it against
  the Simplicity Gate.

## Dependencies

- **#480** (CSF crosswalk data) — parent; merge first.
- **#514** (full CSF Subcategory coverage) — strongly recommended first so the
  gap view covers the full framework, not the thin subset.
- **#006** (SCF catalog importer) — the gap view traverses CSF → SCF anchors.
