# Azure connector

The third major-cloud connector (slice 486), bringing Azure to parity with the
AWS (slice 004) and the planned GCP (slice 442) connectors. It follows the
locked connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits three evidence kinds:

| Kind                              | Profile | Source                                                                                                    |
| --------------------------------- | ------- | --------------------------------------------------------------------------------------------------------- |
| `azure.entra_role_assignment.v1`  | pull    | Microsoft Graph `roleManagement/directory/roleAssignments` (`Directory.Read.All`, `Application.Read.All`) |
| `azure.storage_account_config.v1` | pull    | Azure Resource Manager `Microsoft.Storage/storageAccounts` (ARM **Reader** role)                          |
| `azure.aks_cluster_config.v1`     | pull    | Azure Resource Manager `Microsoft.ContainerService/managedClusters` (ARM **Reader** role) — slice 519     |

The connector reads **configuration + role-assignment metadata only**. It never
reads blob/object contents, Key-Vault secret values, storage access keys, SAS
tokens, user mailbox/profile PII beyond the display name needed to name an
assignment, or — for AKS — admin kubeconfig / cluster-admin credentials,
service-principal secrets, or workload/pod manifests (the AKS read calls only
the managed-cluster **list** surface, never `listClusterAdminCredential`). The
Azure credential stays source-side and never enters an evidence record or a
platform push (canvas invariant #3).

## Profile + interval — honest, not "continuous monitoring"

The connector runs on the **pull** profile: each invocation is one bounded
read-and-push pass. It is **operator-scheduled** (cron / scheduler) — the
recommended cadence is **every 24h**. This is deliberately **not** "continuous
monitoring": the interval is named honestly. An event-driven profile (via Azure
Event Grid / Activity-Log diagnostic settings) is a documented follow-on, not
part of v0.

## Auth — least-privilege read-only Entra app + ARM Reader

The connector authenticates to Azure with a read-only **Entra app-registration**
(client-credentials) or a **managed identity**. The platform-side push reuses the
existing connector credential boundary (OAuth client_credentials, slice 191) — no
new auth scheme.

Create a dedicated read-only app-registration / managed identity and grant it
**exactly** the permissions below. Every call the connector makes is a read.

| Surface                | Permission                           | Access | Gates                                                                                                                                       |
| ---------------------- | ------------------------------------ | ------ | ------------------------------------------------------------------------------------------------------------------------------------------- |
| Microsoft Graph        | `Directory.Read.All` (application)   | Read   | `azure.entra_role_assignment.v1` (directory-role / RBAC assignments)                                                                        |
| Microsoft Graph        | `Application.Read.All` (application) | Read   | `azure.entra_role_assignment.v1` (service-principal / app inventory)                                                                        |
| Azure Resource Manager | **Reader** (built-in role)           | Read   | `azure.storage_account_config.v1` (storage account configuration) **and** `azure.aks_cluster_config.v1` (AKS managed-cluster configuration) |

The **same** ARM **Reader** role gates both ARM-sourced kinds — the AKS kind
(slice 519) adds **no** new Azure scope (P0-519-2). Reader cannot call
`listClusterAdminCredential` (the privilege-escalating admin-kubeconfig API), so
admin credentials are unreachable by construction (P0-519-1).

Run `atlas-azure permissions` to print this list.

**Banned permissions.** Do **not** grant any `*.ReadWrite.*` / `*.Manage` Graph
permission, and do **not** grant Owner / Contributor / User Access Administrator
on the subscription. Do **not** use the broad **Global Administrator** /
**Global Reader** directory roles where the narrow `Directory.Read.All` +
`Application.Read.All` application permissions suffice — least privilege prefers
the narrower set. The connector has no write code path; the only Graph/ARM
operations it issues are reads (`GET .../roleAssignments`,
`GET .../storageAccounts`, `GET .../managedClusters`). It never issues the
`listClusterAdminCredential` / `listClusterUserCredential` POSTs that return
kubeconfig credentials.

The Graph-permission vs ARM-role split is the scope-minimum subtlety: identity
evidence needs the two Graph application permissions; storage **and** AKS
evidence both need only the single ARM Reader role. Grant only the set for the
kinds you run (use `--skip-entra` / `--skip-storage` / `--skip-aks` to run a
subset of surfaces).

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-azure register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read Entra ID + Azure Storage + AKS, push evidence records.
# Azure credentials are read from the environment (never the CLI, so the
# secret stays out of shell history):
export AZURE_TENANT_ID=<tenant-guid>
export AZURE_CLIENT_ID=<app-registration-client-id>
export AZURE_CLIENT_SECRET=<app-registration-secret>

atlas-azure run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --subscription-id <subscription-guid> \
  --environment prod

# Print the least-privilege Azure permissions.
atlas-azure permissions
```

| Flag                | Subcommand | Required | Default                       | Notes                                                                                      |
| ------------------- | ---------- | -------- | ----------------------------- | ------------------------------------------------------------------------------------------ |
| `--endpoint`        | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                                                     |
| `--token`           | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                                                |
| `--insecure`        | both       | no       | `false`                       | disables TLS; loopback endpoints only                                                      |
| `--tenant-id`       | `run`      | no\*     | env `AZURE_TENANT_ID`         | Entra tenant id (\*required, via flag or env)                                              |
| `--client-id`       | `run`      | no\*     | env `AZURE_CLIENT_ID`         | app-registration client id (client-credentials mode)                                       |
| `--subscription-id` | `run`      | yes†     | —                             | subscription for the storage + AKS kinds († unless both `--skip-storage` and `--skip-aks`) |
| `--environment`     | `run`      | yes      | —                             | environment scope tag; records are never emitted un-scoped                                 |
| `--auth-mode`       | `run`      | no       | `client-credentials`          | `client-credentials` or `managed-identity`                                                 |
| `--entra-control`   | `run`      | no       | `scf:IAC-21`                  | control id attached to entra records                                                       |
| `--storage-control` | `run`      | no       | `scf:CRY-04`                  | control id attached to storage records                                                     |
| `--aks-control`     | `run`      | no       | `scf:CFG-02`                  | control id attached to AKS records                                                         |
| `--skip-entra`      | `run`      | no       | `false`                       | skip the `azure.entra_role_assignment.v1` pull                                             |
| `--skip-storage`    | `run`      | no       | `false`                       | skip the `azure.storage_account_config.v1` pull                                            |
| `--skip-aks`        | `run`      | no       | `false`                       | skip the `azure.aks_cluster_config.v1` pull                                                |

The client secret is **only** read from `AZURE_CLIENT_SECRET` — never a CLI flag
— so it never lands in shell history. It is never logged and never enters an
evidence record (the resolved credential redacts its secret on every format
path).

`register` announces `name=azure-connector`,
`supported_kinds=[azure.entra_role_assignment.v1, azure.storage_account_config.v1, azure.aks_cluster_config.v1]`,
and `profiles_supported=[pull]` to `ConnectorRegistryService.Register`. The
`profiles_supported` value describes how the connector retrieves data **from
Azure** (a scheduled pull); the platform-side wire is always push (invariant #3).

## Scope minimums

Every emitted record sets the minimum scope dimensions the connector-pattern
convention requires:

| Scope key       | Entra value              | Storage / AKS value       | Source                         |
| --------------- | ------------------------ | ------------------------- | ------------------------------ |
| `cloud_account` | `azure:<tenant_id>`      | `azure:<subscription_id>` | the resolved credential / flag |
| `environment`   | the `--environment` flag | the `--environment` flag  | the `--environment` flag       |

For Entra the account-equivalent is the **tenant**; for Storage and AKS it is the
**subscription** (ARM resources are subscription-scoped). `run` fails loudly when
`--environment` is unset rather than pushing an un-scoped record.

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:azure:entra@<version>` for
identity records, `connector:azure:storage@<version>` for storage records, and
`connector:azure:aks@<version>` for AKS records, where `<version>` is the build's
module version (or `dev` under `go run`).

## Idempotency

Each record's `idempotency_key` is
`sha256("<kind>|<resource_id>|<hour_truncated_observed_at>")` (see
`internal/idem`). `observed_at` is truncated to the UTC hour, so two runs within
the same hour for the same assignment / storage account collapse to one ledger
row; a run that crosses an hour boundary writes a fresh record.
`source_attribution.session_id` is left empty on purpose: a per-call UUID would
change the record's canonical hash between dedup retries.

## Result semantics

- **`azure.entra_role_assignment.v1` → `INCONCLUSIVE` (descriptive).** The
  connector does not decide pass/fail for an assignment — the platform evaluator
  interprets which assignment pattern passes/fails per (control, scope). The
  connector-side `is_privileged` flag is a heuristic hint, not a verdict.
- **`azure.storage_account_config.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The
  connector verdicts the deterministic hardening posture: `PASS` when encryption
  at rest is on **and** secure-transfer (HTTPS-only) is required **and**
  anonymous public blob access is off; `FAIL` when any of the three is off;
  `INCONCLUSIVE` when a per-account read errored.
- **`azure.aks_cluster_config.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The
  connector verdicts the deterministic cluster hardening posture: `PASS` when
  Kubernetes RBAC is enabled **and** a network-policy plugin is configured
  **and** the API server is isolated (private cluster **or** authorized-IP
  ranges); `FAIL` when a core control is off; `INCONCLUSIVE` when a per-cluster
  read errored. The payload also carries the descriptive `managed_identity`,
  `local_accounts_disabled`, `oidc_issuer_enabled`, `kubernetes_version`, and
  `node_pool_count` facts the platform evaluator can read; the final pass/fail
  per (control, cluster) is the evaluator's.

## Not in v0 (follow-ons)

The connector ships three evidence surfaces. It does **not** ship:

- AKS workload / pod-level evidence — the AKS kind reads **cluster-level**
  management-plane configuration only; pod manifests, secrets, and container
  images are osquery / a Kubernetes-native connector's job
- Network-Security-Group / firewall evidence
- Key-Vault access-policy evidence
- Azure-Policy / Activity-Log evidence
- an event-driven (subscribe) profile via Azure Event Grid
- cursor pagination / multi-subscription auto-enumeration (v0 reads a bounded
  first page for one subscription)

These are filed as follow-on slices (see `docs/issues/486-azure-connector.md`,
`docs/issues/519-azure-aks-workload-config-evidence.md`, and the sibling
follow-ons NSG/Key-Vault/Azure-Policy 520 / 521).

## Tests

```sh
go test ./connectors/azure/...
```

Unit tests fake the Microsoft Graph + Azure Resource Manager surfaces (no live
Azure, no real credentials) and pin the normalization, the storage **and** AKS
hardening verdict matrices, the credential redaction, the read-only permission
contract, and the `idem` hour-window behavior. A structural over-collection test
(`internal/aks`) reflects over the `ClusterConfig` / `RawCluster` struct field
names and fails the build if any field even hints at a secret / credential /
kubeconfig / workload-content surface, and the AKS client test asserts the read
issues only a `GET` to the managed-cluster list endpoint (never a `credential`
endpoint). The integration test (in-package, bufconn platform — no Postgres)
exercises the full collect → build → SDK `Push` → push-receipt round-trip for all
three kinds and asserts two same-hour pushes collapse to one `record_id`, that
emitted payloads carry config/assignment metadata only (no PII / secrets / admin
kubeconfig — a per-kind allow-list), and that the credential never surfaces in a
formatted log.
