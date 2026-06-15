# Azure connector

The third major-cloud connector (slice 486), bringing Azure to parity with the
AWS (slice 004) and the planned GCP (slice 442) connectors. It follows the
locked connector pattern verbatim: register-per-run, a stable `actor_id`, an
hour-truncated `observed_at`, scope minimums, and vendor-native read-only auth.
It emits six evidence kinds:

| Kind                              | Profile | Source                                                                                                               |
| --------------------------------- | ------- | -------------------------------------------------------------------------------------------------------------------- |
| `azure.entra_role_assignment.v1`  | pull    | Microsoft Graph `roleManagement/directory/roleAssignments` (`Directory.Read.All`, `Application.Read.All`)            |
| `azure.storage_account_config.v1` | pull    | Azure Resource Manager `Microsoft.Storage/storageAccounts` (ARM **Reader** role)                                     |
| `azure.aks_cluster_config.v1`     | pull    | Azure Resource Manager `Microsoft.ContainerService/managedClusters` (ARM **Reader** role) — slice 519                |
| `azure.nsg_rules.v1`              | pull    | Azure Resource Manager `Microsoft.Network/networkSecurityGroups` (ARM **Reader** role) — slice 520                   |
| `azure.keyvault_access_config.v1` | pull    | Azure Resource Manager `Microsoft.KeyVault/vaults` (ARM **Reader** role) — slice 521                                 |
| `azure.firewall_rules.v1`         | pull    | Azure Resource Manager `Microsoft.Network/firewallPolicies` rule-collection groups (ARM **Reader** role) — slice 614 |

The connector reads **configuration + role-assignment metadata only**. It never
reads blob/object contents, Key-Vault secret/key/certificate values, storage
access keys, SAS tokens, user mailbox/profile PII beyond the display name needed
to name an assignment, or — for AKS — admin kubeconfig / cluster-admin
credentials, service-principal secrets, or workload/pod manifests (the AKS read
calls only the managed-cluster **list** surface, never
`listClusterAdminCredential`), or — for NSGs — flow logs, packet captures, or
traffic contents (the NSG read calls only the NSG **list** surface and never
mutates a network resource), or — for Key Vaults — any secret, key, or
certificate **value** (the Key-Vault read calls only the ARM management-plane
vault **list** surface — vault config + access policies — and **never** the
Key-Vault data plane `vault.azure.net`; it requires **no** Key-Vault data-plane
role), or — for Azure Firewall — flow logs, packet captures, traffic contents,
NAT-rule secrets, threat-intel feeds, or route tables (the firewall read calls
only the `firewallPolicies` + `ruleCollectionGroups` **list** surfaces and never
mutates a network resource). The Azure credential stays source-side and never
enters an evidence record or a platform push (canvas invariant #3).

## Profiles + interval — honest, not "continuous monitoring"

The connector advertises `profiles_supported=[pull, subscribe]`. Both describe how
the connector retrieves data **from Azure**; the platform-side wire is always push
(invariant #3).

- **pull** (`run` subcommand): each invocation is one bounded read-and-push pass.
  **Operator-scheduled** (cron / scheduler) — recommended cadence **every 24h**. It
  is the **reconciliation backstop**.
- **subscribe** (`eventgrid` subcommand, slice 522): an event-driven receiver. Azure
  Event Grid delivers a change event for an in-scope resource; the connector treats
  the event as a **trigger**, re-reads the changed resource via the **same read-only
  path** the pull profile uses, and pushes the matching record. "Event-driven" means
  **Event-Grid delivery latency** (typically seconds to a minute) plus a short
  **coalescing window** — it is **not** instantaneous and is deliberately **not**
  "continuous monitoring".

Both are named honestly. The event is never the data: the record's contents come
entirely from the re-read of real Azure state, so a forged event can at most cause a
redundant read of real config — never a fabricated record. See the `eventgrid`
subcommand below and `docs/audit-log/522-azure-eventgrid-decisions.md`.

### `eventgrid` subscribe receiver — security + operation

- **Delivery auth (per event):** set `AZURE_EVENTGRID_DELIVERY_KEY` to the delivery
  key you configured on the Event Grid subscription. Every delivery's key is verified
  **constant-time BEFORE** any re-read; a missing/forged key is rejected **401** and
  never triggers a re-read. The key location is `--credential-in header|query`
  (default `header`, `--credential-name Authorization`).
- **SubscriptionValidation handshake:** when Event Grid validates the webhook it
  POSTs a `Microsoft.EventGrid.SubscriptionValidationEvent` carrying a
  `validationCode`; the receiver responds `200 {"validationResponse":"<code>"}` and
  builds **no** record.
- **DoS bounding:** a bounded event queue + a coalescing window collapse an event
  storm into one re-read per resource; the body is size-bounded (413). The pull
  profile is the reconciliation backstop for any dropped event.
- **No new permission (receiver):** the re-read uses the **same** read-only Graph +
  ARM Reader set as the pull profile (plus the operator's Event-Grid subscription
  read, configured in Azure). The steady-state receiver has **no write code path**
  and never holds a write scope; it emits no new evidence kind. Creating the
  Event-Grid plumbing the receiver listens behind is a **separate, opt-in,
  privileged** action — see the `provision` subcommand below (slice 658). The
  receiver and the provisioner are deliberately distinct processes with distinct
  credentials.
- **Bind / TLS:** defaults to loopback (`127.0.0.1:8485`). Event Grid requires an
  HTTPS endpoint with a valid certificate — terminate TLS at a reverse proxy in
  front of this process.

### Cross-profile dedup

A subscribe-emitted record and a pull-emitted record for the **same resource in the
same UTC hour** derive the **identical** idempotency key
(`sha256("<kind>|<resource_id>|<hour>")`, the slice-486 key, unchanged via the same
record builders) and collapse to **one** ledger row. An event at 14:45 and a pull at
15:05 fall in different UTC hours → two rows, which is correct (two observations an
hour apart).

## Auth — least-privilege read-only Entra app + ARM Reader

The connector authenticates to Azure with a read-only **Entra app-registration**
(client-credentials) or a **managed identity**. The platform-side push reuses the
existing connector credential boundary (OAuth client_credentials, slice 191) — no
new auth scheme.

Create a dedicated read-only app-registration / managed identity and grant it
**exactly** the permissions below. Every call the connector makes is a read.

| Surface                | Permission                           | Access | Gates                                                                                                                                                                                                                                                                                                                                         |
| ---------------------- | ------------------------------------ | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Microsoft Graph        | `Directory.Read.All` (application)   | Read   | `azure.entra_role_assignment.v1` (directory-role / RBAC assignments)                                                                                                                                                                                                                                                                          |
| Microsoft Graph        | `Application.Read.All` (application) | Read   | `azure.entra_role_assignment.v1` (service-principal / app inventory)                                                                                                                                                                                                                                                                          |
| Azure Resource Manager | **Reader** (built-in role)           | Read   | `azure.storage_account_config.v1` (storage account configuration), `azure.aks_cluster_config.v1` (AKS managed-cluster configuration), `azure.nsg_rules.v1` (NSG firewall-rule posture), `azure.keyvault_access_config.v1` (Key-Vault access-policy / RBAC posture) **and** `azure.firewall_rules.v1` (Azure Firewall rule-collection posture) |

The **same** ARM **Reader** role gates every ARM-sourced kind — the AKS kind
(slice 519), the NSG kind (slice 520), the Key-Vault kind (slice 521), and the
Azure-Firewall kind (slice 614) add **no** new Azure scope (P0-519-2 /
P0-520-1 / P0-521-3 / P0-614-1). Reader cannot call
`listClusterAdminCredential` (the privilege-escalating admin-kubeconfig API), so
admin credentials are unreachable by construction (P0-519-1); Reader cannot
mutate a network resource, so NSG and Azure-Firewall rule changes are
unreachable by construction (P0-520-3 / P0-614-3).

**Key Vault — the over-privilege trap (P0-521-1, hard).** The Key-Vault kind
reads the ARM **management plane** only: vault configuration (RBAC-vs-access-
policy mode, purge protection, soft-delete, public-network-access, network ACLs)
and the **access-policy / role-assignment metadata** (which principal has which
permission verbs / role on the vault). For a legacy access-policy vault those
principals come from the vault's `accessPolicies`; for an RBAC vault they come
from a second read-only ARM read of `Microsoft.Authorization/roleAssignments`
scoped to the vault resource id (principal object id + role definition name —
slice 615), with role names resolved via a bounded read-only `roleDefinitions`
lookup. Reader suffices for both reads. It **never** reads a secret, key, or
certificate **value** — those live on the Key-Vault **data plane**
(`vault.azure.net`), which the connector never calls. Do **not** grant this
connector any Key-Vault **data-plane** role (`Key Vault Secrets User`,
`Key Vault Crypto User`, `Key Vault Certificates Officer`, `Key Vault
Administrator`, the legacy `get`/`list` access policies on secrets, etc.) to
"let it read the vault" — that is the over-privilege trap and is **not**
required. ARM **Reader** alone suffices, and a secret value is unreachable by
construction (the connector has no data-plane code path and the collector struct
has no field to hold secret material).

Run `atlas-azure permissions` to print this list.

**Banned permissions.** Do **not** grant any `*.ReadWrite.*` / `*.Manage` Graph
permission, and do **not** grant Owner / Contributor / User Access Administrator
on the subscription. Do **not** use the broad **Global Administrator** /
**Global Reader** directory roles where the narrow `Directory.Read.All` +
`Application.Read.All` application permissions suffice — least privilege prefers
the narrower set. The connector's steady-state code paths (`run` pull +
`eventgrid` subscribe receiver) have **no write code path**; the only Graph/ARM
operations they issue are reads (`GET .../roleAssignments`,
`GET .../storageAccounts`, `GET .../managedClusters`,
`GET .../networkSecurityGroups`, `GET .../Microsoft.KeyVault/vaults`,
`GET .../firewallPolicies`, `GET .../firewallPolicies/<policy>/ruleCollectionGroups`).
It never issues the `listClusterAdminCredential` / `listClusterUserCredential`
POSTs that return kubeconfig credentials, it never issues any NSG or
Azure-Firewall write / mutate call, and it never issues any Key-Vault
**data-plane** (`vault.azure.net`) secret/key/certificate `GET`.

The Graph-permission vs ARM-role split is the scope-minimum subtlety: identity
evidence needs the two Graph application permissions; storage, AKS, NSG,
Key-Vault **and** Azure-Firewall evidence all need only the single ARM Reader
role. Grant only the set for the kinds you run (use `--skip-entra` /
`--skip-storage` / `--skip-aks` / `--skip-nsg` / `--skip-keyvault` /
`--skip-firewall` to run a subset of surfaces).

> **The one write surface is the opt-in `provision` subcommand (slice 658).** The
> banned-permissions rule above governs the steady-state `run` / `eventgrid`
> credential. The `provision` / `deprovision` subcommands are a **separate,
> one-shot, privileged** action you run with your **own elevated, short-lived**
> credential (distinct env vars — see below) carrying ARM
> `Microsoft.EventGrid/*` write + `Microsoft.Insights/diagnosticSettings/write`.
> Those actions are **operator-supplied for that single run**, never held by the
> long-lived receiver. Run `atlas-azure provision --print-rbac` for the exact
> action list.

## Event-Grid provisioning (opt-in, privileged — slice 658)

The `eventgrid` receiver **receives** events but deliberately does **not** create
the Azure Event-Grid system topic + event subscription (or the Activity-Log
diagnostic setting) that route events to its webhook. Auto-creating those requires
a **write** scope the steady-state receiver must never hold (P0-658-1). So
provisioning is a **separate, opt-in, one-shot** operator action:

- **Distinct elevated credential.** `provision` / `deprovision` read their **own**
  env vars — `AZURE_PROVISION_TENANT_ID` / `AZURE_PROVISION_CLIENT_ID` /
  `AZURE_PROVISION_CLIENT_SECRET` — **not** the receiver's read-only `AZURE_*`.
  Supply a short-lived credential with the write role for the single run, then
  revoke it.
- **ARM management API only.** Provisioning talks to **Azure's** ARM API; it does
  **not** touch the security-atlas platform and does **not** widen the platform
  push wire (invariant #3 / P0-658-2).
- **Idempotent.** ARM PUT is upsert, so re-running an already-provisioned plan
  succeeds. `deprovision` (or `provision --teardown`) DELETEs what `provision`
  created; deleting an absent resource is a no-op.
- **Exact RBAC actions** the elevated credential needs:

  | Action                                                       | Why                                                              |
  | ------------------------------------------------------------ | ---------------------------------------------------------------- |
  | `Microsoft.EventGrid/systemTopics/write`                     | create the system topic                                          |
  | `Microsoft.EventGrid/systemTopics/delete`                    | tear down the system topic                                       |
  | `Microsoft.EventGrid/systemTopics/eventSubscriptions/write`  | create the event subscription to the receiver webhook            |
  | `Microsoft.EventGrid/systemTopics/eventSubscriptions/delete` | tear down the event subscription                                 |
  | `Microsoft.Insights/diagnosticSettings/write`                | create the Activity-Log diagnostic setting (`--with-diagnostic`) |
  | `Microsoft.Insights/diagnosticSettings/delete`               | tear down the Activity-Log diagnostic setting                    |

## Subcommands

```sh
# Announce this connector instance to the platform.
atlas-azure register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read Entra ID + Azure Storage + AKS + NSG + Key Vault + Azure Firewall, push evidence records.
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

# Run the event-driven (subscribe) Event Grid receiver. The delivery key is read
# from the environment (never the CLI). Terminate TLS at a reverse proxy in front.
export AZURE_EVENTGRID_DELIVERY_KEY=<delivery-key-configured-on-the-subscription>

atlas-azure eventgrid \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --subscription-id <subscription-guid> \
  --environment prod \
  --listen 127.0.0.1:8485 \
  --path /webhooks/azure/eventgrid

# Print the least-privilege Azure permissions.
atlas-azure permissions

# [PRIVILEGED, opt-in, one-shot — slice 658] Provision the Event-Grid plumbing the
# receiver listens behind. Run with your OWN ELEVATED, short-lived credential
# (DISTINCT env vars from the receiver's read-only AZURE_*). Then revoke it.
atlas-azure provision --print-rbac   # show the exact write actions required

export AZURE_PROVISION_TENANT_ID=<elevated-tenant-guid>
export AZURE_PROVISION_CLIENT_ID=<elevated-app-client-id>
export AZURE_PROVISION_CLIENT_SECRET=<elevated-app-secret>
export AZURE_EVENTGRID_DELIVERY_KEY=<delivery-key-the-receiver-verifies>

atlas-azure provision \
  --subscription-id <subscription-guid> \
  --resource-group rg-atlas \
  --location eastus \
  --webhook-host https://atlas.example.com \
  --path /webhooks/azure/eventgrid \
  --with-diagnostic                  # also route Activity-Log events (optional)

# Tear it all down when you retire the receiver:
atlas-azure deprovision \
  --subscription-id <subscription-guid> \
  --resource-group rg-atlas \
  --location eastus \
  --webhook-host https://atlas.example.com \
  --with-diagnostic
```

| Flag                 | Subcommand  | Required | Default                       | Notes                                                                                                                                                                               |
| -------------------- | ----------- | -------- | ----------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--endpoint`         | both        | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                                                                                                                                                              |
| `--token`            | both        | yes      | env `SECURITY_ATLAS_TOKEN`    | security-atlas bearer token                                                                                                                                                         |
| `--insecure`         | both        | no       | `false`                       | disables TLS; loopback endpoints only                                                                                                                                               |
| `--tenant-id`        | `run`       | no\*     | env `AZURE_TENANT_ID`         | Entra tenant id (\*required, via flag or env)                                                                                                                                       |
| `--client-id`        | `run`       | no\*     | env `AZURE_CLIENT_ID`         | app-registration client id (client-credentials mode)                                                                                                                                |
| `--subscription-id`  | `run`       | yes†     | —                             | subscription for the storage + AKS + NSG + Key-Vault + Azure-Firewall kinds († unless all of `--skip-storage`, `--skip-aks`, `--skip-nsg`, `--skip-keyvault` and `--skip-firewall`) |
| `--environment`      | `run`       | yes      | —                             | environment scope tag; records are never emitted un-scoped                                                                                                                          |
| `--auth-mode`        | `run`       | no       | `client-credentials`          | `client-credentials` or `managed-identity`                                                                                                                                          |
| `--entra-control`    | `run`       | no       | `scf:IAC-21`                  | control id attached to entra records                                                                                                                                                |
| `--storage-control`  | `run`       | no       | `scf:CRY-04`                  | control id attached to storage records                                                                                                                                              |
| `--aks-control`      | `run`       | no       | `scf:CFG-02`                  | control id attached to AKS records                                                                                                                                                  |
| `--nsg-control`      | `run`       | no       | `scf:NET-04`                  | control id attached to NSG records                                                                                                                                                  |
| `--keyvault-control` | `run`       | no       | `scf:CRY-09`                  | control id attached to Key-Vault records                                                                                                                                            |
| `--firewall-control` | `run`       | no       | `scf:NET-04`                  | control id attached to Azure-Firewall records                                                                                                                                       |
| `--skip-entra`       | `run`       | no       | `false`                       | skip the `azure.entra_role_assignment.v1` pull                                                                                                                                      |
| `--skip-storage`     | `run`       | no       | `false`                       | skip the `azure.storage_account_config.v1` pull                                                                                                                                     |
| `--skip-aks`         | `run`       | no       | `false`                       | skip the `azure.aks_cluster_config.v1` pull                                                                                                                                         |
| `--skip-nsg`         | `run`       | no       | `false`                       | skip the `azure.nsg_rules.v1` pull                                                                                                                                                  |
| `--skip-keyvault`    | `run`       | no       | `false`                       | skip the `azure.keyvault_access_config.v1` pull                                                                                                                                     |
| `--skip-firewall`    | `run`       | no       | `false`                       | skip the `azure.firewall_rules.v1` pull                                                                                                                                             |
| `--listen`           | `eventgrid` | no       | `127.0.0.1:8485`              | address to bind the Event Grid receiver (loopback default; terminate TLS at a reverse proxy)                                                                                        |
| `--path`             | `eventgrid` | no       | `/webhooks/azure/eventgrid`   | URL path the Event Grid receiver listens on                                                                                                                                         |
| `--credential-in`    | `eventgrid` | no       | `header`                      | where Event Grid carries the delivery key: `header` or `query`                                                                                                                      |
| `--credential-name`  | `eventgrid` | no       | `Authorization`               | header name (`credential-in=header`) or query-param name (`credential-in=query`) carrying the delivery key                                                                          |

The client secret is **only** read from `AZURE_CLIENT_SECRET` — never a CLI flag
— so it never lands in shell history. It is never logged and never enters an
evidence record (the resolved credential redacts its secret on every format
path).

`register` announces `name=azure-connector`,
`supported_kinds=[azure.entra_role_assignment.v1, azure.storage_account_config.v1, azure.aks_cluster_config.v1, azure.nsg_rules.v1, azure.keyvault_access_config.v1, azure.firewall_rules.v1]`,
and `profiles_supported=[pull, subscribe]` to `ConnectorRegistryService.Register`.
The `profiles_supported` values describe how the connector retrieves data **from
Azure** (a scheduled pull, or event-driven Event Grid receipt); the platform-side
wire is always push (invariant #3).

## Scope minimums

Every emitted record sets the minimum scope dimensions the connector-pattern
convention requires:

| Scope key       | Entra value              | Storage / AKS / NSG / Key-Vault / Firewall value | Source                         |
| --------------- | ------------------------ | ------------------------------------------------ | ------------------------------ |
| `cloud_account` | `azure:<tenant_id>`      | `azure:<subscription_id>`                        | the resolved credential / flag |
| `environment`   | the `--environment` flag | the `--environment` flag                         | the `--environment` flag       |

For Entra the account-equivalent is the **tenant**; for Storage, AKS, NSG,
Key Vault and Azure Firewall it is the **subscription** (ARM resources are
subscription-scoped). `run` fails loudly when `--environment` is unset rather
than pushing an un-scoped record.

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:azure:entra@<version>` for
identity records, `connector:azure:storage@<version>` for storage records,
`connector:azure:aks@<version>` for AKS records, `connector:azure:nsg@<version>`
for NSG records, `connector:azure:keyvault@<version>` for Key-Vault records, and
`connector:azure:firewall@<version>` for Azure-Firewall records, where
`<version>` is the build's module version (or `dev` under `go run`).

## Idempotency

Each record's `idempotency_key` is
`sha256("<kind>|<resource_id>|<hour_truncated_observed_at>")` (see
`internal/idem`). `observed_at` is truncated to the UTC hour, so two runs within
the same hour for the same assignment / storage account / vault / firewall
policy collapse to one ledger row; a run that crosses an hour boundary writes a
fresh record.
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
- **`azure.nsg_rules.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The connector
  verdicts the deterministic network-segmentation posture: `FAIL` when any rule
  allows inbound from the whole Internet (`*` / `0.0.0.0/0` / `Internet`) to a
  management port (SSH 22 / RDP 3389); `INCONCLUSIVE` when a per-NSG read
  errored; `PASS` otherwise. The full rule set (direction, access, protocol,
  source/dest prefix, port range, priority) plus the subnet / NIC association
  counts ride in the payload for the platform evaluator; the final pass/fail per
  (control, NSG) is the evaluator's.
- **`azure.keyvault_access_config.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The
  connector verdicts the deterministic secrets-management posture: `FAIL` when
  soft-delete is off, purge protection is off, or the vault is reachable from
  the public network with no default-Deny network ACL; `INCONCLUSIVE` when a
  per-vault read errored; `PASS` otherwise. The payload carries the access model
  (RBAC vs access-policy), purge-protection / soft-delete / public-network-access
  / network-ACL-default posture, and the access-policy / role-assignment
  **metadata** (principal id/type + granted permission verbs or role name) for
  the platform evaluator; the final pass/fail per (control, vault) is the
  evaluator's. Secret/key/certificate **values** are never read and never ride
  in the payload.
- **`azure.firewall_rules.v1` → `PASS` / `FAIL` / `INCONCLUSIVE`.** The connector
  verdicts the deterministic network-perimeter posture: `FAIL` when an allow rule
  collection permits traffic from the whole Internet (`*` / `0.0.0.0/0` /
  `Internet`) to a management port (SSH 22 / RDP 3389); `INCONCLUSIVE` when a
  per-policy rule-collection-group read errored; `PASS` otherwise. The full
  rule-collection-group tree (group priority ordering, each collection's
  action/priority, and per-rule protocols / source-dest prefixes / ports / target
  FQDNs) rides in the payload for the platform evaluator; the final pass/fail per
  (control, firewall policy) is the evaluator's. Flow logs, packet captures,
  traffic contents, NAT-rule secrets, threat-intel feeds, and route tables are
  never read and never ride in the payload.

## Not in v0 (follow-ons)

The connector ships six evidence surfaces. It does **not** ship:

- AKS workload / pod-level evidence — the AKS kind reads **cluster-level**
  management-plane configuration only; pod manifests, secrets, and container
  images are osquery / a Kubernetes-native connector's job
- NSG flow logs / packet captures / traffic contents — the NSG kind reads the
  security-**rule** posture only; traffic telemetry is out of scope
- Key-Vault secret / key / certificate **values** — the Key-Vault kind reads
  the management-plane access posture only; secret material lives on the data
  plane and is never read
- Key-Vault **role-assignment cursor pagination** — for an RBAC-authorization
  vault the connector now enumerates the per-vault
  `Microsoft.Authorization/roleAssignments` (slice 615, symmetric with the
  legacy access-policy read), but the per-vault read is a single **bounded**
  page (capped defensively) — a vault with more role assignments than the cap
  needs the shared cursor-pagination follow-on (filed as spillover slice 623)
- Azure Firewall flow logs / packet captures / traffic contents / NAT-rule
  secrets / threat-intel feeds / route tables — the Azure-Firewall kind (slice 614) reads the rule-**collection** posture only (network + application rule
  collections + rule-collection-group priority ordering); traffic telemetry,
  NAT secrets, threat-intel, and routing topology are out of scope
- Azure Firewall **rule-collection-group cursor pagination** — the per-policy
  `ruleCollectionGroups` read is a single **bounded** page (capped defensively);
  a policy with more rule-collection groups than the cap needs the shared
  cursor-pagination follow-on (filed as spillover slice 634)
- route tables / VNet peering topology
- Azure-Policy evidence
- cursor pagination / multi-subscription auto-enumeration (v0 reads a bounded
  first page for one subscription)

These are filed as follow-on slices (see `docs/issues/486-azure-connector.md`,
`docs/issues/519-azure-aks-workload-config-evidence.md`, and the sibling
follow-ons NSG/Key-Vault/Azure-Firewall/Azure-Policy 520 / 521 / 614).

## Tests

```sh
go test ./connectors/azure/...
```

Unit tests fake the Microsoft Graph + Azure Resource Manager surfaces (no live
Azure, no real credentials) and pin the normalization, the storage, AKS, NSG,
Key-Vault **and** Azure-Firewall hardening verdict matrices, the credential
redaction, the read-only permission contract, and the `idem` hour-window
behavior. Structural over-collection tests (`internal/aks`, `internal/nsg`,
`internal/keyvault`, `internal/firewall`) reflect over the collector structs'
field names and fail the build if any field even hints at a secret / credential /
kubeconfig / workload-content (AKS), flow-log / packet / traffic-content (NSG),
secret/key/certificate-value (Key Vault), or flow-log / packet / traffic /
NAT-secret / threat-intel / route-table (Azure Firewall) surface; the AKS client
test asserts the read issues only a `GET` to the managed-cluster list endpoint
(never a `credential` endpoint), the NSG client test asserts the read issues only
a `GET` to the networkSecurityGroups list endpoint (never a mutate), the
Key-Vault client test asserts the read issues only a `GET` to the
`Microsoft.KeyVault/vaults` management-plane list endpoint (never a
`vault.azure.net` data-plane call), and the Azure-Firewall client test asserts
the policy-list + per-policy rule-collection-group reads are GETs only (never a
mutate). The integration test (in-package, bufconn platform — no Postgres)
exercises the full collect → build → SDK `Push` → push-receipt round-trip for all
six kinds and asserts two same-hour pushes collapse to one `record_id`, that
emitted payloads carry config / assignment / rule / access-policy metadata only
(no PII / secrets / admin kubeconfig / flow logs / secret values / NAT secrets /
threat-intel — a per-kind allow-list, descending into the NSG rule list, the
Key-Vault access-entry list, and the Azure-Firewall rule-collection-group tree),
and that the credential never surfaces in a formatted log.
