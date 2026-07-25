# 614 — Azure Firewall rule-collection evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls for
slice 614 — the evidence-kind shape (what the `azure.firewall_rules.v1` payload
carries and how the ARM read is structured), the scope-minimum + structural
over-collection-guard mechanism, and the stable-field choices (idempotency key,
actor_id, scope, default SCF anchors). It does NOT block merge; the maintainer
iterates post-deployment from the "Revisit once in use" list.

Parent: slice 520 (`docs/audit-log/520-azure-nsg-firewall-evidence-decisions.md`).
This slice ships the Azure-Firewall surface that slice 520 named as an explicit
OPTIONAL extension and deferred to keep its scope to NSGs only. Azure Firewall
(`Microsoft.Network/firewallPolicies` rule-collection groups) is a distinct
boundary-protection control point from NSGs — a centralized policy-based L3-L7
firewall rather than per-subnet/per-NIC NSG rules — so it answers the sibling
network-segmentation question at the network-perimeter grain, mapped to the
SAME SCF anchors (NET-04 / NET-01).

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** unit. One build-time correction surfaced during the
  slice and was caught at the unit tier by `go test` before push: the ARM
  `ruleCollectionType` discriminator does NOT carry the network-vs-application
  distinction (Azure tags both filter collections
  `FirewallPolicyFilterRuleCollection`); the `collectionType` mapper initially
  derived the type from that field and the client test caught the wrong value.
  Fixed in the SAME change by deriving the collection type from each rule's
  `ruleType` (`NetworkRule` / `ApplicationRule`). A second unit-tier signal — the
  package dipping to 89.2% (below the 90 floor) after the client error-path and
  edge branches landed — was caught by `go test -cover` before push and fixed in
  the SAME change by adding the missing branch tests (group-read bad-JSON
  fallback, empty + untyped rule collections, port edge cases), lifting it to
  92.8%.
- **detection_tier_target:** unit. A discriminator-mapping mistake and a
  coverage-floor regression are exactly the class of issue the per-package unit
  gate exists to catch; both were caught there. No product-behavior bug
  surfaced; there were no fix-forward defects.

## Decisions made

### D1 — Evidence-kind shape: a per-firewall-policy record carrying the nested rule-collection-group tree

- **Options:** (a) one record per firewall **policy**, carrying its
  rule-collection groups (with priority ordering) → collections → rules as a
  nested payload tree; (b) one record per rule **collection**; (c) one record
  per individual rule.
- **Chosen:** (a). The firewall **policy** is the resource an operator owns and
  reasons about (it is what attaches to a firewall and what an SCF
  network-segmentation control is evaluated against), exactly as the NSG kind
  emits one record per NSG. The rule-collection-group priority ordering is a
  load-bearing signal the slice spec names explicitly, and it only makes sense
  relative to the whole policy — so the group/collection/rule tree rides inside
  the one record. This mirrors slice 520 (one `GroupConfig` per NSG carrying its
  rule list) and the Key-Vault kind (one record per vault carrying its access
  entries), keeping the connector's record-granularity convention uniform.
- **Rejected (b)/(c):** a per-collection or per-rule record would shred the
  rule-collection-group ordering and force the evaluator to re-stitch a policy
  from many records keyed by parent id — the ordering is the signal, so it must
  stay whole.

### D2 — ARM read shape: policy list + a per-policy `ruleCollectionGroups` second read, inside the concrete `Client`

- **Options:** (a) the `firewallPolicies` list returns the policy identity only;
  issue a second scoped read of each policy's `ruleCollectionGroups` inside
  `Client.ListFirewallPolicies` and merge before returning the `RawPolicy`; (b)
  widen the `firewall.API` interface with a second method `Inspect` calls
  per-policy; (c) merge in the `cmd` collector layer.
- **Chosen:** (a). This is the same shape slice 615 adopted for the Key-Vault
  RBAC read: the rule-collection groups are how the ARM client resolves a
  policy's rules, not a separate collector concept, so they belong inside the
  client. `Inspect`, the `firewall.API` interface, the `cmd` seam fakes, and the
  builder stay narrow and uniform with the sibling kinds. A per-policy
  `ruleCollectionGroups` read error marks the policy INCONCLUSIVE (the same
  fail-soft contract the NSG `ReadError` and Key-Vault RBAC read use) rather than
  failing the whole run — one throttled policy must not blind the connector to
  the rest of the estate.
- **Rejected (b)/(c):** widening the interface or pushing the read into `cmd`
  would force every fake to learn a second call and put an ARM read where no ARM
  client lives, for no behavioral gain.

### D3 — Scope-minimum + structural over-collection guard (P0-614-1 / P0-614-2 / P0-614-3)

- **Scope minimum:** ARM **Reader** alone, the SAME role every ARM-sourced kind
  uses — no Network Contributor, no rule mutation, no new Azure scope
  (P0-614-1). The only ARM operations issued are `GET .../firewallPolicies` and
  `GET .../firewallPolicies/<policy>/ruleCollectionGroups`; the client has no
  write/POST code path, so rule mutation is unreachable by construction
  (P0-614-3). The client test asserts every issued request is a GET.
- **Over-collection guard mechanism (the load-bearing call):** the guard is
  **structural**, mirroring slice 520/615. The collector record structs
  (`PolicyConfig`, `RuleCollectionGroup`, `RuleCollection`, `Rule`, `RawPolicy`)
  carry rule CONFIGURATION metadata ONLY — there is deliberately NO field
  capable of holding a flow log, packet capture, traffic content, NAT-rule
  secret, threat-intel feed, or route table. If the struct physically cannot
  hold the excluded data, the connector cannot emit it even if a future ARM API
  version exposed it on the same resource. A reflection-based test
  (`TestStructs_RuleConfigOnly_NoTrafficSecretOrThreatIntelFields`) walks every
  struct's field names and FAILS the build if any name even hints at one of the
  excluded surfaces, so a future field that opens an over-collection door trips
  CI. The `cmd` record builder and the bufconn integration test add a per-level
  allow-list + banned-substring scan that descends the whole group → collection
  → rule tree as a second, payload-layer pin (P0-614-2).
- **Why no DB migration:** evidence kinds are registered via the schemaregistry
  `DefaultSeed` slice + an embedded JSON Schema file (`x-evidence-kind`), not a
  DB migration. The kind ↔ schema bijection test
  (`internal/control/evidence_kind_drift_test.go`) is the enforcement surface;
  it passes with the new kind. No migration was needed or written.

### D4 — Stable-field choices

- **`actor_id`:** `connector:azure:firewall@<version>` — the cross-connector
  `connector:<vendor>:<service>@<version>` convention, with `firewall` as the
  service segment (distinct from `nsg`).
- **`idempotency_key`:** `sha256("azure.firewall_rules|<policy_id>|<hour>")` via
  a new `idem.FirewallRulesKey`, hour-truncated in UTC so two runs within the
  same hour for the same firewall policy collapse to one ledger row (the locked
  connector pattern); a distinct-from-other-kinds test pins no cross-kind
  collision on the same id.
- **`observed_at` granularity:** hour-truncated UTC (documented in the README),
  the same granularity every Azure kind uses, chosen so re-runs within an hour
  dedup.
- **Default SCF anchors:** `x-default-scf-anchors=[NET-04, NET-01]` — the SAME
  boundary-protection anchors the NSG kind uses, because Azure Firewall answers
  the same network-segmentation question at the perimeter grain. Per the AI-assist
  boundary these are default mapping HINTS flagged for maintainer recheck
  (OQ #9), not an auto-approved mapping.
- **Default control id:** `--firewall-control` defaults to `scf:NET-04`, matching
  the NSG kind's `--nsg-control`.
- **Descriptive verdict:** the connector emits a deterministic descriptive
  default (FAIL when an allow rule collection permits whole-Internet traffic to a
  management port 22/3389; INCONCLUSIVE on a per-policy read error; PASS
  otherwise) — the platform evaluator owns the final pass/fail per (control,
  firewall policy). Application rules (FQDN-based, no port surface) cannot match
  the management-port heuristic and are skipped by the verdict.

### D5 — DoS guard: bounded page + per-run cap; cursor pagination is a follow-on

- v0 reads the first **bounded** page of firewall policies for one subscription,
  and each per-policy `ruleCollectionGroups` read is truncated to
  `maxRuleCollectionGroupsPerPolicy` (200) and skipped entirely once a run-wide
  cap (`maxRuleCollectionGroupsPerRun`, 2000) is reached — the same defensive
  bound the Key-Vault RBAC read uses. This is the threat-model-D DoS guard.
- Cursor pagination for a huge estate (and multi-subscription auto-enumeration)
  is the SHARED follow-on, deliberately NOT implemented here. Filed as spillover
  **slice 634** (`docs/issues/634-azure-firewall-rulecollectiongroup-pagination.md`).

## Revisit once in use

- The management-port (22/3389) verdict heuristic is descriptive and deliberately
  narrow; the real pass/fail lives in the evaluator. If operators want the
  connector-side verdict to flag more (e.g. any allow-from-Internet collection,
  not just to management ports), that is an evaluator-rule change, not a
  connector change.
- The bounded-page caps (200 per policy / 2000 per run) are conservative
  defaults; revisit if a real estate's policies legitimately exceed them before
  the cursor-pagination follow-on (slice 634) lands.
- Application-rule semantics: v0 captures application-rule target FQDNs and
  protocols but the descriptive verdict only scores network rules. If
  application-layer egress posture becomes a verdict signal, extend `verdict`
  (and the evaluator) — the FQDN data is already in the payload.
