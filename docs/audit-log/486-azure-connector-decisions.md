# 486 — Azure connector (Entra ID + Storage) — JUDGMENT decisions log

**Slice type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Author:** implementing agent
**Parent invariants:** canvas invariant #3 (push-only wire), evidence-integrity
(sha256), anti-pattern bans (no proprietary connector, honest intervals).

The connector framework is the slice-004 (AWS) / slice-045 (Okta) pattern
verbatim: `cmd/` cobra glue, `internal/<source>` collectors, register-per-run,
a stable `actor_id`, `observed_at` truncation, and bufconn round-trip tests. The
genuine JUDGMENT surface is the two evidence-kind shapes, their
`x-default-scf-anchors`, and the scope minimum. Those calls are recorded below;
the maintainer re-checks anchor accuracy (OQ #9 is load-bearing).

---

## Decisions

### D1 — Connector shape: one binary, two collectors, two evidence kinds

`connectors/azure/cmd/atlas-azure` (binary `atlas-azure`) with two
`internal/` collectors:

- `internal/entra` — Microsoft Graph reads → `azure.entra_role_assignment.v1`
- `internal/storage` — Azure Resource Manager reads → `azure.storage_account_config.v1`

plus `internal/azureauth` (credential resolution + DocumentedPermissions) and
`internal/idem` (per-kind hour-windowed idempotency keys). This mirrors the Okta
connector's multi-kind layout (the closest exemplar — okta also pulls multiple
kinds with one binary). Mirroring an existing locked pattern over inventing a new
one is the slice's explicit Phase-2 grill conclusion.

**Confidence:** HIGH — directly mirrors a merged, tested exemplar.

### D2 — Evidence-kind shape: `azure.entra_role_assignment.v1`

One record per **(principal, role, scope)** directory-role/RBAC assignment, plus
service-principal / app-registration inventory facts needed to interpret the
assignment. Payload fields (all config / assignment metadata — NO PII beyond the
identity needed to name the assignment):

| field                    | type              | notes                                                  |
| ------------------------ | ----------------- | ------------------------------------------------------ |
| `assignment_id`          | string (required) | stable id of the role assignment                       |
| `principal_id`           | string (required) | object id of the assigned principal                    |
| `principal_type`         | string (required) | `user` \| `servicePrincipal` \| `group`                |
| `principal_display_name` | string            | the assignment's human label (NOT mailbox/profile PII) |
| `role_definition_id`     | string (required) | directory-role or RBAC role id                         |
| `role_display_name`      | string            | e.g. `Global Administrator`, `Reader`                  |
| `directory_scope_id`     | string            | `/` (tenant) or an AU/app scope                        |
| `is_privileged`          | boolean           | heuristic: role is a known high-privilege role         |
| `tenant_id`              | string            | the Entra tenant the assignment lives in               |

**Result:** `INCONCLUSIVE` (descriptive). The connector does not decide
pass/fail — the platform evaluator interprets which assignment pattern
passes/fails per (control, scope). This mirrors the Okta `app_assignment`
descriptive-emit decision (don't bake policy into the pipe).

**`x-default-scf-anchors`: `["IAC-21", "IAC-22"]`** — IAC-21 (Least Privilege)
and IAC-22 (Account Management). These are the same anchors `okta.app_assignment`
uses for the analogous identity-entitlement evidence, so the cross-connector
identity surface defaults consistently.

**Confidence:** MEDIUM-HIGH on field shape; MEDIUM on anchor exactness (the
maintainer re-checks per OQ #9 — IAC-21/IAC-22 are the natural least-privilege +
account-management pair, but a deployment mapping Entra role assignments to a
PAM-specific anchor may override).

### D3 — Evidence-kind shape: `azure.storage_account_config.v1`

One record per storage account. Payload fields (account configuration only — NO
blob/object contents, NO access keys, NO SAS tokens):

| field                      | type               | notes                                       |
| -------------------------- | ------------------ | ------------------------------------------- |
| `account_id`               | string (required)  | ARM resource id of the storage account      |
| `account_name`             | string (required)  | storage account name                        |
| `subscription_id`          | string (required)  | subscription the account lives in           |
| `resource_group`           | string             | resource group name                         |
| `location`                 | string             | Azure region                                |
| `encryption_enabled`       | boolean (required) | encryption-at-rest configured               |
| `encryption_key_source`    | string             | `Microsoft.Storage` \| `Microsoft.Keyvault` |
| `https_traffic_only`       | boolean (required) | secure-transfer-required flag               |
| `minimum_tls_version`      | string             | e.g. `TLS1_2`                               |
| `allow_blob_public_access` | boolean (required) | public-blob-access posture                  |

**Result:** mapped — `PASS` when `https_traffic_only && encryption_enabled &&
!allow_blob_public_access`, `FAIL` when any of the three hardening flags is off,
`INCONCLUSIVE` when a per-account read errored. This is a config posture the
connector CAN verdict deterministically (unlike role assignments, which need
policy context), so it mirrors the AWS S3 encryption-state PASS/FAIL/INCONCLUSIVE
decision rather than the descriptive Okta path.

**`x-default-scf-anchors`: `["CRY-04", "NET-04"]`** — CRY-04 (Encryption At Rest;
the same anchor `aws.s3.bucket_encryption_state` defaults to) + NET-04
(Transmission Confidentiality / secure-transfer/TLS). The storage record carries
both an at-rest signal (encryption) and an in-transit signal (HTTPS-only +
min-TLS), so it defaults to both anchors.

**Confidence:** HIGH on field shape (these are the canonical ARM
`StorageAccountProperties` hardening flags); MEDIUM on NET-04 exactness (CRY-04 is
the established encryption anchor; NET-04 is the natural transmission anchor but
the maintainer re-checks).

### D4 — Scope minimum

Every record sets the minimum two scope dimensions the connector-pattern
convention requires:

| scope key       | Entra value              | Storage value             |
| --------------- | ------------------------ | ------------------------- |
| `cloud_account` | `azure:<tenant_id>`      | `azure:<subscription_id>` |
| `environment`   | the `--environment` flag | the `--environment` flag  |

`cloud_account` namespaces by `azure:` (mirrors `aws:<account_id>`). For Entra the
account-equivalent is the **tenant**; for Storage it is the **subscription** (a
tenant can own many subscriptions, and ARM resources are subscription-scoped). The
connector fails loudly when `--environment` is unset rather than emitting an
un-scoped record (mirrors the AWS connector's loud-fail-on-missing-environment).

`environment` is taken from a required `--environment` flag rather than inferred
from an Azure tag, because Azure has no single canonical "environment" tag
convention (unlike the AWS account `Environment` tag the AWS connector reads via
Organizations). Inferring from a tag is a documented follow-on.

**Confidence:** HIGH.

### D5 — Stable optional fields / actor_id / observed_at

- **actor_id:** `connector:azure:entra@<version>` and
  `connector:azure:storage@<version>` per the cross-connector
  `connector:<vendor>:<service>@<version>` convention.
- **observed_at:** truncated to the UTC hour (idem key also keys on the hour) so
  two runs within the same hour for the same resource collapse to one ledger row
  — identical to AWS/Okta.
- **session_id:** intentionally left empty — a per-call UUID would change the
  canonical hash between dedup retries.
- **schema_version:** `1.0.0`.

**Confidence:** HIGH — mechanical mirror of the locked pattern.

### D6 — Auth: client-credentials or managed identity, redacted credential

`internal/azureauth.Resolve` accepts an Entra app-registration
**client-credentials** triple (`AZURE_TENANT_ID` + `AZURE_CLIENT_ID` +
`AZURE_CLIENT_SECRET`) OR a **managed-identity** mode (no secret;
`--auth-mode managed-identity`). The resolved `Credential.String()` is redacted
so `%v`/`%+v` cannot leak the secret (the Okta-connector redaction pattern; the
unit test pins it). The connector never logs the secret and never places it in an
evidence record or a push payload (P0-486-4).

`DocumentedPermissions()` returns the canonical least-privilege set:

- Graph **application** permissions: `Directory.Read.All`, `Application.Read.All`
  (read-only; gates the Entra kind).
- ARM **Reader** role on the in-scope subscription(s) (read-only; gates the
  Storage kind).

The companion unit test rejects any future widening into `*.ReadWrite.*` /
`*.Manage` / Owner / Contributor / Global Administrator (the substring guard the
Okta connector uses for write/delete/admin scopes).

**Confidence:** HIGH.

---

## Revisit-once-in-use

- **R1 — SCF anchor exactness (OQ #9).** IAC-21/IAC-22 (Entra) and CRY-04/NET-04
  (Storage) are defaults. The maintainer re-checks against the deployment's SCF
  crosswalk; a PAM-heavy program may remap Entra assignments, and a program that
  separates at-rest vs in-transit controls may split the storage record's
  anchors.
- **R2 — `is_privileged` heuristic.** The Entra record's `is_privileged` flag is
  a connector-side heuristic over a known high-privilege role-name set. If the
  evaluator wants the raw role-template-id instead, drop the heuristic and let the
  evaluator decide.
- **R3 — Pagination + per-run cap.** v0 pulls a bounded first page per Graph/ARM
  list (the AWS/Okta v0 convention). A large tenant (thousands of SPs, many
  subscriptions) needs cursor pagination + a per-run cap — filed as a follow-on
  (threat-model D mitigation is the bounded page + run timeout in v0).
- **R4 — Subscription enumeration.** v0 takes `--subscription-id` explicitly
  (one subscription per run). Auto-enumerating subscriptions via ARM
  `subscriptions/list` is a follow-on; explicit is the least-surprising v0.

## Confidence summary

| decision              | confidence  |
| --------------------- | ----------- |
| D1 connector shape    | HIGH        |
| D2 entra kind shape   | MEDIUM-HIGH |
| D2 entra anchors      | MEDIUM      |
| D3 storage kind shape | HIGH        |
| D3 storage anchors    | MEDIUM      |
| D4 scope minimum      | HIGH        |
| D5 stable fields      | HIGH        |
| D6 auth               | HIGH        |

## Detection-tier classification (slice 353 Q-13)

- `detection_tier_actual`: `none`
- `detection_tier_target`: `none`

No bug surfaced during the build. The collect→push round-trip, the
no-PII/no-secret assertions, and the credential-redaction test are all green at
the unit + bufconn-integration tier.
