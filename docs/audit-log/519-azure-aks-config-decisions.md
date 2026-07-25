# 519 — Azure AKS managed-cluster configuration evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shape, the cluster-config field set, the SCF anchors, the
scope-minimum, and THE load-bearing call — the structural over-collection
boundary that keeps admin kubeconfig / secrets / workload content out of the
record). It does NOT block merge; the maintainer iterates post-deployment from
the "Revisit once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no shipped-behavior bug surfaced during the
  build).
- **detection_tier_target:** none.

The only build-time corrections were the expected consequence of adding a third
evidence kind to the slice-486 connector: the `cmd` seam harness needed a
default no-op `aksScan` seam so the on-by-default AKS pull never reaches the live
ARM client in the existing seam tests, the register integration test's
`supported_kinds` count moved 2 → 3, and the `actorID`/`BuildAKSRecord` coverage
top-ups — all authoring fixes at the unit tier, none a product-behavior defect.

## Decisions made

### D1 — A SEPARATE sibling kind `azure.aks_cluster_config.v1`, mirroring the slice-486 storage kind

- **Options:** (a) a new sibling kind on the existing Azure connector; (b) fold
  AKS facts into `azure.storage_account_config.v1` (rejected immediately —
  unrelated resource); (c) a brand-new connector binary.
- **Chosen:** (a). A new `connectors/azure/internal/aks` collector + a new
  `azure.aks_cluster_config.v1` kind on the SAME `atlas-azure` binary, registered
  alongside the two slice-486 kinds. This is the slice-486 pattern verbatim
  (the spec says so) — the storage kind (also ARM-Reader-sourced) is the direct
  template: `aks.Inspect` mirrors `storage.Inspect`, `aks.Client` mirrors
  `storage.Client`, `idem.AKSClusterConfigKey` mirrors `StorageAccountKey`,
  `buildAKSRecord` mirrors `buildStorageRecord`.
- **Rationale:** AKS cluster hardening answers a cloud-configuration control
  question distinct from storage hardening, but it is the same connector's
  source (ARM, same Reader role, same tenant/subscription scope), so a sibling
  kind on the same binary is the minimum-surface choice. A new binary (c) would
  duplicate the auth + register + push plumbing for no benefit.

### D2 — Field set: cluster-level hardening posture, no workload, no secrets

- **Decision:** one record per AKS managed cluster carrying the compliance-
  relevant management-plane settings: `rbac_enabled`, `network_policy`,
  `private_cluster`, `authorized_ip_ranges`, `managed_identity`,
  `local_accounts_disabled`, `oidc_issuer_enabled`, `kubernetes_version`,
  `node_pool_count`, plus the identity/scope facts (`cluster_id`,
  `cluster_name`, `subscription_id`, `resource_group`, `location`).
- **The load-bearing call — structural over-collection boundary (P0-519-1 / -3):**
  the `aks.ClusterConfig` and `aks.RawCluster` structs carry CONFIGURATION flags
  ONLY. There is deliberately NO field for admin kubeconfig, cluster-admin
  credentials, service-principal secrets, workload/pod manifests, container
  images, or any secret payload — there is no place for such data to land even
  if a future ARM field exposed it. This is enforced two ways:
  1. `TestStructs_ConfigOnly_NoSecretFields` reflects over BOTH structs' field
     names and fails the build if any field name contains a banned token
     (`secret`, `credential`, `kubeconfig`, `password`, `token`, `key`,
     `manifest`, `container`, `image`, …).
  2. The ARM client (`internal/aks/client.go`) issues a `GET` to the
     managed-cluster **list** surface only and NEVER POSTs to
     `listClusterAdminCredential` / `listClusterUserCredential` (those return
     kubeconfig credentials and are a privilege escalation). The client test
     asserts the method is `GET` and that no request path contains
     `credential`.
- **Booleans always emitted:** `managed_identity`, `local_accounts_disabled`,
  `oidc_issuer_enabled`, `node_pool_count` are always present in the payload
  (false / 0 is signal, not absence); the string optionals (`resource_group`,
  `location`, `kubernetes_version`, `network_policy`) are omitted when empty,
  mirroring the storage builder.

### D3 — SCF anchors: CFG-02 + NET-04 (`x-default-scf-anchors`)

- **Decision:** `x-default-scf-anchors: ["CFG-02", "NET-04"]`; the `run`
  subcommand's `--aks-control` default is `scf:CFG-02`.
- **Rationale:** AKS cluster hardening spans configuration-management (RBAC,
  managed identity, local-accounts-disabled, OIDC, K8s version, network-policy
  plugin = baseline config) and network protection (private cluster /
  authorized-IP-range API-server isolation). `CFG-02` (Secure Baseline
  Configurations) is the same anchor the analogous `k8s.workload_security_context.v1`
  kind uses; `NET-04` (network protection) is the anchor the slice-486 storage
  kind already uses for its public-access flag. Both anchors are already present
  in the seed / in use by sibling kinds, so the bijection drift-guard is happy.
  These are default mapping HINTS flagged for maintainer recheck (OQ #9), not a
  binding mapping — the per-run `--aks-control` flag overrides per deployment.

### D4 — Scope minimum: `cloud_account=azure:<subscription_id>` + `environment`

- **Decision:** every AKS record carries exactly two scope dimensions —
  `cloud_account=azure:<subscription_id>` and `environment=<--environment>` —
  identical to the storage kind (AKS managed clusters are subscription-scoped
  ARM resources, same as storage accounts). `run` fails loudly when
  `--environment` is unset rather than pushing an un-scoped record, and requires
  `--subscription-id` unless `--skip-aks` is set.
- **Rationale:** the connector-pattern scope minimum; the subscription is the
  account-equivalent for ARM resources (slice 486 D for storage).

### D5 — Result semantics: descriptive deterministic verdict, evaluator owns final pass/fail

- **Decision:** `PASS` when RBAC enabled AND a network-policy plugin configured
  AND the API server is isolated (private cluster OR authorized-IP ranges);
  `FAIL` when a core control is off; `INCONCLUSIVE` when a per-cluster read
  errored. The richer facts (`managed_identity`, `oidc_issuer_enabled`, …) ride
  in the payload for the platform evaluator to interpret; the connector verdict
  is a descriptive default, not the binding decision.
- **Rationale:** mirrors the storage-kind verdict shape. The private-cluster-OR-
  authorized-IP-ranges disjunction reflects that either mechanism is a valid
  API-server isolation control (a public cluster locked to authorized IP ranges
  is an accepted hardening posture), so demanding both would over-fail.

### D6 — Coverage floor + no migration / no wire change

- New package `connectors/azure/internal/aks` enrolled in
  `cmd/scripts/coverage-thresholds.json` at a **90%** floor (measured 94.0%).
- NO platform API route, NO migration — evidence flows through the existing
  slice-013 ledger via the single `IngestEvidence` push RPC (invariant #3); the
  platform-side wire is unchanged. `profiles_supported` stays `[pull]`; the
  interval stays honestly named.

## Revisit once in use (maintainer iteration list)

- **SCF-anchor recheck (OQ #9).** Confirm CFG-02 + NET-04 are the right default
  anchors for AKS cluster hardening, or split RBAC-related facts onto an IAC
  anchor if the mapping proves too coarse in practice.
- **Cursor pagination / multi-subscription enumeration.** v0 reads the first
  bounded ARM page for one subscription (threat-model D, shared with slice 486
  R3). A large cluster fleet needs the shared cursor-pagination follow-on.
- **Richer per-node-pool detail.** v0 summarizes node pools as a count. A
  follow-on could emit per-node-pool config (VM size, autoscaling, OS disk
  encryption) if a control demands that grain — but only if it stays
  config-only.

## Anti-criteria honored (P0)

- **P0-519-1** — never calls `listClusterAdminCredential` / any admin-kubeconfig
  API (client issues GET to the list surface only; client test asserts no
  `credential` path).
- **P0-519-2** — no new Azure scope; the existing ARM **Reader** role gates the
  AKS kind (DocumentedPermissions unchanged in shape; the same Reader row gates
  both ARM kinds).
- **P0-519-3** — never reads workload manifests / secrets / container contents
  (structural: the struct has no field for them; `TestStructs_ConfigOnly` +
  the integration no-PII allow-list enforce it).
- **P0-519-4** — no platform-side wire change; push only (invariant #3).
