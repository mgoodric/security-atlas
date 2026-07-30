# ADR 0021 — Application/service criticality tiering

**Status:** Accepted — design spike for OPENENGINE-624. This ADR defines the
shared Business Impact Analysis primitive and the consumer contracts. It does
not ship the primitive.

**Date:** 2026-07-30

**Implements through:** child build issues filed from OPENENGINE-624.

**Related sources:** [`internal/vendor/types.go`](../../internal/vendor/types.go),
[`docs/governance/business-continuity.md`](../governance/business-continuity.md),
[`docs/governance/incident-response.md`](../governance/incident-response.md),
[`docs/governance/asset-inventory.md`](../governance/asset-inventory.md),
[`Plans/canvas/05-scopes.md`](../../Plans/canvas/05-scopes.md), and
ADR-0011 / ADR-0014.

---

## Context

security-atlas has several local notions of importance, but no shared
application/service primitive:

- Vendors have a slice-024 `Criticality` band (`low | medium | high`) that
  drives review cadence. That band belongs to the third party and is not tied
  to the applications the vendor supports.
- The business-continuity plan records RTO/RPO targets by project asset class.
  It is a governance document, not a tenant-scoped product primitive other
  modules can read.
- Scope cells and `FrameworkScope` predicates model multidimensional audit
  scope. They can tag product/environment/data-classification dimensions, but
  `applicability_expr` currently has no system criticality input.
- The asset-inventory governance document inventories the security-atlas
  project itself. The software/vendor register tracks things the organization
  buys. Neither is a register of the applications/services the organization
  runs or owns.

The shared tier must be set once per tenant application/service and read by
BCP/DR, vendor risk, incident response, and control applicability without
copying separate criticality fields into each consumer.

## Decision

Create a first-class **application/service register** and give each row exactly
one Business Impact Analysis criticality tier. The register models systems the
tenant **runs, owns, or operates**: customer-facing products, internal services,
shared platforms, data-processing services, and business applications the tenant
is accountable for operating.

The register is distinct from:

- **Vendors and software:** vendors/software are things the tenant buys or
  depends on. They may support one or many applications, but they are not the
  application boundary. A vendor can be linked to supported applications through
  a tenant-scoped join.
- **The asset-inventory governance document:** that document inventories the
  security-atlas project/operator assets for audit evidence. It can cite this
  primitive later, but it is not the product source of truth for tenant systems.
- **Scope cells:** scope remains the multidimensional tuple space from
  ADR-0014. Application/service rows are reference data that scope predicates
  and controls may read; they do not replace scope cells.

### Tier model

Use four ordered levels:

| Tier  | Name              | Semantics                                                                                                                                                      | Default BCP/DR targets                            | Restore priority | Incident severity floor |
| ----- | ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------- | ---------------- | ----------------------- |
| **0** | Mission-critical  | Loss stops the core business, creates material customer/compliance exposure, or blocks incident recovery for other systems.                                    | RTO 4h, RPO 1h                                    | 1                | P1                      |
| **1** | Business-critical | Loss materially degrades customer delivery, revenue operations, or compliance operations, but the organization can operate briefly with workaround procedures. | RTO 24h, RPO 24h                                  | 2                | P2                      |
| **2** | Operational       | Loss disrupts a team or workflow but does not materially stop customer delivery or regulated processing.                                                       | RTO 3 business days, RPO 3 business days          | 3                | P3                      |
| **3** | Low               | Loss is inconvenient, experimental, replaceable, or archival with low operational impact.                                                                      | RTO 10 business days, RPO 10 business days or N/A | 4                | none                    |

Lower numeric tier means higher impact. Tier labels are user-facing, while the
numeric order is the stable contract for comparisons (`tier <= 1` means
Tier 0 or Tier 1).

The BCP/DR defaults are defaults, not hard-coded promises. A system row may
carry override RTO/RPO values only when the tenant records a rationale and an
owner. Consumers read both the tier and the resolved target values.

### Vendor criticality reconciliation

The existing vendor `Criticality` band **coexists** with application tiering,
but becomes a fallback/manual vendor-risk attribute rather than the primary
driver when application links exist.

The effective vendor review depth is derived as:

1. If the vendor supports one or more linked applications, compute the highest
   impact linked application tier and map it to review depth:

   | Highest linked app tier | Effective vendor band | Minimum review cadence |
   | ----------------------- | --------------------- | ---------------------- |
   | Tier 0                  | high                  | quarterly              |
   | Tier 1                  | high                  | quarterly              |
   | Tier 2                  | medium                | biannual               |
   | Tier 3                  | low                   | annual                 |

2. If no application links exist, use the vendor's existing `Criticality` band
   and configured cadence exactly as today.
3. If the manual vendor band is higher than the linked-app-derived band, the
   effective band is the higher one. This preserves legitimate vendor-level
   concerns that are not captured by one supported system, such as broad data
   access, concentration risk, or contractual/regulatory sensitivity.
4. If the manual vendor band is lower than the linked-app-derived band, the
   linked-app-derived band wins. A low-marked vendor supporting a Tier 0 system
   cannot stay on a low-depth review path.

This resolves the conflict without deleting the existing vendor field. Future
build work should expose the manual band as "vendor inherent criticality" and
the computed value as "effective review depth" so users can see why a review
cadence tightened.

## Read contracts

### BCP/DR

BCP/DR consumers read:

- `system.id`, `system.name`, `system.criticality_tier`
- resolved `rto_target`, `rpo_target`
- `restore_priority`
- `tier_rationale`, owner, and review timestamp

The default mapping above operationalizes the governance plan's RTO/RPO concept
inside the product. BCP views sort first by restore priority, then by explicit
override target, then by name. Lower-tier systems may not receive tighter RTO/RPO
than higher-tier systems without a recorded rationale, because that is a BIA
exception.

### Vendor risk

Vendor risk consumers read a tenant-scoped many-to-many link:

```
vendor_supported_systems(tenant_id, vendor_id, system_id, support_role, notes)
```

The review program computes `effective_vendor_criticality` and
`effective_review_cadence` from the highest impact linked system plus the
manual vendor band fallback/override described above. Vendor detail and
burndown views should show both the manual band and computed effective band.

### Incident response

Incidents reference zero or more affected systems. The system tier supplies a
severity floor:

- Any Tier 0 affected system floors the incident at P1.
- Any Tier 1 affected system floors the incident at P2.
- Any Tier 2 affected system floors the incident at P3.
- Tier 3 alone does not force severity.

Active exploitation, confirmed compromise, legal/regulatory deadlines, or
customer impact can always raise severity above the floor. The floor only
prevents under-triage of important systems.

### Control applicability

Control applicability may reference application/service tier through the same
predicate model used by `applicability_expr` and `FrameworkScope.predicate`.
The expression engine should support a stable dimension such as
`system_criticality_tier` with ordered comparisons (`lte`, `gte`) or normalized
sets (`in`). Examples:

```json
{ "op": "lte", "dim": "system_criticality_tier", "value": 1 }
```

```json
{ "op": "in", "dim": "system_criticality_tier", "values": ["0", "1"] }
```

Controls that do not care about tier remain unchanged. Higher-tier controls can
apply only to cells linked to Tier 0/Tier 1 systems, preserving ADR-0014's
intersection model instead of creating per-tier copies of controls.

## Data model and migration shape

Build work should add tenant-scoped tables, all protected by ADR-0011 RLS:

```
systems (
  id uuid primary key,
  tenant_id uuid not null,
  name text not null,
  slug text not null,
  description text not null default '',
  owner_user text not null default '',
  lifecycle text not null default 'active',
  criticality_tier smallint not null,
  tier_rationale text not null default '',
  rto_target interval null,
  rpo_target interval null,
  restore_priority smallint generated/resolved from tier unless overridden,
  reviewed_at timestamptz null,
  created_at timestamptz not null default now(),
  updated_at timestamptz not null default now(),
  unique (tenant_id, id),
  unique (tenant_id, slug),
  check (criticality_tier between 0 and 3)
)

system_scope_cells (
  tenant_id uuid not null,
  system_id uuid not null,
  scope_cell_id uuid not null,
  primary key (tenant_id, system_id, scope_cell_id),
  foreign key (tenant_id, system_id) references systems(tenant_id, id)
)

vendor_supported_systems (
  tenant_id uuid not null,
  vendor_id uuid not null,
  system_id uuid not null,
  support_role text not null default '',
  notes text not null default '',
  primary key (tenant_id, vendor_id, system_id),
  foreign key (tenant_id, vendor_id) references vendors(tenant_id, id),
  foreign key (tenant_id, system_id) references systems(tenant_id, id)
)
```

Every table must `ENABLE ROW LEVEL SECURITY`, `FORCE ROW LEVEL SECURITY`, and
use `current_tenant_matches(tenant_id)` for read/write/update/delete policies.
Composite `(tenant_id, id)` foreign keys are preferred anywhere a tenant-scoped
row points to another tenant-scoped row. No global system catalog is introduced;
system names and tiers are tenant private and must not leak across tenants.

Migrations should not backfill vendor links automatically. Existing vendors keep
their manual band until users link supported systems.

## Minimal API/UI surface

The buildable minimum is:

- CRUD API and UI for application/service rows with name, owner, lifecycle,
  tier, rationale, optional RTO/RPO override, and scope-cell links.
- Vendor detail/form support for linking supported systems and showing manual
  vendor band vs effective review depth.
- Incident create/edit support for affected systems and computed severity floor.
- Applicability-expression validation/evaluation support for
  `system_criticality_tier`.
- Read-only BCP/DR view/API that lists systems by restore priority and resolved
  RTO/RPO targets.

## Consequences

**Positive:**

- The BIA decision is made once per application/service and reused everywhere.
- Vendor reviews tighten automatically when a vendor supports high-impact
  systems, without deleting the vendor-specific criticality field.
- Incident triage cannot understate the severity of high-impact system events.
- Control applicability gains a tier input without breaking the existing
  multidimensional scope model.

**Negative / accepted trade-offs:**

- The product gains a new register. This is intentional because application
  ownership is a different entity from vendors/software and from scope cells.
- Vendor criticality becomes a two-value story: manual inherent band plus
  computed effective band. The UI must explain both or users will think the
  product is ignoring one value.
- Tier defaults can feel too coarse for mature programs. The override fields
  and rationale let those programs tune RTO/RPO without fragmenting the shared
  tier model.

## Alternatives considered

- **Add criticality as another scope dimension only.** Rejected. Scope cells
  are coordinates for evaluation. They do not carry ownership, RTO/RPO,
  lifecycle, supported vendors, or BIA rationale. A dimension alone cannot be
  the system register.
- **Put criticality on software/vendor rows.** Rejected. Software and vendors
  are things the tenant buys; the BIA tier belongs to systems the tenant runs
  or owns. One vendor can support a Tier 0 production service and a Tier 3
  internal experiment at the same time.
- **Replace vendor `Criticality` immediately.** Rejected. Existing vendor rows
  and review cadences depend on it, and some vendor risk is inherent to the
  vendor relationship independent of a single supported application.
- **Five or more BIA levels.** Rejected for v1. More levels invite false
  precision before the four consuming workflows are wired. Four levels preserve
  the distinction between mission-critical, business-critical, operational, and
  low impact.

## Open questions for Matt

These are non-blocking tuning decisions; changing them later does not alter the
entity boundary or read contracts in this ADR.

- Should the UI label Tier 0 as "Mission-critical" or "Critical"? The ADR uses
  "Mission-critical" to avoid colliding with the existing vendor `high` band.
- Should Tier 1 vendors require quarterly review by default, or should the
  first vendor-wiring slice allow tenants to configure Tier 1 as biannual?
- Should RTO/RPO overrides require approval evidence, or is owner + rationale
  enough for v1?

## Build decomposition

Child issues from OPENENGINE-624 should land in this order:

1. Application/service register primitive: schema, RLS policies, API/store, and
   minimal CRUD UI.
2. BCP/DR read surface: resolved RTO/RPO and restore-priority list.
3. Vendor supported-systems wiring: links plus effective review depth.
4. Incident affected-systems wiring: severity floor from highest-impact system.
5. Control applicability tier input: expression validation/evaluation and UI
   affordance for `system_criticality_tier`.
