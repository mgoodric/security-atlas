# Governance documents

This directory holds the project's operational governance documents —
the policies and procedures that bind how the project itself is run.
It is distinct from [`docs/adr/`](../adr/) (architectural decisions),
[`Plans/`](../../Plans/) (design intent), and the slice pipeline at
[`docs/issues/`](../issues/) (per-change work-orders).

| Concept                         | Where it lives                          |
| ------------------------------- | --------------------------------------- |
| Architectural decisions         | `docs/adr/`                             |
| Design intent (canvas)          | `Plans/`                                |
| Per-change work-orders (slices) | `docs/issues/`                          |
| **Operational governance**      | **`docs/governance/` ← this directory** |
| Audit reports                   | `docs/audits/`                          |
| Audit decision logs             | `docs/audit-log/`                       |
| Incident logs                   | `docs/incidents/`                       |

The companion governance documents at the repo root —
[`GOVERNANCE.md`](../../GOVERNANCE.md), [`SECURITY.md`](../../SECURITY.md),
[`CONTRIBUTING.md`](../../CONTRIBUTING.md),
[`CODE_OF_CONDUCT.md`](../../CODE_OF_CONDUCT.md), and
[`LICENSE`](../../LICENSE) — sit at the root because they are GitHub-
recognized community-health files and benefit from root-level
discoverability. The documents in this directory complement them with
operational detail.

---

## Current documents

| Document                                                                           | Purpose                                                                                                                                                                          | Filed by  | Reviewed  |
| ---------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------- | --------- |
| [`incident-response.md`](./incident-response.md)                                   | How the project responds to security and operational incidents affecting itself.                                                                                                 | Slice 372 | Annual    |
| [`business-continuity.md`](./business-continuity.md)                               | RTO / RPO targets and restore procedures for the project's own properties; bus-factor & succession operationalized.                                                              | Slice 373 | Annual    |
| [`access-review.md`](./access-review.md)                                           | Periodic-review cadence for the GitHub repository, CI secrets, third-party Apps, signing keys, and PATs.                                                                         | Slice 374 | Annual    |
| [`data-retention.md`](./data-retention.md)                                         | Retention durations and disposal procedures per data category, with framework-floor mapping and legal-hold override.                                                             | Slice 375 | Annual    |
| [`asset-inventory.md`](./asset-inventory.md)                                       | Project asset inventory: code repos, container images, cryptographic material, deploy infrastructure, third-party integrations, documentation surfaces — with criticality tiers. | Slice 376 | Annual    |
| [`board-narrative-tone-anti-patterns.md`](./board-narrative-tone-anti-patterns.md) | Canonical list of phrases the board-narrative AI-assist system prompt rejects.                                                                                                   | Slice 182 | As-needed |

The five governance documents at slices **372 + 373 + 374 + 375 + 376** form the **COMPLETE slice 329 audit-driven governance chain** — every High-severity finding (H-1 through H-5) from the slice 329 compliance meta-audit is closed by a corresponding document above. The chain provides the operator-side policy-artifact surface a third-party security reviewer expects to see before examining technical control evidence.

---

## Planned documents

No governance documents are currently in the planned-but-not-filed
state. The slice 329 audit-driven governance chain is complete; new
governance documents may be filed in response to future audits, new
constitutional commitments, or material project-shape evolution.

---

## Conventions

- **Tone.** Measured, factual, no marketing voice. Follow the
  [board-narrative tone anti-pattern reference](./board-narrative-tone-anti-patterns.md)
  even in non-board contexts where it makes the writing better.
- **Cross-references over duplication.** Governance documents
  cross-reference each other, the canvas, ADRs, and the repo-root
  community-health files; they do not duplicate content.
- **Maintained by the project maintainer.** Per
  [`GOVERNANCE.md`](../../GOVERNANCE.md), all governance documents
  are owned by the project maintainer; changes follow the standard
  slice / PR / DCO process.
- **Review cadence.** Each document declares its own review cadence
  in its header. Most are annual.
- **Capability statements, not certification claims.** These
  documents describe what the project does; they are not third-party
  attestations.
