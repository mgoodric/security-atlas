# Framework

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What Framework and FrameworkVersion mean in security-atlas
    - How crosswalk through SCF anchors works (and why we never
      duplicate controls)
    - How to add a framework version, view satisfaction, and switch
      versions safely
<!-- prettier-ignore-end -->

A **Framework** in security-atlas is `ISO 27001` or `SOC 2` or `PCI
DSS`. A **FrameworkVersion** is `ISO 27001:2022` (distinct from `ISO
27001:2013`), `SOC 2 v2017 TSC`, or `PCI DSS v4.0`. Mappings are
**version-pinned** — upgrading from `2013` to `2022` is an explicit
migration, never an in-place mutation.

## The shape

| Framework field     | Notes                               |
| ------------------- | ----------------------------------- |
| `id`, `slug`        | `iso_27001`, `soc_2`, `pci_dss`     |
| `name`              | Display name.                       |
| `issuer`            | ISO/IEC, AICPA, PCI SSC, etc.       |
| `latest_version_id` | Pointer to current default version. |

| FrameworkVersion field           | Notes                                |
| -------------------------------- | ------------------------------------ |
| `framework_id`                   | Parent framework.                    |
| `version`                        | `2022`, `r5`, `v4`, `2024`, `v2017`. |
| `effective_from`, `effective_to` | Standard's own lifecycle.            |
| `status`                         | `current` · `legacy` · `withdrawn`   |
| `requirement_count`              | Denormalized for quick display.      |
| `oscal_catalog_uri`              | When we have an OSCAL ingest.        |

## The UCF — one control, N framework satisfactions

Constitutional invariant 1: **one control, N framework satisfactions**.
The Unified Control Framework is a graph; framework requirements map to
SCF anchors, controls map to SCF anchors, and the platform computes
which controls satisfy which requirements through the shared anchor
layer.

```
[ Framework Requirement ] ──► [ SCF Anchor ] ◄── [ Control ]
   "SOC 2 CC6.1"                 "IAC-01"          (your impl)
```

Edges carry a **STRM relationship type** (per NIST IR 8477):

| STRM type         | Meaning                                                                   |
| ----------------- | ------------------------------------------------------------------------- |
| `equal`           | The requirement and the anchor are interchangeable.                       |
| `subset_of`       | The requirement is a subset of the anchor.                                |
| `superset_of`     | The requirement is broader than the anchor.                               |
| `intersects`      | They partially overlap.                                                   |
| `no_relationship` | Explicit non-mapping. Recorded to defeat the "is it just missing?" doubt. |

Two consequences:

- **Adding a framework is mostly mapping**, not control authoring. If
  your control set already covers SCF anchor `IAC-01`, adding ISO
  27001:2022 means linking ISO `A.5.15` to `IAC-01` — not authoring a
  new "ISO version" of an identity-and-access control.
- **Removing a framework does not delete controls**. Controls are
  yours; framework satisfaction is a graph view over them.

## Adding a framework version

The default install seeds the SCF catalog and SOC 2 v2017. To add
another framework version:

```sh
# Import the OSCAL catalog (NIST IR 8477 mappings included if present).
just atlas-cli catalog import \
  --framework iso_27001 \
  --version 2022 \
  --catalog ./catalogs/iso-27001-2022.oscal.json
```

The importer:

1. Validates the OSCAL catalog against the v1.1.x schema.
2. Loads requirements as `FrameworkRequirement` rows pinned to the new
   version.
3. Loads the STRM mappings (if the catalog carries them) as edges to SCF
   anchors.
4. Reports the satisfaction view — how many requirements have at least
   one currently-passing control.

Mappings the catalog doesn't carry are added manually via the
**Frameworks → Mappings** UI (or in bulk via an OSCAL profile import).

## Viewing satisfaction

Open **Frameworks** in the sidebar, click a framework, then a version.
The version detail view shows:

- Requirement-by-requirement satisfaction (pass / fail / no mapping
  yet / no evidence yet).
- The STRM relationship type for each mapped control.
- The current effective scope (per [Scope](scope.md) §FrameworkScope).
- Cross-framework view — how requirements in this framework overlap
  with another active framework (via shared SCF anchors).

## Switching versions

Frameworks evolve — ISO 27001:2013 → 2022, PCI DSS v3.2.1 → v4.0. The
platform treats this as a migration:

1. Import the new version (above).
2. Open **Frameworks → \<framework\> → Compare versions** to see which
   requirements moved, merged, or split.
3. For each changed requirement, the platform suggests an updated SCF
   anchor mapping (drawing on the STRM data in the new catalog). The
   human reviews and approves — the [AI-assist
   boundary](https://github.com/mgoodric/security-atlas/blob/main/CLAUDE.md#ai-assist-boundary-hard)
   applies.
4. Click **Set as active** when you're ready to cut over.

Open [AuditPeriods](../first-audit.md) pinned to the old version
continue to evaluate against the old version; new periods use the new
version.

## Framework + scope = effective audit set

The effective set of controls in scope for an audit is the intersection
of:

- The framework's `FrameworkScope` predicate (where the framework
  applies)
- Each control's `applicability_expr` (where the control applies)

```
in_audit(control, framework) = applicability_expr ∩ framework_scope.predicate
```

This is what the auditor reads. See [Scope](scope.md) for the full
predicate language and [First audit](../first-audit.md) for how the
intersection becomes an OSCAL SSP.

## Next steps

- [Controls →](controls.md) — what satisfies framework requirements
- [Scope →](scope.md) — what restricts framework applicability
- [Framework setup →](../framework-setup.md) — the end-to-end loader
  walkthrough
- [First audit →](../first-audit.md) — how framework state becomes an
  audit artifact

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
