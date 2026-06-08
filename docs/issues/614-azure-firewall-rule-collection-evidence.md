# 614 — Azure connector: Azure Firewall rule-collection evidence

**Cluster:** Connectors
**Estimate:** M (1-2d)
**Type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Status:** `ready` (depends on #520 — Azure NSG evidence — merged first)

## Narrative

Slice 520 added NSG (Network Security Group) firewall-rule evidence
(`azure.nsg_rules.v1`) to the Azure connector. The slice-520 spec named **Azure
Firewall network/application rule collections** as an explicit OPTIONAL extension
that slice 520 deferred to keep its scope to NSGs only:

> "NSG security rules + (optional) Azure Firewall network/app rule collections
> only."

This slice ships that deferred Azure Firewall surface as a SEPARATE sibling kind
`azure.firewall_rules.v1` on the same `atlas-azure` binary. Azure Firewall (the
managed `Microsoft.Network/azureFirewalls` + the
`Microsoft.Network/firewallPolicies` rule-collection-group resources) is a
distinct boundary-protection control point from NSGs — a centralized,
policy-based L3-L7 firewall rather than per-subnet/per-NIC NSG rules — so it
answers a sibling network-segmentation question at the network-perimeter grain.

slice-520 pattern verbatim: a new `connectors/azure/internal/firewall` collector
plus ARM client, plus a new `azure.firewall_rules.v1` evidence kind plus a schema
with `x-default-scf-anchors` (NET-04 + NET-01, the same boundary-protection
anchors the NSG kind uses), registered in `DefaultSeed`, faked ARM surface in
tests. Push only (invariant #3); `profiles_supported=[pull]`; honest interval.

**Scope discipline.** Azure Firewall network + application rule collections (and
the rule-collection-group priority ordering) only. NOT NAT rule secrets, NOT
threat-intel feeds, NOT flow logs, NOT route tables.

## Threat model

Inherits the slice-486 / slice-520 connector-family threat model verbatim.

- **S — Spoofing.** Reuses the existing connector push credential + the read-only
  ARM **Reader** role; credential stays source-side.
- **T — Tampering.** sha256 content-hash per record; ingest validates.
- **R — Repudiation.** register-per-run + stable `actor_id`
  (`connector:azure:firewall@<version>`) + documented `observed_at` granularity.
- **I — Information disclosure.** Firewall RULE configuration only — never flow
  logs, packet captures, or traffic contents.
- **D — Denial of service.** Bounded ARM list page + per-run cap + run timeout; a
  large estate needs the cursor-pagination follow-on (shared with 486 R3).
- **E — Elevation of privilege.** ARM **Reader** only — no Network Contributor,
  no rule mutation (read-only list of rule collections).

## Acceptance criteria

- [ ] **AC-1.** A new `internal/firewall` collector reads Azure Firewall network + application rule collections via read-only ARM, faked in tests.
- [ ] **AC-2.** A new `azure.firewall_rules.v1` evidence kind + JSON Schema with
      `x-default-scf-anchors` lands in the schema-registry tree + `DefaultSeed`
      (the schema-drift bijection passes with the new kind).
- [ ] **AC-3.** Each record pushes via the single `IngestEvidence` API — no wire
      change; sha256 content-hash; `profiles_supported=[pull]`.
- [ ] **AC-4.** A structural over-collection test asserts the connector reads
      rule configuration ONLY (no flow logs / packet data / NAT secrets).
- [ ] **AC-5.** `run` threads the firewall kind (seam + `--firewall-control`
      flag + `--skip-firewall`) exactly as 520 did for NSG; coverage floor for
      the new `firewall` package.
- [ ] **AC-6.** README + decisions log + changelog updated.

## Anti-criteria (P0 — block merge)

- **P0-614-1.** Does NOT widen Azure permissions beyond the existing ARM Reader
  role.
- **P0-614-2.** Does NOT read flow logs / packet captures / traffic contents /
  NAT-rule secrets.
- **P0-614-3.** Does NOT mutate any network resource (read-only).
- **P0-614-4.** Does NOT widen the platform-side wire (push only).

## Dependencies

- **#520** (Azure NSG evidence) — `merged`. Parent slice; this is the deferred
  Azure Firewall extension slice 520 named as out-of-scope.
