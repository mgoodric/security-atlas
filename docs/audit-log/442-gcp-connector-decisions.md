# Slice 442 — GCP connector — JUDGMENT decisions log

**Slice type:** JUDGMENT (evidence-kind shape + scope-minimum + stable-field choices)
**Parent:** docs/issues/442-gcp-connector.md
**Pattern template:** slice 004 (AWS connector — LOCKED connector pattern); slice 443 (Slack — most-recent same-shape); slice 486 (Azure — the closest analog: IAM-assignment + storage-config surfaces)

- detection_tier_actual: none
- detection_tier_target: none

> No product-code bug surfaced during the build. The connector is the
> slice-004 / slice-443 pattern verbatim; the work was GCP-API-specific
> collection + the two evidence-kind schemas, not re-inventing the framework.
> The over-collection word-splitter guard was authored correctly the first
> time by reusing the slice-443 lesson (word-token matching, not naive
> substring) plus a `TestSplitIdentifierWords` sanity test, so the slice-443
> guard-false-positive did not recur. `actual == target == none` — no escape
> because nothing escaped.

---

## D1 — Two evidence kinds (one connector, two surfaces)

The spec mandates ONE connector with TWO evidence surfaces (IAM + Cloud
Storage), the minimum that demonstrates GCP is a first-class peer. Each surface
gets its own `.v1` kind rather than a polymorphic single kind:

- `gcp.iam_policy_binding.v1` — access evidence, one record per `(role, member)`
  binding, descriptive (`INCONCLUSIVE`) — the evaluator owns least-privilege.
- `gcp.storage_bucket_config.v1` — bucket-hardening evidence, one record per
  bucket, pass/fail (public-access + uniform-access verdict).

**Why two kinds, not one:** a single kind would force a union payload schema
and a meaningless shared verdict. Two kinds keep each JSON Schema
`additionalProperties: false` and let the evaluator treat each surface with the
right semantics. This mirrors the Azure connector exactly
(`azure.entra_role_assignment.v1` + `azure.storage_account_config.v1`) — the
closest analog on `main`.

## D2 — IAM record grain + verdict semantics (the load-bearing shape call)

The IAM surface fans out to **one record per `(role, member)` binding**, not
one record per service account and not one per role. Rationale: the unit a SOC 2
/ ISO access reviewer reasons about is "who (member) has what (role)", which is
exactly a binding. The service-account inventory is folded in as enrichment (the
`is_service_acc` + `disabled` flags on each binding) rather than a third kind,
because the SA facts only matter as context for interpreting a binding.

Every **live** binding is emitted `INCONCLUSIVE` (descriptive): the connector
does not decide whether `roles/owner` on a given member violates least-privilege
— that is policy the platform evaluator owns, mirroring
`azure.entra_role_assignment`. The one exception is a **disabled** service-account
grant, emitted `PASS` (the grant is inert — correctly deprovisioned). A
connector-side `is_privileged` heuristic flags owner/editor/IAM-admin-family
roles so the evaluator's attention is drawn, without the connector usurping the
decision.

## D3 — SCF anchor choices (`x-default-scf-anchors`, OQ #9 governance)

Every chosen anchor was verified present in the bundled SCF fixture
(`migrations/fixtures/scf-sample.json`) via grep BEFORE selection, and the
slice-654 catalog-existence guard (`TestDefaultSCFAnchors_ResolveInBundledCatalog`)
re-validates them mechanically in CI. Verified-present titles:

| Kind                           | Anchors            | Rationale                                                                                                                                            |
| ------------------------------ | ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gcp.iam_policy_binding.v1`    | `IAC-21`, `IAC-15` | IAC-21 = Privileged Account Management (the "who has what role" surface, esp. high-privilege grants); IAC-15 = Account Review (feeds access review). |
| `gcp.storage_bucket_config.v1` | `CRY-04`, `NET-04` | CRY-04 = Encryption At Rest; NET-04 = Boundary Protection (the public-access dimension). The SAME pair `azure.storage_account_config.v1` uses.       |

**Considered and rejected — IAC-22 (Least Privilege).** The spec's narrative
gestures at least-privilege as the natural IAM anchor, and the Azure Key-Vault
kind (slice 521) and Grafana kind (slice 534) reference IAC-22. But **IAC-22 is
ABSENT from the bundled SCF catalog fixture** (grep-confirmed: 0 matches), so
selecting it would dangle and fail the slice-654 guard. `IAC-21` (Privileged
Account Management) — verified present — is the closest real anchor for the
"who holds privileged roles" surface, and is the primary. `IAC-15` (Account
Review) is the secondary because the binding inventory is precisely what an
access review consumes. This is the same dangling-candidate remap pattern the
k8s.secret_inventory slice (525, IAC-22→CRY-09) and monitoring.alert_firing
slice (535, IRO-02→IRO-09) recorded.

**Cross-connector consistency:** the storage anchors are deliberately identical
to the Azure storage kind's so an operator's encryption/public-access evidence
maps to the same controls regardless of which cloud produced it.

## D4 — Scope minimum: `cloud_project` only

Every record carries exactly one scope dimension: `cloud_project` =
`gcp:<PROJECT_ID>`. This mirrors AWS (`cloud_account` = `aws:<ACCOUNT_ID>`) and
Slack (`tenant_workspace`). Org- and folder-level scope dimensions are
**deliberately not added** — the spec forbids inventing unjustified scope
dimensions, and a single-project run is the v0 grain (one connector instance per
project, exactly as the AWS connector is one instance per assumed account).
Org/folder scoping is named as a follow-on, not smuggled in.

## D5 — Stable-field choices (slice-004 connector-pattern conventions)

- **`actor_id` = `connector:gcp:<service>@<version>`** — `connector:gcp:iam@…`
  and `connector:gcp:storage@…`, so the two kinds carry distinct traceable
  actor ids (the slice-443 multi-service pattern).
- **`observed_at` truncated to the hour**, and the `idempotency_key` keys on the
  same hour, so two runs within an hour collapse to one ledger row (slice-004
  pattern). Anchors: `<project>|<role>|<member>` for IAM, `bucket:<name>` for
  storage.
- **`session_id` left empty** — a per-call UUID would change the record's
  canonical hash between dedup retries, turning idempotency into AlreadyExists.

## D6 — Auth: ADC, read-only scope, no data-plane role (threat-model E / I)

The connector authenticates via Application Default Credentials (ADC) /
service-account key with the single OAuth scope
`cloud-platform.read-only`, and the documented minimal roles are
`roles/iam.securityReviewer` + `roles/storage.bucketViewer`. The load-bearing
distinction: **`roles/storage.bucketViewer` reads bucket CONFIG without object
data**, whereas `roles/storage.objectViewer` (a data-plane role) is explicitly
**banned** — it is in `gcpauth.BannedRoles` and a test asserts it. The credential
is acquired via ADC only (never a CLI flag, so it never lands in shell history),
is read only at the `gcpapi` Authorization header, and is redacted in every
`fmt` verb (`String()`/`GoString()` + `TestCredentialNeverLogged`).

## D7 — Net/http adapter over the GCP Go SDK (dependency discipline)

The vendor adapter uses the standard library `net/http` against the GCP REST
APIs (resource-manager `getIamPolicy`, IAM `serviceAccounts.list`, Storage
`buckets.list`), exactly mirroring the Slack adapter — rather than pulling in
the large `cloud.google.com/go/*` SDK surface. Rationale: (a) it matches the
locked Slack pattern (the `gcpapi` package is the ONLY network-touching package,
the ONLY place the credential is read); (b) it keeps the new-dependency
footprint to a single transitive indirect (`cloud.google.com/go/compute/metadata`,
already pulled by the in-tree `golang.org/x/oauth2/google` used for ADC) with
**zero** new module vulnerabilities (`govulncheck`: "0 vulnerabilities in
modules you require"); (c) the three read hosts are constant-folded and guarded
so the data plane (`/b/<bucket>/o`) is structurally unreachable.

## Over-collection enforcement (threat-model I — primary risk)

Two build-failing guards, mirroring slice 443:

1. `gcpcollect_test.go::TestNoObjectContentField` — reflects over every field of
   both evidence structs; fails the build if any field's word tokens denote
   object content / secret material (`object`, `blob`, `content`, `data`,
   `secret`, `credential`, `acl`, …). A `TestSplitIdentifierWords` sanity test
   proves the splitter correctly tokenizes acronym fields (`DefaultKMSKeyName`
   → `[default kms key name]`), so the CMEK **key name** is not mistaken for a
   key **value**.
2. `cmd_run_test.go::TestDoRun_PayloadIsConfigOnly` — asserts no pushed payload
   key carries a banned token and no payload value carries the GCP credential
   (P0-442-4).

The bucket read requests `projection=noAcl` (least detail — never the per-object
ACL surface), and the SA inventory reads email + disabled only (never
`keys.list`/`keys.get`).
