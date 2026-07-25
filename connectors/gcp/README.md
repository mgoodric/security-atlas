# GCP connector

The slice-442 GCP connector. After AWS, GCP is the highest-demand missing
cloud for the SaaS-startup persona: many startups run on GCP, and "prove our
GCP IAM and storage are locked down" is a recurring SOC 2 / ISO access-control
and encryption evidence demand. This connector collects exactly two high-signal
metadata surfaces — project IAM bindings and Cloud Storage bucket configuration
— and pushes them to the platform. It **never reads stored object contents,
service-account key material, or secret values**.

It follows the slice-004 AWS connector pattern: register-per-run, stable
`actor_id`, hour-truncated `observed_at`, scope minimums, vendor-native auth.

| Kind                           | Profile | Source                                                  | Default SCF anchors |
| ------------------------------ | ------- | ------------------------------------------------------- | ------------------- |
| `gcp.iam_policy_binding.v1`    | pull    | Resource Manager `getIamPolicy` + IAM `serviceAccounts` | `IAC-21`, `IAC-15`  |
| `gcp.storage_bucket_config.v1` | pull    | Cloud Storage `buckets.list` (`projection=noAcl`)       | `CRY-04`, `NET-04`  |

- **iam_policy_binding** — one record per project-IAM `(role, member)` binding,
  enriched with service-account inventory facts (is the member a service
  account, is that account disabled). Verdict: `pass` (disabled service-account
  grant — inert), `inconclusive` (every live binding — the platform evaluator
  owns the least-privilege decision, mirroring `azure.entra_role_assignment`). A
  connector-side `is_privileged` heuristic flags owner/editor/IAM-admin grants
  for the evaluator's attention.
- **storage_bucket_config** — one record per Cloud Storage bucket: encryption
  (default CMEK key), public-access-prevention state, uniform bucket-level
  access, object versioning, and retention-policy duration. Verdict: `pass`
  (public access enforced-prevented AND uniform access on), `fail` (public
  access not prevented, or per-object ACLs still permitted).

## What this connector does NOT collect (threat-model I — primary risk)

A GCP project holds extremely sensitive data. **The connector never reads
it.** No stored object contents, no `objects.get` / media download, no
service-account key material (`keys.list`/`keys.get` are never called), no
secret values, no bucket ACL entries. The evidence structs physically have no
field that could hold any of these, and two build-failing tests enforce it:

- a reflection guard (`gcpcollect_test.go::TestNoObjectContentField`) fails the
  build if a content/secret-bearing field is ever added to an evidence struct;
- a payload guard (`cmd_run_test.go::TestDoRun_PayloadIsConfigOnly`) fails the
  build if a pushed record's payload ever carries a content/secret/credential
  key, or if the GCP credential leaks into a payload value.

The bucket read requests `projection=noAcl` — the least-detail projection,
which never returns the per-object ACL surface.

## Auth — least-privilege read-only GCP identity

The connector authenticates to GCP via **Application Default Credentials**
(ADC) or a service-account key, requesting only the read-only OAuth scope
`cloud-platform.read-only`. The credential is source-side (invariant #3): it
stays with the connector process, is carried only on the outbound GCP
`Authorization` header, and is **never logged** or transmitted to the platform
(a `String()`/`GoString()` redaction guard + `TestCredentialNeverLogged`
enforce this).

Grant the connector's identity these **read-only** roles, and no more:

| GCP role                     | Why                                                                |
| ---------------------------- | ------------------------------------------------------------------ |
| `roles/iam.securityReviewer` | read the project IAM policy + the service-account inventory        |
| `roles/storage.bucketViewer` | read bucket CONFIGURATION (encryption, public-access, versioning…) |

**Banned roles — never grant these.** Any write/admin/data-read role defeats
the least-privilege and over-collection boundaries and is a misconfiguration:
`roles/owner`, `roles/editor`, `roles/iam.serviceAccountKeyAdmin`,
`roles/storage.admin`, `roles/storage.objectAdmin`, and crucially
**`roles/storage.objectViewer`** — the data-plane object-read role the
connector must never hold. `roles/storage.bucketViewer` grants bucket-config
read **without** object-data read; that distinction is the boundary.

## Pull profile and interval (named honestly)

`register` announces `profiles_supported=[pull]`. `profiles_supported`
describes how the connector retrieves data **from GCP** — a scheduled poll.
The **platform-side wire is always push** (invariant #3): every record is
pushed to the single `EvidenceIngestService.Push` API.

Recommended cadence: **daily**, run by the operator's job runner. This is a
scheduled pull, named honestly — it is **not** event-driven and **not**
"continuous monitoring". An event-driven profile via a GCP audit-log sink is a
follow-on slice.

## Subcommands

```sh
# Build the binary (justfile target).
just connector-build-gcp   # produces ./bin/gcp-connector

# Announce this connector instance to the platform.
gcp-connector register \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN"

# Read the project IAM policy + bucket config and push evidence.
# Credentials come from ADC (GOOGLE_APPLICATION_CREDENTIALS or workload identity).
gcp-connector run \
  --endpoint platform.example.com:443 \
  --token "$SECURITY_ATLAS_TOKEN" \
  --project my-prod-project
```

| Flag                  | Subcommand | Required | Default                       | Notes                                 |
| --------------------- | ---------- | -------- | ----------------------------- | ------------------------------------- |
| `--endpoint`          | both       | yes      | env `SECURITY_ATLAS_ENDPOINT` | platform gRPC endpoint                |
| `--token`             | both       | yes      | env `SECURITY_ATLAS_TOKEN`    | platform bearer token                 |
| `--insecure`          | both       | no       | `false`                       | disables TLS; loopback endpoints only |
| `--project`           | `run`      | yes      | —                             | GCP project id to collect from        |
| `--iam-control-id`    | `run`      | no       | `scf:IAC-21`                  | control id on IAM-binding records     |
| `--bucket-control-id` | `run`      | no       | `scf:CRY-04`                  | control id on storage-bucket records  |

The GCP credential is supplied via Application Default Credentials — set
`GOOGLE_APPLICATION_CREDENTIALS` to a service-account key file, or run under a
workload-identity-bound service account. The connector reads the credential
only via ADC; it is never a CLI flag (so it never lands in shell history).

## Scope minimums

Every emitted record sets the minimum scope dimension the slice-004
connector-pattern convention requires:

| Scope key       | Value shape        | Source             |
| --------------- | ------------------ | ------------------ |
| `cloud_project` | `gcp:<PROJECT_ID>` | the `--project` id |

This mirrors the AWS connector's `cloud_account` (`aws:<ACCOUNT_ID>`) and
Slack's `tenant_workspace`. Org/folder-level scoping is a follow-on slice.

`source_attribution.actor_id` follows the cross-connector convention
`connector:<vendor>:<service>@<version>` — `connector:gcp:iam@<version>` and
`connector:gcp:storage@<version>` — so the two kinds carry distinct, traceable
actor ids.

## Idempotency

`idempotency_key = sha256(anchor | hour_truncated_observed_at)`:

- **IAM binding** records anchor on `<project>|<role>|<member>`.
- **Storage bucket** records anchor on `bucket:<bucket_name>`.

Two runs within the same hour collapse to one ledger row; a run crossing an
hour boundary writes a fresh record.
`source_attribution.session_id` is left empty on purpose: a per-call UUID would
change the record's canonical hash between dedup retries.

## Bounded reads (threat-model D — denial of service)

A project with thousands of bindings or buckets could make a run unbounded.
Every paginated read (service-account inventory, bucket list) is capped at
`gcpcollect.MaxPages` (100 pages); exceeding the cap is a hard error, not a
silent truncation.

## Anti-criteria (P0)

- Reads stored object contents / secret values / service-account keys → REJECTED.
  IAM-binding + bucket-CONFIGURATION metadata only; structurally enforced.
- Requires or documents a broad / write / data-read GCP role → REJECTED.
  Read-only least-privilege roles only; `storage.objectViewer` is banned.
- Logs or transmits the GCP credential to the platform → REJECTED. Credential is
  source-side, header-only, redacted in every `fmt` verb.
- Widens the platform-side wire → REJECTED. Push only (invariant #3).
- Labels the pull profile "continuous monitoring" → REJECTED. Honest interval.
- Ships GKE / VPC / org-policy evidence → out of scope; follow-on slices.

## Tests

```sh
go test ./connectors/gcp/...
```

Unit tests fake the GCP REST surfaces (no live GCP) and pin the IAM / bucket
verdicts, the `(role, member)` fan-out + service-account disabled-state join,
member classification, bucket-config parsing, pagination + the `MaxPages`
denial-of-service bound, the idempotency hour-window behavior, credential
redaction, the read-only-role discipline, the collect→push round-trip, and the
two over-collection guards.
