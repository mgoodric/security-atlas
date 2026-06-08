# 521 — Azure Key-Vault access-policy / RBAC evidence: JUDGMENT decisions log

Slice type: JUDGMENT. This file records the subjective build-time calls (the
evidence-kind shape, the access / config field set, the SCF anchors, the
scope-minimum, the descriptive verdict, and THE load-bearing call — the
structural over-collection boundary that keeps every secret / key / certificate
VALUE out of the record). It does NOT block merge; the maintainer iterates
post-deployment from the "Revisit once in use" list.

## Detection-tier classification (slice 353 / Q-13)

- **detection_tier_actual:** none (no product-behavior bug surfaced during the
  slice — the build-time corrections were all expected fifth-kind authoring
  fixes, caught at the unit tier as I wrote the threading).
- **detection_tier_target:** none.

The build-time corrections were the expected consequence of adding a fifth
evidence kind to the slice-486/519/520 connector and were all authoring fixes,
not product-behavior defects:

- the `cmd` seam harness needed a default no-op `keyvaultScan` seam so the
  on-by-default Key-Vault pull never reaches the live ARM client in the existing
  seam tests (mirrors the slice-519 AKS / slice-520 NSG no-op default);
- the register integration test's `supported_kinds` count moved 4 → 5 and
  `TestRun_PushesAllKinds` moved `pushed == 4` → `5`;
- `gofmt` re-aligned the widened `runFlags` / `seamOverrides` struct blocks
  (caught by `golangci-lint`, fixed with `gofmt -w`).

The cross-kind allow-list interaction that bit slice 520 (the shared
`"traffic"` ban colliding with `https_traffic_only`) did NOT recur here: the
shared `bannedSubstrings` list already contains `secret` / `credential` /
`key_value` / `access_key`, and none of the Key-Vault payload keys
(`purge_protection`, `soft_delete_enabled`, `public_network_access`,
`network_acl_default`, `rbac_authorization`, `access_entries`, `vault_*`,
`principal_*`, `permissions`, `role_name`) contains any of them as a substring,
so the existing shared list applies cleanly with no Key-Vault-scoped sub-list.

## Decisions made

### D1 — A SEPARATE sibling kind `azure.keyvault_access_config.v1`, mirroring the slice-519/520 ARM kinds

- **Options:** (a) a new sibling kind on the existing Azure connector; (b) fold
  Key-Vault facts into an existing kind (rejected immediately — unrelated
  resource); (c) a brand-new connector binary.
- **Chosen:** (a). A new `connectors/azure/internal/keyvault` collector + a new
  `azure.keyvault_access_config.v1` kind on the SAME `atlas-azure` binary,
  registered alongside the four slice-486/519/520 kinds. This is the
  slice-486/519/520 pattern verbatim: `keyvault.Inspect` mirrors `nsg.Inspect`,
  `keyvault.Client` mirrors `nsg.Client`, `idem.KeyVaultAccessKey` mirrors
  `NSGRulesKey`, `buildKeyVaultRecord` mirrors `buildNSGRecord`.
- **Rationale:** Key-Vault secrets-management / least-privilege answers a control
  question distinct from network or cluster hardening, but it is the same
  connector's source (ARM, same Reader role, same subscription scope), so a
  sibling kind on the same binary is the minimum-surface choice.

### D2 — THE load-bearing call: management-plane CONFIGURATION + access METADATA only — NEVER a secret/key/certificate VALUE

- **Decision:** the collector reads the ARM **management plane** only —
  `GET .../Microsoft.KeyVault/vaults` (vault config + the legacy `accessPolicies`
  array). It NEVER calls the Key-Vault **data plane** (`vault.azure.net`
  secret/key/certificate get), and the connector requires NO Key-Vault
  data-plane role.
- **Structural enforcement (the slice-520 gold standard):** the
  `keyvault.VaultConfig` / `keyvault.AccessEntry` / `keyvault.RawVault` structs
  have NO field for any secret/key/certificate value. `TestStructs_MetadataOnly_NoSecretValueFields`
  reflects over every field name and fails the build if any hints at secret
  material (`value`, `secret`, `password`, `privatekey`, `keymaterial`, `pem`,
  `pfx`, `certificate_contents`, `passphrase`, `token`, `connection_string`,
  …). The builder cannot emit what the struct cannot hold, and the client has no
  data-plane code path — so a secret value is unreachable by construction. The
  client test additionally asserts the read is a `GET` against the
  `Microsoft.KeyVault/vaults` list endpoint and never a `vault.azure.net` path.
- **Permission verbs are metadata, not material:** for a legacy-access-policy
  vault each access entry carries the granted permission VERBS namespaced as
  `<area>:<verb>` (e.g. `secrets:get`, `keys:list`). These are permission NAMES
  — they describe WHAT a principal may do, never the secret it could read. This
  is exactly the least-privilege signal the control needs.
- **This satisfies P0-521-1 / P0-521-2** and is the same boundary slice 486
  established for Storage (config, not blob contents) and slice 519 for AKS
  (config, not kubeconfig).

### D3 — Field set: per-vault config posture + access-policy / RBAC-mode metadata

- **Decision:** one record per vault carrying the access model
  (`rbac_authorization` bool), the durability posture (`purge_protection`,
  `soft_delete_enabled`), the network posture (`public_network_access`,
  `network_acl_default`), the identity/scope facts (`vault_id`, `vault_name`,
  `subscription_id`, `resource_group`, `location`), and the `access_entries`
  list (principal id/type + permission verbs OR role name).
- **`access_entries` shape:** a discriminated list — `principal_type` is
  `access_policy` (carries `permissions` verb list) or `rbac_role_assignment`
  (carries `role_name`). v0 populates the legacy access-policy entries in full;
  for an RBAC-mode vault the `rbac_authorization=true` flag is reported but the
  per-vault `Microsoft.Authorization/roleAssignments` are NOT enumerated (the v0
  read is the bounded vault-list page only). See D5 + the spillover.

### D4 — SCF anchors `IAC-21` (Privileged Account Management) + `CRY-09` (Cryptographic Key Management)

- **Options considered (all present in `migrations/fixtures/scf-sample.json`):**
  IAC-21, IAC-06 (MFA), IAC-15 (Account Review), CRY-09, CRY-04 (Encryption At
  Rest), CRY-01 (Use of Cryptographic Controls).
- **Chosen:** `["IAC-21", "CRY-09"]`. The Key-Vault access posture is two
  controls in one record: (1) WHO has access and at what privilege —
  least-privilege / privileged-account-management (`IAC-21`), the access-policy /
  RBAC principal+permission list; and (2) the SECRET-STORE durability posture —
  cryptographic-key-management (`CRY-09`), the purge-protection / soft-delete /
  network-isolation config. `CRY-04` (encryption at rest) was rejected as the
  primary anchor because a vault's own at-rest encryption is platform-managed and
  not the signal here; `CRY-09` (key management lifecycle, of which a hardened
  key store is the foundation) is the precise fit.
- **Default `--keyvault-control` is `scf:CRY-09`** (the secret-store durability
  posture is the headline control the connector verdicts on); `IAC-21` is the
  secondary anchor for the access-principal least-privilege read.
- **Caveat:** `x-default-scf-anchors` are mapping HINTS flagged for maintainer
  recheck (OQ #9), consistent with every prior connector kind.

### D5 — Descriptive verdict: PASS/FAIL on the deterministic secrets-management posture

- **Decision:** `FAIL` when soft-delete is off, OR purge protection is off, OR
  the vault is reachable from the public network with no default-Deny network
  ACL; `INCONCLUSIVE` when a per-vault read errored; `PASS` otherwise.
- **`publicReachable` nuance:** an empty `public_network_access` is treated as
  not-explicitly-open (the ARM default varies by API version; the connector does
  not infer a FAIL from an absent field). A vault with public access `Enabled`
  but a default-Deny network ACL is firewalled → PASS. This mirrors the
  storage/AKS/NSG descriptive-default convention: the connector emits a
  defensible default, the platform evaluator owns the final pass/fail per
  (control, vault).

## Spillover

- **slice 615 — Azure Key-Vault RBAC role-assignment enumeration.** For an
  RBAC-authorization vault, enumerate the per-vault
  `Microsoft.Authorization/roleAssignments` (principal + role name) so the
  `access_entries` list is populated for RBAC-mode vaults the same way it is for
  legacy-access-policy vaults. v0 reports only the `rbac_authorization=true`
  flag + vault config for RBAC vaults; the role-assignment read is a second ARM
  call (still ARM-Reader, still management-plane, no new scope) deliberately
  deferred to keep this slice's read the bounded single vault-list page. Cites
  parent 521; status `ready` (no new dependency).

## Revisit once in use (maintainer iteration list)

- Whether `CRY-09` vs `CRY-04` is the better default anchor once real SOC 2 /
  ISO key-management evidence requests are mapped.
- Whether the `publicReachable` heuristic (empty = not-open) matches operator
  expectation, or should treat an absent `public_network_access` as a FAIL.
- Cursor pagination / multi-subscription auto-enumeration (shared follow-on with
  the whole connector family — v0 reads a bounded first page for one
  subscription).
