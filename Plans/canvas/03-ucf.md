**security-atlas canvas** · [← index](../ARCHITECTURE_CANVAS.md)

---

# 3. The Unified Control Framework

This is the section the existing tools get wrong. It is the heart of the platform.

> **Deep dive:** the graph model is fully worked out — diagrams, traversal queries, versioning, storage decisions, and a concrete "one MFA evidence record satisfies six frameworks" walkthrough — in the companion document [`UCF_GRAPH_MODEL.md`](../UCF_GRAPH_MODEL.md).

## 3.1 The graph, not the spreadsheet

Vanta-shaped tools maintain framework crosswalks as flat tables: `(control_in_framework_A, control_in_framework_B)`. This decays with every framework revision and silently rounds N:M relationships to 1:1.

security-atlas models the UCF as a **directed labeled graph**:

- **Nodes:** every requirement in every FrameworkVersion (e.g., `SOC2:2017:CC6.1`, `ISO27001:2022:A.5.15`, `NIST_CSF:2.0:PR.AA-01`, `PCI:4.0:7.2.1`).
- **Spine nodes:** SCF controls (`SCF:IAC-01`) acting as semantic-equivalence-class anchors.
- **Edges:** STRM-typed mappings (per [NIST IR 8477](https://csrc.nist.gov/pubs/ir/8477/final)) between requirements and SCF anchors, never directly between framework requirements.

Because all framework-to-framework relationships are derived through SCF anchors, mappings stay coherent under versioning: an ISO 27001:2013 → ISO 27001:2022 update changes only the edges from ISO requirements to SCF, not the SCF graph itself.

## 3.2 STRM mapping cardinality

NIST IR 8477 defines five relationship types, each with a strength score 0.0–1.0:

| Relationship      | Meaning                                                                        | Example                                              |
| ----------------- | ------------------------------------------------------------------------------ | ---------------------------------------------------- |
| `subset_of`       | Source is fully covered by target.                                             | `ISO27001:A.9.4.2 subset_of SCF:IAC-22`              |
| `superset_of`     | Source covers more than target.                                                | `SCF:IAC-01 superset_of SOC2:CC6.1` (SCF is broader) |
| `intersects_with` | Partial overlap.                                                               | `PCI:8.3 intersects_with HIPAA:164.312(d)`           |
| `equal`           | Logically equivalent.                                                          | `NIST_800_53:AC-2 equal SCF:IAC-15`                  |
| `no_relationship` | Confirmed _no_ overlap. (Yes, this is data — it suppresses false suggestions.) |                                                      |

A **strength** field captures auditor judgment: `(equal, 1.0)` is full confidence; `(intersects_with, 0.4)` flags partial coverage that needs supplemental evidence.

This means **one piece of evidence can satisfy N controls automatically** when their SCF anchors are connected, and the platform can compute _coverage strength_ per requirement: if your evidence covers SCF:IAC-22 with strength 1.0, and ISO27001:A.9.4.2 → SCF:IAC-22 with strength 0.8, the ISO requirement is covered at 0.8 — and the UI surfaces the gap.

## 3.3 Versioning strategy

- `FrameworkVersion` is immutable once `status='current'`. Changes ship as new versions.
- Mappings (`requirement → SCF`) are pinned to a `FrameworkVersion` AND a `SCF release`. The mapping table has its own version lineage.
- A `framework_version_migration` job suggests likely 1:1 mappings between adjacent versions, flagging the rest for human review. Rotting is bounded by SCF release cadence (quarterly), not the user's audit calendar.

## 3.4 OSCAL ingest and export

| Direction | OSCAL model                              | Use                                                                               |
| --------- | ---------------------------------------- | --------------------------------------------------------------------------------- |
| Ingest    | `catalog`                                | Import a framework version (NIST 800-53r5 catalog ships from NIST as OSCAL JSON). |
| Ingest    | `profile`                                | Import a tailored baseline (FedRAMP Moderate).                                    |
| Ingest    | `component-definition`                   | Import "this AWS service satisfies these controls" definitions.                   |
| Export    | `system-security-plan` (SSP)             | Generate the SSP for an auditor.                                                  |
| Export    | `assessment-plan` / `assessment-results` | Generate the audit plan and what we found.                                        |
| Export    | `plan-of-action-and-milestones` (POA&M)  | Track open findings to remediation.                                               |

We use [IBM compliance-trestle](https://github.com/oscal-compass/compliance-trestle) under the hood for OSCAL serialization; it is the most mature OSCAL SDK and CNCF/Linux Foundation–affiliated.

## 3.5 SCF as the canonical catalog

We ship SCF (latest release) as the default control catalog. Users can:

- Use SCF directly as their internal control library.
- Override with a custom catalog while keeping SCF as the mapping spine.
- Import additional catalogs (NIST 800-53, CSA CCM if licensed) as alternative anchor sets.

We treat **CSA CCM** as opt-in import for cloud-native overlays, because its commercial-product embed terms are murky for a distributed OSS product. We treat **UCF Common Controls Hub** as off-limits — proprietary IP.

---

[← Canvas index](../ARCHITECTURE_CANVAS.md) · [← 2. Primitives](./02-primitives.md) · **Next:** [4. Evidence Engine →](./04-evidence-engine.md)
