**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 5. Scopes and Multitenancy

## 5.1 Scope dimensions

(Repeated from [§2.4](./02-primitives.md) with the runtime semantics.) Each control's `applicability_expr` is a boolean expression over scope dimensions. Example:

```
environment IN ('prod', 'staging')
AND data_classification IN ('restricted', 'confidential')
AND geography IN ('US', 'EU')
AND cloud_account.provider = 'aws'
```

The evaluation engine, given a control and the org's universe of scope cells, computes the **applicability set** — the cells where this control must be evaluated. A control's overall pass requires pass in every applicable cell.

## 5.2 Per-cell evaluation

Each `(control × scope_cell × time)` triplet has its own state. The dashboard rolls up:
- by control across cells (where is it failing?)
- by cell across controls (what's broken in `prod` × `restricted` × `EU`?)
- by framework requirement across the SCF graph (what does my SOC 2 look like?)

## 5.3 Scope inheritance and override

Some scope dimensions are hierarchical (BU, geography). Controls can be declared at a parent level and inherit; child cells can override applicability or evidence requirements. Overrides are tracked as first-class artifacts (auditors care who changed scope).

## 5.4 Tenant isolation (Postgres RLS, named explicitly)

Multi-tenancy is enforced at the database layer using **PostgreSQL Row-Level Security**. Every tenant-scoped table has a `tenant_id` column and an RLS policy that restricts access based on the connection's `app.current_tenant` setting. Application code that forgets a `WHERE tenant_id = ...` cannot leak — RLS denies.

This is the only multi-tenancy strategy that does not depend on application-code correctness. For self-host deployments, this means a single Postgres instance can safely serve multiple tenants. For SaaS deployments, each tenant gets its own RLS context.

Storage tier (object store for large artifacts) uses per-tenant prefixes with tenant-scoped credentials — separate enforcement at separate layers.

## 5.5 Framework scope — the per-framework subset of cells and controls

The single most-misunderstood real-world fact about multi-framework programs is that **scope is per-framework, not global**. PCI's "cardholder data environment" (CDE) is not the same as HIPAA's "covered systems" is not the same as SOC 2's auditor-attested system is not the same as ISO 27001's ISMS scope. A control may be operationally applied across all 50 of an org's scope cells, but its evidence only *counts* for PCI in the 5 cells inside the CDE.

This intersection — between a control's operational applicability and a framework's audit scope — is how a unified control library produces a *subset* of relevant controls per framework, automatically.

**The model adds one entity:**

```
FrameworkScope {
  id
  framework_version_id            -- which framework version this scope belongs to
  name                            -- "PCI 4.0 CDE", "HIPAA Covered Systems Q3 2026"
  predicate                       -- a boolean over scope dimensions, same DSL as Control.applicability_expr
  effective_from, effective_to    -- scope can change over time (e.g., post-segmentation)
  status                          -- draft | approved | active | retired
  approved_by, approval_evidence  -- who locked this scope, and the artifact (architecture diagram, SOW)
}
```

**Two layers of applicability, intersected:**

```
Control.applicability_expr      // where the control IS applied (engineering reality)
        ∩
FrameworkScope.predicate         // what's in-scope for THIS framework (audit reality)
        =
effective_scope(control, framework)   // cells where the control's evidence COUNTS for this framework
```

**The canonical examples:**

| Framework | Typical FrameworkScope predicate | Lever for scope reduction |
|---|---|---|
| **PCI DSS 4.0** (CDE) | `data_classification IN ('cardholder_data') OR connected_to_chd = true OR security_impacting_chd = true` | Network segmentation, tokenization, P2PE — each removes cells from the CDE |
| **HIPAA Security Rule** (Covered) | `phi_handling = true OR product_line IN ('clinical', 'patient_portal')` | De-identification, BAA-bounded systems boundary |
| **SOC 2** (System) | Auditor-defined; can be any predicate. Often `product_line = 'core_saas'` excluding `internal_tools` | Carving the system definition tightly with the auditor up front |
| **ISO 27001** (ISMS) | Org-defined ISMS scope statement, codified as a predicate | The Statement of Applicability is the formal artifact |
| **NIST CSF / 800-53** (System) | Per-system; for FedRAMP this is the authorization boundary | Boundary scoping is a heavy artifact, often diagrammed |
| **GDPR** | Different shape — predicate over data records, not infrastructure cells. Special-cased. | Data minimization, anonymization |

**How the graph traversal accounts for it:**

When computing coverage for `framework_requirement R`:

1. Walk `R → SCF anchors → controls` (the existing graph traversal).
2. For each candidate control `C`, compute `effective_scope(C, R.framework) = C.applicability_expr ∩ R.framework.scope.predicate`.
3. Coverage is the weighted strength × effectiveness aggregated **only over cells in `effective_scope`**, not over all cells where `C` is applied.

The practical consequences the user actually feels:

- **The PCI dashboard naturally filters down** from the org's full ~200 controls to the ~80 that map (via SCF) to in-scope PCI requirements, evaluated only over CDE cells.
- **Scope-reduction work is a first-class operation.** Removing a system from the CDE is shrinking `FrameworkScope.predicate` — coverage math updates immediately, and the auditor sees a precise before/after.
- **A single control can have different coverage scores for different frameworks at the same time.** Okta MFA might be 1.0 covered for SOC 2 (applied across the whole system) but 0.6 covered for PCI (one of the 5 CDE cells didn't have MFA enforced last week). Each number is honest in its own context.
- **Framework scopes are versioned and audit-evidenced** in their own right — when did the CDE change, why, who approved, against what diagram. This is itself audit evidence.

**Why this is hard for flat-table tools:** they can't intersect "control applicability" with "framework scope" because they don't model framework scope at all — they assume one global scope. Real programs have to maintain the intersection in spreadsheets. The graph model + `FrameworkScope` makes this a query, not a copy-paste exercise.

**FrameworkScope vs. SCF anchor scope mappings:** these are different things. SCF anchors are framework-agnostic concepts. The mapping `FrameworkRequirement → SCF anchor` says "this concept is what the requirement is about." `FrameworkScope` separately says "even though the concept applies broadly, this framework's audit only cares about it in *these* cells." The two intersect at evaluation time.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 4. Evidence Engine](./04-evidence-engine.md) · **Next:** [6. Risk Register Linkage →](./06-risk.md)
