# 520 — Azure NSG / firewall-rule evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shape, the security-rule field set, the SCF anchors, the
scope-minimum, the descriptive verdict, and THE load-bearing call — the
structural over-collection boundary that keeps flow logs / packet captures /
traffic contents / secrets out of the record). It does NOT block merge; the
maintainer iterates post-deployment from the "Revisit once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** integration (one build-time correction surfaced
  there — see below).
- **detection_tier_target:** integration.

The one build-time correction: the integration no-PII test (`TestEmittedRecords_NoPIIOrSecrets`)
caught that adding `"traffic"` to the SHARED banned-substring list collided with
the storage kind's legitimate `https_traffic_only` payload key (a false
positive). The fix was to keep the flow-log/packet/traffic-content bans in an
NSG-scoped `nsgBannedSubstrings` list applied only to the NSG record + its
nested rule items, leaving the shared list untouched. This is exactly the tier
that should catch a cross-kind allow-list interaction, so `actual == target`.
The remaining build-time corrections were the expected consequence of adding a
fourth evidence kind to the slice-486/519 connector: the `cmd` seam harness
needed a default no-op `nsgScan` seam so the on-by-default NSG pull never reaches
the live ARM client in the existing seam tests, the register integration test's
`supported_kinds` count moved 3 → 4, and the `actorID` / `buildNSGRecord` /
`mapNSGResult` coverage top-ups — all authoring fixes at the unit tier, none a
product-behavior defect.

## Decisions made

### D1 — A SEPARATE sibling kind `azure.nsg_rules.v1`, mirroring the slice-519 AKS kind

- **Options:** (a) a new sibling kind on the existing Azure connector; (b) fold
  NSG facts into `azure.storage_account_config.v1` or
  `azure.aks_cluster_config.v1` (rejected immediately — unrelated resource);
  (c) a brand-new connector binary.
- **Chosen:** (a). A new `connectors/azure/internal/nsg` collector + a new
  `azure.nsg_rules.v1` kind on the SAME `atlas-azure` binary, registered
  alongside the three slice-486/519 kinds. This is the slice-486/519 pattern
  verbatim (the spec says so) — the AKS kind (also ARM-Reader-sourced) is the
  direct template: `nsg.Inspect` mirrors `aks.Inspect`, `nsg.Client` mirrors
  `aks.Client`, `idem.NSGRulesKey` mirrors `AKSClusterConfigKey`,
  `buildNSGRecord` mirrors `buildAKSRecord`.
- **Rationale:** NSG segmentation answers a network-control question distinct
  from cluster or storage hardening, but it is the same connector's source (ARM,
  same Reader role, same subscription scope), so a sibling kind on the same
  binary is the minimum-surface choice. A new binary (c) would duplicate the
  auth + register + push plumbing for no benefit.

### D2 — Field set: per-NSG rule list + association counts, no traffic, no secrets

- **Decision:** one record per NSG carrying the operator-authored security rules
  — each rule's `name`, `direction` (inbound/outbound), `access` (allow/deny),
  `protocol`, `priority`, `source_address_prefix`, `destination_address_prefix`,
  `source_port_range`, `destination_port_range` — plus the identity/scope facts
  (`nsg_id`, `nsg_name`, `subscription_id`, `resource_group`, `location`) and
  summary `associated_subnets` / `associated_nics` counts.
- **ARM default security rules excluded:** only the operator-authored
  `securityRules` are emitted. The ARM `defaultSecurityRules` (AllowVNetInBound,
  DenyAllInBound, …) are identical across every NSG and add no compliance
  signal, so they are dropped at the client mapping layer.
- **Associations as COUNTS, not topology:** the spec asks for associations "at a
  summary level". v0 emits the subnet/NIC association COUNT only — not the
  associated resource ids — because the count is the segmentation-coverage
  signal ("is this NSG actually attached to anything?") without dragging in VNet
  topology (an explicit non-goal of the spec).
- **The load-bearing call — structural over-collection boundary (P0-520-2):**
  the `nsg.GroupConfig`, `nsg.SecurityRule`, and `nsg.RawGroup` structs carry
  RULE METADATA ONLY. There is deliberately NO field for flow logs, packet
  captures, traffic contents, secrets, or PID/PII — there is no place for such
  data to land even if a future ARM field exposed it. This is enforced two ways:
  1. `TestStructs_RulesOnly_NoTrafficOrSecretFields` reflects over ALL THREE
     structs' field names and fails the build if any field name contains a
     banned token (`flowlog`, `flow_log`, `packet`, `capture`, `payload`,
     `traffic`, `content`, `secret`, `credential`, `password`, `token`, …).
  2. The ARM client (`internal/nsg/client.go`) issues a `GET` to the
     networkSecurityGroups **list** surface only and NEVER POSTs / PUTs /
     PATCHes / DELETEs (P0-520-3: read-only, never mutates a network resource;
     P0-520-2: the list surface returns rule config, never flow logs). The
     client test asserts the method is `GET`.
- **Empty optionals omitted:** the string optionals (`resource_group`,
  `location`) and the per-rule string optionals (`protocol`, prefixes, port
  ranges) are omitted from the payload when empty, mirroring the storage / AKS
  builders. The association counts are always present (0 is signal, not
  absence).

### D3 — SCF anchors: NET-04 + NET-01 (`x-default-scf-anchors`)

- **Decision:** `x-default-scf-anchors: ["NET-04", "NET-01"]`; the `run`
  subcommand's `--nsg-control` default is `scf:NET-04`.
- **Rationale:** NSG security rules are squarely a network-boundary /
  segmentation control. `NET-04` (Boundary Protection) is the firewall /
  segmentation anchor — the exact "no 0.0.0.0/0 inbound on management ports"
  posture this kind proves — and is already present in the seed (the slice-486
  storage kind + slice-519 AKS kind both reference it). `NET-01` (Network
  Security) is the broader network-security anchor for the general
  allow/deny-rule posture. Both anchors are already present in
  `migrations/fixtures/scf-sample.json`, so the bijection drift-guard is happy.
  These are default mapping HINTS flagged for maintainer recheck (OQ #9), not a
  binding mapping — the per-run `--nsg-control` flag overrides per deployment.
- **NET-04 over CFG-02:** unlike the AKS kind (whose anchor list leads with the
  config-baseline anchor CFG-02), the NSG kind leads with NET-04 because a
  firewall rule set is fundamentally a boundary-protection artifact, not a
  baseline-configuration artifact.

### D4 — Scope minimum: `cloud_account=azure:<subscription_id>` + `environment`

- **Decision:** every NSG record carries exactly two scope dimensions —
  `cloud_account=azure:<subscription_id>` and `environment=<--environment>` —
  identical to the storage + AKS kinds (NSGs are subscription-scoped ARM
  resources). `run` fails loudly when `--environment` is unset rather than
  pushing an un-scoped record, and requires `--subscription-id` unless
  `--skip-nsg` is set.
- **Rationale:** the connector-pattern scope minimum; the subscription is the
  account-equivalent for ARM resources (slice 486 D for storage, 519 D4 for
  AKS).

### D5 — Result semantics: descriptive deterministic verdict, evaluator owns final pass/fail

- **Decision:** `FAIL` when any inbound, allow rule with an Internet-equivalent
  source (`*` / `0.0.0.0/0` / `Internet` / `any`) targets a management port
  (SSH 22 or RDP 3389) — directly, via a `*` port, or via a numeric range that
  spans 22/3389; `INCONCLUSIVE` when a per-NSG read errored; `PASS` otherwise
  (including an empty rule set — a freshly-created NSG with no operator rules has
  no open management port). The full rule set rides in the payload for the
  platform evaluator to interpret; the connector verdict is a descriptive
  default, not the binding decision.
- **Rationale:** the open-management-port-from-the-Internet check is THE
  canonical NSG-segmentation failure the spec names ("prove no NSG allows
  0.0.0.0/0 inbound on management ports"). Restricting it to ports 22/3389 keeps
  the descriptive default precise and low-false-positive; a broader "any open
  inbound" verdict would over-fail legitimate public web tiers (an NSG allowing
  443 from the Internet is normal). The richer per-rule facts let the evaluator
  apply a tenant's actual policy.

### D6 — Coverage floor + no migration / no wire change

- New package `connectors/azure/internal/nsg` enrolled in
  `cmd/scripts/coverage-thresholds.json` at a **90%** floor (measured 92.0%).
- NO platform API route, NO migration — evidence flows through the existing
  slice-013 ledger via the single `IngestEvidence` push RPC (invariant #3); the
  platform-side wire is unchanged. `profiles_supported` stays `[pull]`; the
  interval stays honestly named (operator-scheduled, NOT continuous monitoring).

## Revisit once in use (maintainer iteration list)

- **SCF-anchor recheck (OQ #9).** Confirm NET-04 + NET-01 are the right default
  anchors for NSG rule posture, or add an access-control anchor if a deployment's
  mapping proves too coarse.
- **Azure Firewall policy rule collections.** The spec flags Azure Firewall
  network/app rule collections as an OPTIONAL extension. v0 ships NSGs only; the
  Azure Firewall surface is filed as spillover (614) for a sibling follow-on.
- **Richer association detail.** v0 summarizes subnet/NIC associations as counts.
  A follow-on could emit the associated subnet/NIC ids if a control demands that
  grain — but only if it stays topology-light.
- **Cursor pagination / multi-subscription enumeration.** v0 reads the first
  bounded ARM page for one subscription (threat-model D, shared with slice 486
  R3). A large NSG estate needs the shared cursor-pagination follow-on.
- **Effective security rules.** v0 reads the declared `securityRules`. ARM also
  exposes a per-NIC "effective security rules" view that resolves rule
  precedence + default rules. A follow-on could emit that resolved view for a
  higher-fidelity "what actually applies" signal.

## Anti-criteria honored (P0)

- **P0-520-1** — no new Azure scope; the existing ARM **Reader** role gates the
  NSG kind (DocumentedPermissions unchanged in shape; the same Reader row gates
  all three ARM kinds).
- **P0-520-2** — never reads flow logs / packet captures / traffic contents
  (structural: the structs have no field for them; `TestStructs_RulesOnly` + the
  integration no-PII allow-list enforce it; the client reads the list surface
  only).
- **P0-520-3** — never mutates a network resource (read-only: the client issues
  GET only; the client test asserts the method is GET).
- **P0-520-4** — no platform-side wire change; push only (invariant #3).
