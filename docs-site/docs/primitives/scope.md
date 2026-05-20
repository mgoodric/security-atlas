# Scope

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - Why scope is a multidimensional space, not a tree
    - How scope cells, applicability expressions, and FrameworkScope
      intersect
    - How to define and use scope in your tenant
<!-- prettier-ignore-end -->

**Scope** in security-atlas is **not a hierarchy**. It is a coordinate
in an N-dimensional space. The platform ships with a default dimension
set; orgs add dimensions when their business requires them.

This shape solves a recurring problem with single-tree scope models:
**PCI CDE is not the same set of systems as HIPAA covered systems is not
the same set as the SOC 2 system description**. A tree forces one of
them to be primary; a multidimensional space lets each framework draw
its own predicate over the shared space.

## The default dimensions

| Dimension             | Default values                                                |
| --------------------- | ------------------------------------------------------------- |
| `business_unit`       | Org-defined.                                                  |
| `environment`         | `prod` · `staging` · `dev` · `sandbox`                        |
| `geography`           | ISO 3166 country codes / regions.                             |
| `cloud_account`       | Per cloud — AWS account, GCP project, Azure sub, K8s cluster. |
| `data_classification` | `restricted` · `confidential` · `internal` · `public`         |
| `product_line`        | Org-defined.                                                  |

A **scope cell** is a tuple of values across these dimensions:

```
(business_unit=platform, environment=prod, geography=us-east-1,
 cloud_account=aws-123456, data_classification=restricted,
 product_line=core)
```

Every Evidence record, every Control evaluation, every Risk lives in
one or more scope cells.

## Applicability expressions

A Control has an `applicability_expr` — a boolean over scope dimensions
that defines **where the control applies**:

```
environment IN ('prod', 'staging')
AND data_classification IN ('restricted', 'confidential')
```

The expression evaluates per scope cell. The control's state is
`n/a` for cells where the expression is false, and computed against
evidence for cells where the expression is true.

This means the same control statement ("Encryption at rest for all
data") evaluates against the right subset automatically — you don't
maintain "Encryption at rest — prod" and "Encryption at rest — dev" as
separate controls.

## FrameworkScope — the second predicate

Each framework also has a **FrameworkScope** — a separate predicate
over the same dimensions defining **what is in scope for that
framework's audit**. This is the practical answer to "PCI CDE vs HIPAA
covered vs SOC 2 system":

| Framework | FrameworkScope predicate (example)                                                                    |
| --------- | ----------------------------------------------------------------------------------------------------- |
| SOC 2     | `product_line IN ('core', 'enterprise') AND environment IN ('prod', 'staging')`                       |
| PCI DSS   | `data_classification = 'restricted' AND env_tag CONTAINS 'cardholder-data' AND environment = 'prod'`  |
| HIPAA     | `data_classification = 'restricted' AND env_tag CONTAINS 'phi' AND business_unit = 'health-platform'` |
| ISO 27001 | `business_unit IN ('platform', 'product') AND environment IN ('prod', 'staging')`                     |

A control's **effective scope for a framework** is the intersection:

```
effective_scope(control, framework) = applicability_expr ∩ framework_scope.predicate
```

So the same control applies in different ways for different audits —
without you authoring per-framework versions.

## Defining scope cells

Sign in as an admin and open **Settings → Scope** in the sidebar. The
scope editor lets you:

- Add or modify dimensions (e.g. add `regulatory_regime` if you operate
  in multiple jurisdictions).
- List the active scope cells (combinations actually present in your
  systems).
- Tag any cell with custom annotations (`env_tag = 'cardholder-data'`,
  for example) — annotations are what enable framework-specific
  predicates like the PCI DSS one above.

Scope cells are typically discovered, not authored. The AWS connector
(and every other source connector) tags evidence with the scope cell
inferred from the source — region, account, tags — and the platform
records the cell on first observation.

## Defining a FrameworkScope

Open **Frameworks** in the sidebar, click the framework name, then
**Scope** in the sub-nav:

1. Set the predicate using the visual builder (or paste a predicate
   string).
2. The platform shows you the cells the predicate currently selects.
3. Click **Save** — the predicate is versioned; existing audit periods
   pin to the version they were created with.

Changing a FrameworkScope **does not retroactively change frozen
[AuditPeriods](../first-audit.md)** — they keep the version they froze
against. Live evaluation switches to the new predicate from the next
evaluation cycle.

## Scope in evidence

Every Evidence record carries a `scope_id` — the cell the observation
applies to. The ingestion stage tags this automatically:

- For AWS S3 bucket-encryption evidence, the cell is inferred from the
  account ID and region.
- For manual evidence, the uploading user picks the cell from a
  dropdown.
- For pushed evidence, the pusher supplies the cell ID (or a tag-set
  the platform resolves to a cell).

Records that cannot resolve to a cell are **rejected at ingest** with
`rejected_scope_violation`. Anonymous or scope-less evidence is
explicitly not supported.

## Next steps

- [Controls →](controls.md) — what applies in scope
- [Framework →](framework.md) — what scope a framework draws over
- [Evidence →](evidence.md) — what scope evidence carries

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
