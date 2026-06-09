# Slice 515 — NIST CSF 2.0 Tier / Profile assessment: JUDGMENT decisions log

**Slice:** 515 — NIST CSF 2.0 Tier / Profile assessment workflow
**Type:** JUDGMENT (the CSF maturity construct + assessment UX + the
generalize-or-not call are subjective design decisions)
**Parent:** 480 (CSF crosswalk data); strongly recommended-after 514 (full CSF
Subcategory coverage)
**Author:** Claude (Engineer)
**Date:** 2026-06-08

This log records the subjective build-time calls per the JUDGMENT-slice
convention. The product-runtime AI-assist boundary is untouched: a Tier is
NEVER auto-rated (P0-515-3) — `RateTier` takes an operator-supplied token and
there is no inference path. This slice adds a new tenant-confidential
assessment surface, so the dominant constitutional concern is **information
disclosure** (threat-model I): a tenant's Tier / profile / gap view must never
leak cross-tenant. That is enforced at the database layer (invariant #6 RLS),
not in application code.

---

## D1 — CSF-specific tables, NOT a generalized maturity-assessment primitive (the load-bearing JUDGMENT)

**Decision:** ship CSF-specific tables (`csf_tier_ratings`, `csf_profiles`,
`csf_profile_selections`, `csf_assessment_audit`). Do NOT build a
framework-agnostic maturity-assessment engine.

**Reasoning (the trade-off):**

- The slice + Article VII Simplicity Gate + anti-criterion P0-515-4 frame this
  exactly: do not over-generalize prematurely; a CSF-specific shape is the
  right v1 call UNLESS the generalization is nearly free.
- **It is not nearly free.** The two CSF constructs are tightly coupled to
  CSF's own model:
  - The **Tier** is a fixed 1-4 ordinal scale with CSF-defined semantics
    (Partial / Risk Informed / Repeatable / Adaptive). It has **no analog** in
    the two frameworks the slice names as candidate reusers: ISO 27001 Annex A
    applicability is binary (applicable / excluded with justification), and PCI
    DSS compensating-controls are per-requirement justification prose. There is
    no shared "maturity scale" to abstract over — inventing one now would be a
    speculative abstraction with **no second real consumer** to validate it.
  - The **Profile** is a per-Subcategory target-outcome selection. ISO Annex A
    applicability and PCI compensating-controls are also "per-requirement
    operator input", so a _selection_ abstraction is more plausibly shared than
    the Tier — but a generic engine would still need a configurable
    scale-definition table, a polymorphic selection model, and per-framework
    rendering. That is materially more surface than four CSF-specific tables,
    and the Simplicity Gate caps v1 at the minimum that solves the _actual_
    problem (a CSF self-assessment an insurer / enterprise customer asks for).
- **The generalization stays cheap to reach later.** The tables are already
  framework-pinned via `framework_version_id`. A future maturity-assessment
  primitive lifts the scale + selection shape into a `maturity_scales` +
  `maturity_assessments` pair (the scale rows define a framework's ordinal
  vocabulary; the assessment rows carry the per-requirement selection against a
  scale). That is an **additive migration**, not a rewrite — the CSF-specific
  tables can be back-filled into it or left as-is. We pay nothing now for a
  generalization we can do for the same price when a second real consumer
  appears.

**Grill-against-canvas note (per the slice's grill-with-docs requirement):**
canvas §3 (UCF graph) already separates the _shared_ crosswalk from
_tenant-overlay_ state; canvas §7 (metrics / board reporting) treats maturity
as a per-framework display concern, not a cross-framework engine. Both readings
support a framework-specific overlay over the shared graph rather than a new
cross-cutting engine. The gap view's coverage traversal is the canonical
example: it reuses the existing `internal/api/ucfcoverage`
requirement→anchor→coverage path (invariant #1, P0-515-2) rather than
re-storing the mapping per assessment.

---

## D2 — Single overall Tier in v1; per-function Tiers deferred

**Decision:** v1 ships ONE overall Tier rating per (tenant, CSF
framework_version). Rating each of the six CSF Functions (GV/ID/PR/DE/RS/RC)
independently is deferred.

**Reasoning:** the CSF self-assessment + insurer questionnaires that drive the
v1 binary criterion ask for a single overall Tier characterization. Per-function
Tiers are a richer construct (six rows + a roll-up UX) that no v1 user need
forces. The schema does not preclude it — a per-function variant adds a
nullable `function` discriminator + relaxes the unique constraint later. Filed
as forward work; not a spillover slice (no cohesive standalone surface yet).

---

## D3 — `target_outcome` as TEXT + CHECK, Tier as a Postgres enum

**Decision:** `csf_profile_selections.target_outcome` is `TEXT` with a CHECK
(`not_targeted | partial | largely | fully`); `csf_tier_ratings.tier` is a
Postgres `csf_tier` ENUM; `csf_profiles.kind` is a `csf_profile_kind` ENUM.

**Reasoning:** the Tier (1-4) and the profile kind (current/target) are
**closed, canonical** vocabularies defined by NIST — an enum is the right shape
(a bad value can never be persisted, and sqlc emits type-safe Go constants).
The per-Subcategory _target outcome_ is a **profile-level convention** the
project chose (NIST does not fix a four-point outcome scale), so it is more
likely to gain a value or a label later; a `TEXT + CHECK` widens with a single
`ALTER` rather than the heavier enum `ADD VALUE` dance. Both still reject
invalid input at the DB layer (threat-model T) and again in Go before the write
(`ValidTiers` / `ValidKinds` / `ValidOutcomes`, returning HTTP 400).

---

## D4 — Role-permission cut: `grc_engineer` / `admin` edits, all tenant roles read

**Decision:** WRITE routes (Tier rating, Profile create, selection set/clear)
require `cred.HasOwnerRole("grc_engineer")` (admin is a wildcard inside
`HasOwnerRole`); READ routes (Tier, Profile, gap) require only an authenticated
tenant credential. A viewer / auditor / control_owner can read the assessment
but cannot edit it.

**Reasoning:** the CSF assessment is program-management input the security
leader owns — the canvas §9.5 role model's `grc_engineer` is exactly that
operator role, and `admin` is the wildcard above it. An auditor and a viewer
have legitimate read interest (the gap view is the kind of artifact diligence
asks for) but no edit authority. This mirrors the slice-512 disposition gate
(read by owner, write by approver/admin) and the slice-018 framework-scope
approve gate. The gate lives in the HTTP handler (`editContext`); the DB layer
enforces tenant isolation only (it is role-agnostic). NEUTRAL test fixtures
only — the role tests mint `grc_engineer` / viewer / admin credentials via
`testjwt`, no real identities.

---

## D5 — Audit shape: one append-only `csf_assessment_audit` table, no FK to subject

**Decision:** a single append-only audit table records every Tier set/re-rate,
Profile create, and selection set/clear, carrying `framework_version_id`,
`subject_kind` (`tier|profile|selection`), `subject_id`, `action`, `actor`,
`detail`. No FK to the subject row. SELECT + INSERT RLS policies only under
FORCE (append-only by construction).

**Reasoning:** threat-model R asks for "who set which Tier / Profile selection,
when, against which CSF version" — every field is present. One table (rather
than three per-subject logs) keeps the audit trail queryable as a single
timeline and mirrors the `decisions_audit` precedent (slice 055). No FK so the
trail survives a future hard-delete of a profile/rating (the
framework_version CASCADE would otherwise take audit rows with it). The audit
write happens in the **same transaction** as the mutation, so a committed
mutation always has its audit row.

---

## D6 — RLS model: four-policy split, GUC-keyed, fail-closed

**Decision:** every assessment table carries `ENABLE` + `FORCE ROW LEVEL
SECURITY` with the four-policy split (read/write/update/delete) keyed on
`current_tenant_matches(tenant_id)`; the audit table is SELECT + INSERT only.
`atlas_app` (NOSUPERUSER NOBYPASSRLS) is the only application role.

**Reasoning:** this is the load-bearing P0 (P0-515-1, threat-model I). The
slice-512 `imported_components` precedent is the reference shape.
`current_tenant_matches` returns false when `app.current_tenant` is unset, so a
query with no tenant context returns zero rows — the fail-closed
deny-on-missing-context guarantee invariant #6 demands. Both properties are
asserted by integration tests:

- `TestRLS_CrossTenantIsolation` (the P0): tenant A builds a Tier + a current
  selection; tenant B reading the SAME framework_version sees `tier_rating:
null`, `profile: null`, zero selections, an empty gap, and no tier in its gap
  view; tenant B's own write never mutates tenant A's row; each tenant has its
  own audit trail.
- `TestRLS_DenyOnMissingContext`: a row seeded by the BYPASSRLS admin role is
  invisible to a `SELECT` over the app pool with no `app.current_tenant` set
  (returns 0).

---

## Detection-tier classification (slice 353)

- `detection_tier_actual`: `unit` — one latent bug surfaced _during the slice_
  and was caught before any commit by the unit/typecheck loop, not in
  production: the first cut of the three BFF routes read query params via
  `req.nextUrl.searchParams`, which the vitest `next/server` mock does not
  populate (the mock `NextRequest` is a bare `Request`). The vitest run failed
  red immediately; the fix was to read params via `new URL(req.url)` (the
  calendar-route idiom). A second, lint-tier catch: a `staticcheck SA4006`
  unused-assignment in the integration test, caught by `golangci-lint` before
  commit.
- `detection_tier_target`: `integration` + `unit`. The load-bearing assertion
  (RLS cross-tenant isolation + deny-on-missing-context, P0-515-1) is an
  integration-tier concern and is covered there
  (`TestRLS_CrossTenantIsolation`, `TestRLS_DenyOnMissingContext`) against a
  real Postgres under the `atlas_app` NOBYPASSRLS role — the only place RLS is
  actually exercised. The pure-Go Tier/kind/outcome validators + the
  Current-vs-Target `Gap` computation are unit-tier and are covered by
  `internal/csfassessment/helpers_test.go` (no DB, `t.Parallel()` table tests —
  the slice-353 Q-2 pre-DB convention). The BFF GET is covered by vitest
  (`web/app/api/csf/gap/route.test.ts`); the page render is covered by a
  route-mocked Playwright spec (`web/e2e/csf-assessment.spec.ts`, BFF-GET
  route-mocked per the b219 project lesson). The two detection-tier deltas above
  (`actual=unit`/`actual=lint` vs `target=unit`) are _in-tier_ catches, not a
  coverage gap.

---

## Spillover filed

None. The core (Tier rating + Current/Target Profile + per-Subcategory
selection + gap view + RLS + audit + tests at every tier + a read-focused web
surface) is complete and shipped in this slice. Candidate follow-on surfaces
that were deliberately kept OUT of this slice to avoid ballooning an L-sized
slice (per the scope-discipline instruction) are noted as forward work rather
than filed, because none is yet a cohesive standalone slice with a forcing
user need:

- **Per-function Tiers** (D2) — additive schema variant; no v1 user forces it.
- **A full per-Subcategory inline editor** (set every CSF Subcategory's target
  outcome from a single grid) — the write APIs exist (`PUT
/v1/csf/profiles/{kind}/selections`); a richer grid UX over the full CSF
  Subcategory set is a frontend slice if/when the gap-view read surface proves
  too thin in practice.
- **A CSF roadmap / remediation-tracking view** and **board-report integration
  of the gap view** — both are genuine cohesive surfaces (band 639-642
  candidates) but each depends on this slice's gap-view read shape landing
  first; file them once this is on `main` and the read shape is stable.
