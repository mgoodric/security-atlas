# Control Bundle Format (v1)

> **Status:** Stable for slice 009. Backwards-compatible additions allowed; breaking changes require a `bundle_schema_version` bump.

A **control bundle** is the unit of authorship for a security-atlas control. It is a self-contained directory (or `.tar.gz` archive of one) declaring:

- **Metadata** — what the control is, which SCF anchor it claims, who owns it, where it applies.
- **Evidence queries** (optional) — Rego, SQL, or JSON-path expressions that read the evidence ledger and produce a pass/fail signal. _Stored only in slice 009 — execution lives in slice 012._
- **Manual evidence schema** (optional) — JSON Schema for manual attestation forms when `implementation_type` is `manual_periodic` or `manual_attested`.
- **Tests** (optional) — fixture evidence + expected pass/fail (slice 012 will execute these).

Anyone can author a control bundle in their editor of choice, run `security-atlas-cli controls validate ./my-control/` to check schema correctness, and `security-atlas-cli controls upload ./my-control/` to push it to the catalog. Re-uploading the same `bundle_id` creates a new version row that supersedes the prior — the supersession chain is preserved for auditability.

---

## 1. Bundle layout

A bundle is **either** a directory **or** a single `.tar.gz` archive whose root contents are the directory layout below.

```
my-control/
├── control.yaml                 (required) manifest — all metadata, queries, schema
├── description.md               (optional) longer-form description appended to the manifest description
├── tests/                       (optional) per-query fixture evidence + expected output
│   └── ...
└── README.md                    (optional) author-facing documentation, ignored by the parser
```

Only `control.yaml` is mandatory. Everything else is optional and ignored if absent.

When uploaded as a tarball, the archive **MUST**:

- Contain `control.yaml` at the archive root (not in a subdirectory).
- Use forward-slash paths only. Absolute paths (`/etc/passwd`) and parent-traversal segments (`../../etc/passwd`) are rejected with a 400 (anti-criterion: tar slip).
- Be under **5 MB compressed** and **20 MB uncompressed** with at most **500 entries** (defense against decompression bombs).

---

## 2. Manifest schema

### 2.1 Required fields

| Field                   | Type   | Notes                                                                                                                                                     |
| ----------------------- | ------ | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `bundle_schema_version` | string | Exactly `"1"` in this slice. Future v2 will gate breaking changes.                                                                                        |
| `bundle_id`             | string | Stable natural key. Re-uploading the same `bundle_id` is a version bump. Format: `^[a-z][a-z0-9_.-]{2,63}$` (lowercase).                                  |
| `title`                 | string | Human-readable, 1-200 chars.                                                                                                                              |
| `scf_anchor_id`         | string | SCF code (e.g., `IAC-06`) **or** the UUID of an `scf_anchors` row. Must resolve to a row in `scf_anchors` or the upload is rejected (canvas invariant 7). |
| `implementation_type`   | enum   | `automated` \| `semi_automated` \| `manual_attested` \| `manual_periodic`.                                                                                |

### 2.2 Optional fields

| Field                    | Type        | Notes                                                                                                                 |
| ------------------------ | ----------- | --------------------------------------------------------------------------------------------------------------------- |
| `description`            | string      | Longer-form narrative. If `description.md` is present in the bundle, its body is appended.                            |
| `control_family`         | string      | SCF taxonomy family (AAA, AST, BCD, ...). Inferred from the SCF anchor if omitted.                                    |
| `owner_role`             | string      | RACI role responsible (e.g., `security-engineering`).                                                                 |
| `lifecycle_state`        | enum        | Defaults to `draft`. One of: `draft`, `proposed`, `active`, `deprecated`, `retired`.                                  |
| `freshness_class`        | enum        | `realtime`, `hourly`, `daily`, `weekly`, `monthly`, `quarterly`, `annual`. Drives the evidence-staleness UI.          |
| `applicability_expr`     | JSON object | Boolean AST over scope dimensions (slice 017). Omitted = matches every cell. Same shape as `internal/scope/expr.go`.  |
| `linked_policy_ids`      | string[]    | Free-form policy identifiers. v1 stores them verbatim; slice 022 will validate against the `policies` table.          |
| `evidence_queries`       | object[]    | Zero or more queries. See §2.3.                                                                                       |
| `manual_evidence_schema` | JSON Schema | When `implementation_type` is `manual_*`, the schema of the attestation form. Authoritative for the manual upload UI. |

### 2.3 Evidence queries

Each entry has:

| Field           | Type   | Notes                                                                                                                              |
| --------------- | ------ | ---------------------------------------------------------------------------------------------------------------------------------- |
| `id`            | string | Unique within the bundle. Pattern `^[a-z][a-z0-9_-]{2,63}$`.                                                                       |
| `language`      | enum   | `rego` \| `sql` \| `jsonpath`. The slice-009 parser stores the language verbatim; the slice-012 evaluator dispatches on it.        |
| `expression`    | string | The query body. **Not executed in slice 009** — only stored.                                                                       |
| `evidence_kind` | string | Optional. When set, must match an `evidence_kind` registered in the schema registry (slice 014). Unregistered kinds reject upload. |
| `description`   | string | Author-facing comment.                                                                                                             |

### 2.4 applicability_expr shape (mirror of slice 017)

```yaml
applicability_expr:
  op: "and"
  args:
    - { op: "eq", dim: "environment", value: "prod" }
    - {
        op: "in",
        dim: "data_classification",
        values: ["restricted", "confidential"],
      }
```

Operators: `true`, `eq`, `in`, `and`, `or`, `not`. Empty / null / `{}` means "applies to every scope cell". The full operator reference lives in `internal/scope/expr.go`.

AC-5: a bundle whose `applicability_expr` references an undeclared scope dimension (or uses an unknown operator, or malformed args) is **rejected at parse**.

---

## 3. Full worked example

A complete `control.yaml` for an MFA control on production AWS:

```yaml
bundle_schema_version: "1"
bundle_id: aws_iam_mfa_prod
title: "MFA enforced for all human IAM users in production AWS"
scf_anchor_id: IAC-06
implementation_type: automated
control_family: IAC
owner_role: security-engineering
lifecycle_state: proposed
freshness_class: daily

description: |
  Enforces MFA on every human IAM user in production AWS accounts. Service
  accounts are exempt via the `is_service_account` tag. Evaluated daily from
  the AWS connector's `iam_user.access_state` evidence.

applicability_expr:
  op: "and"
  args:
    - { op: "eq", dim: "environment", value: "prod" }
    - { op: "in", dim: "cloud_account", values: ["aws-prod-1", "aws-prod-2"] }

linked_policy_ids:
  - policy_identity_access_management

evidence_queries:
  - id: aws_iam_mfa_check
    language: rego
    evidence_kind: aws.iam_user.access_state
    description: "All human IAM users must have MFA enabled"
    expression: |
      package atlas.controls.aws_iam_mfa_prod
      default allow := false
      allow if {
        every u in input.iam_users {
          u.is_service_account == true
        } or {
          u.mfa_enabled == true
        }
      }
```

A minimal **manual** control:

```yaml
bundle_schema_version: "1"
bundle_id: annual_access_review
title: "Annual access review completed for all production systems"
scf_anchor_id: IAC-15
implementation_type: manual_periodic
freshness_class: annual

manual_evidence_schema:
  $schema: "https://json-schema.org/draft/2020-12/schema"
  type: object
  required: [reviewed_at, reviewer, systems_reviewed]
  properties:
    reviewed_at: { type: string, format: date }
    reviewer: { type: string, minLength: 1 }
    systems_reviewed: { type: array, minItems: 1, items: { type: string } }
    findings_count: { type: integer, minimum: 0 }
```

---

## 4. Upload API

### CLI

```
security-atlas-cli controls validate ./my-control/
security-atlas-cli controls upload   ./my-control/

# Tarball forms accepted for both subcommands:
security-atlas-cli controls validate ./my-control.tar.gz
security-atlas-cli controls upload   ./my-control.tar.gz
```

`validate` is local-only: no network call, no auth required, exits non-zero with a printed field path on the first schema error.

`upload` posts to `POST /v1/controls:upload-bundle` and prints the resulting `control_id`, `version`, and `superseded_id` (if this upload replaced a prior version).

### HTTP

```
POST /v1/controls:upload-bundle
Authorization: Bearer <token>           (must be admin — same gate as slice 014)

Body either:
  (a) Content-Type: multipart/form-data
        - "bundle.tar.gz" file part: the gzip-compressed tarball
  (b) Content-Type: application/json
        - { "manifest_yaml": "<full YAML body>" }
```

Responses:

| Status | Meaning                                                                                                            |
| ------ | ------------------------------------------------------------------------------------------------------------------ |
| 201    | New control row created (initial upload, body has `control_id`, `bundle_id`, `version=1`).                         |
| 200    | New version row created and prior superseded (body has `control_id`, `bundle_id`, `version=N+1`, `superseded_id`). |
| 400    | Bundle malformed (missing field, bad applicability_expr, tar slip, unknown evidence_kind, too large).              |
| 403    | Calling credential is not admin.                                                                                   |
| 404    | `scf_anchor_id` does not resolve to an `scf_anchors` row.                                                          |
| 413    | Tarball over the size limit.                                                                                       |

### Versioning + supersession

- Re-upload with the same `bundle_id` creates a **new row** and sets the prior row's `superseded_by` to the new row's id in the same transaction (AC-6).
- The partial unique index `controls_one_active_version_per_bundle` guarantees only one active version per `(tenant_id, bundle_id)`.
- The full manifest YAML is stored verbatim on every version (`bundle_manifest_yaml`) plus its sha256 hex (`bundle_manifest_hash`) so auditors can prove byte-exact reproducibility.

---

## 5. Anti-criteria (rejected at upload, never stored)

| Reject                                                                  | Reason                                                        |
| ----------------------------------------------------------------------- | ------------------------------------------------------------- |
| `scf_anchor_id` absent or unresolvable                                  | Canvas invariant 7 — SCF anchoring is non-negotiable.         |
| `applicability_expr` malformed or references an unknown operator        | Slice 017 validator rejects up front.                         |
| `evidence_queries[*].evidence_kind` not in the schema registry          | Slice 014 contract — every kind must be registered.           |
| Tarball entry with `..` segment or absolute path                        | Tar slip protection (security review P0).                     |
| Tarball over 5 MB compressed, 20 MB uncompressed, or 500 entries        | Decompression bomb protection.                                |
| OSCAL `component-definition` files                                      | v2 extension; slice 009 is canonical-controls only.           |
| Evidence-query execution payload                                        | Slice 009 stores only; execution is slice 012.                |
| Bundle without `bundle_schema_version` or with a value other than `"1"` | Forward compatibility — unknown schema versions are rejected. |

---

## 6. Open questions (will be resolved in later slices)

- **Bundle signing.** v1 stores plaintext YAML. Slice 010 (or later) introduces optional cosign signatures on the bundle bytes.
- **Marketplace / public bundles.** Canvas open-question #13 covers community-contributed bundles. Slice 009 is per-tenant only.
- **Bundle export.** Round-trip (upload → export → re-upload) is implicit (the manifest YAML is stored verbatim) but no CLI subcommand for it lands in slice 009.
- **Test execution.** The `tests/` directory is parsed and stored as part of the bundle but not yet executed; slice 012's evaluation engine consumes it.
