# 634 — Azure connector: Azure Firewall rule-collection-group cursor pagination

**Cluster:** Connectors
**Estimate:** S (0.5-1d)
**Type:** STANDARD
**Status:** `ready` (depends on #614 — Azure Firewall rule-collection evidence — merged first)

## Narrative

Slice 614 added the `azure.firewall_rules.v1` evidence kind. Its ARM client
(`connectors/azure/internal/firewall`) reads, for each firewall policy, a single
**bounded** page of `Microsoft.Network/firewallPolicies/<policy>/ruleCollectionGroups`,
truncated to `maxRuleCollectionGroupsPerPolicy` (200) and skipped once a run-wide
cap (`maxRuleCollectionGroupsPerRun`, 2000) is reached — the threat-model-D DoS
guard. A firewall policy with more rule-collection groups than the cap has its
list silently truncated.

This is the SAME unbounded-read concern the rest of the Azure connector family
carries (slice 486 R3 for the policy/account/vault list; slice 623 for the
Key-Vault RBAC role-assignment list). It is the shared cursor-pagination
follow-on, scoped here to the Azure-Firewall surface: follow the ARM
`nextLink` cursor on both the `firewallPolicies` list and each policy's
`ruleCollectionGroups` list, behind the existing per-run cap so a huge estate
still cannot exhaust the run.

This was deliberately deferred from slice 614 (see slice 614 decisions-log D5)
to keep that slice scoped to the evidence-kind shape + the over-collection
guard.

## Scope discipline

Cursor pagination on the `firewallPolicies` + `ruleCollectionGroups` reads only.
No new evidence kind, no schema change, no widening of the ARM Reader scope, no
change to the over-collection guard. The per-run cap stays as the DoS backstop.

## Acceptance criteria

- [ ] **AC-1.** The firewall ARM client follows the ARM `nextLink` cursor on the
      `firewallPolicies` list and on each policy's `ruleCollectionGroups` list,
      bounded by the existing `maxRuleCollectionGroupsPerRun` cap.
- [ ] **AC-2.** Unit tests (faked ARM surface, neutral `00000000-...` fixtures)
      cover the multi-page cursor walk and the cap-stops-the-walk path.
- [ ] **AC-3.** No new Azure scope; read-only (GET only); no over-collection
      surface added (the structural guard test stays green).

## Anti-criteria (P0 — block merge)

- **P0-634-1.** Does NOT widen Azure permissions beyond ARM Reader.
- **P0-634-2.** Does NOT remove the per-run cap (it is the DoS backstop even with
  pagination).

## Dependencies

- **#614** (Azure Firewall rule-collection evidence) — parent slice; this is the
  cursor-pagination follow-on slice 614 deferred.
