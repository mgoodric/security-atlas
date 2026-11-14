# Control Bundle Format (v1)

> **Status:** Stable for slice 009. Backwards-compatible additions allowed; breaking changes require a `bundle_schema_version` bump.

A **control bundle** is the unit of authorship for a security-atlas control. It is a self-contained directory (or `.tar.gz` archive of one) declaring:

- **Metadata** — what the control is, which SCF anchor it claims, who owns it, where it applies.
- **Evidence queries** (optional) — Rego, SQL, or JSON-path expressions that read the evidence ledger and produce a pass/fail signal. _Stored only in slice 009 — execution lives in slice 012._
- **Manual evidence schema** (optional) — JSON Schema for manual attestation forms when `implementation_type` is `manual_periodic` or `manual_attested`.
- **Tests** (optional) — fixture evidence + expected pass/fail, executed by `controls test` (slice 496; see §4a).

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
| `scf_anchor_id`         | string | SCF code (e.g., `IAC-37`) **or** the UUID of an `scf_anchors` row. Must resolve to a row in `scf_anchors` or the upload is rejected (canvas invariant 7). |
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
| `external_refs`          | object[]    | Identifiers outside the platform that this control traces to. See §2.5.                                               |

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

### 2.5 External references (#1737)

`external_refs` keeps the trace from a control to the identifiers auditors and GRC teams work from: the organisation's own control number, an auditor's reference, or the framework requirement the control was tested under. A unified control merged from several audited activities lists one entry per activity instead of burying them in `description`.

| Field    | Type   | Notes                                                                                                                                        |
| -------- | ------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `system` | string | Required. Where the id comes from: a framework slug (`soc2`) or an organisation label (`acme-audit`). Pattern `^[a-z0-9][a-z0-9_.-]{0,63}$`. |
| `kind`   | enum   | `reference` (default) or `tested_under`. For `tested_under`, `system` is the framework slug and `id` the requirement code.                   |
| `id`     | string | Required, at most 200 characters. The identifier as the other system writes it (`RA-03`, `CC3.1`).                                           |
| `note`   | string | Optional, at most 500 characters. E.g. `"FY25 report, section 4"`.                                                                           |

A bundle may carry at most 100 entries, and no two may share the same `system` + `kind` + `id`. Length caps count characters, not bytes. The refs are stored per control version, shown in the control detail header, and matched by `/v1/search` (system, id and note).

**Adding or editing refs updates the control in place (#1958).** Refs are trace metadata and do not feed evaluation, so a re-upload whose `control.yaml` differs from the active version only in `external_refs` and/or `description` keeps the control id and version. Owners, evidence, exceptions and links stay attached, and the prior manifest is kept in `control_manifest_revisions`. A refs change made together with any other field change still creates a new version. See §4 "Versioning + supersession".

```yaml
external_refs:
  - system: acme-audit
    id: RA-03
    note: "2025 SOC 2 report, Section 4"
  - system: soc2
    kind: tested_under
    id: CC3.1
    note: "FY25 report, section 4"
```

---

## 3. Full worked example

A complete `control.yaml` for an MFA control on production AWS:

```yaml
bundle_schema_version: "1"
bundle_id: aws_iam_mfa_prod
title: "MFA enforced for all human IAM users in production AWS"
scf_anchor_id: IAC-37
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
      package evidence.query
      result := "fail" if {
        some r in input.records
        r.result == "fail"
      } else := "pass" if {
        some r in input.records
        r.result == "pass"
      } else := "inconclusive" if {
        some r in input.records
        r.result == "inconclusive"
      } else := "na" if {
        count(input.records) > 0
      } else := "inconclusive"
```

A Rego evidence query lives in `package evidence.query` and assigns `result`
(`pass | fail | na | inconclusive`). Its only input is
`{records: [{result}]}`: the per-record verdicts the connector
already decided. It never sees the payload; use the SQL or JSON-path languages
to read payload fields.

A minimal **manual** control:

```yaml
bundle_schema_version: "1"
bundle_id: annual_access_review
title: "Annual access review completed for all production systems"
scf_anchor_id: IAC-12
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

- Re-upload with the same `bundle_id` creates a **new row** and sets the prior row's `superseded_by` to the new row's id in the same transaction (AC-6). Two exceptions:
  - A byte-identical `control.yaml` is a no-op: the active row is returned unchanged (slice 068).
  - A `control.yaml` that differs from the active version's **only in the top-level `description` and/or `external_refs`** (rewording, reflow, adding, editing or removing either) updates the active row in place. The control id and version stay the same, so owners, evidence, exceptions and links stay attached. The response is `200` with the existing `control_id` and `version` and no `superseded_id`. Any other change, including `title` or a nested `evidence_queries[].description`, still creates a new version. See `docs/audit-log/1908-control-description-in-place-decisions.md` and `docs/audit-log/1958-control-external-refs-in-place-decisions.md`.
- The partial unique index `controls_one_active_version_per_bundle` guarantees only one active version per `(tenant_id, bundle_id)`.
- The full manifest YAML is stored verbatim on every version (`bundle_manifest_yaml`) plus its sha256 hex (`bundle_manifest_hash`) so auditors can prove byte-exact reproducibility. An in-place update overwrites both on the active row, so the prior manifest, its hash, uploader and upload time are first copied, in the same transaction, to the append-only `control_manifest_revisions` table. That table is where a control's prose and refs history lives. An upload locks the active row (`SELECT … FOR UPDATE`) before deciding, so concurrent uploads of one bundle run one after another and each in-place update records the manifest the previous one wrote. A concurrent upload that supersedes the row mid-update returns `409`.

---

## 4a. Testing a bundle (`controls test`) — slice 496

A control bundle can ship its own **tests**: fixture evidence records plus the control state you expect them to produce. `security-atlas-cli controls test ./my-control/` feeds each fixture through the **same evaluation engine** the platform uses live, so a bundle that passes its tests behaves identically once uploaded. This is the control-as-code analogue of "tests before code" — verify your evidence query locally before you ship it.

### Fixture layout

Test cases live in `*.yaml` files inside the bundle's `tests/` directory:

```
my-control/
├── control.yaml
└── tests/
    └── cases.yaml        # one or more test cases
```

Every `*.yaml` (or `*.yml`) file in `tests/` is read in lexical order; case order within a file is preserved. Case names must be unique across the whole `tests/` directory.

### Test-case schema

```yaml
cases:
  - name: all-users-have-mfa # required, unique
    description: "optional human context"
    expected_state: pass # required: pass | fail | na | inconclusive
    evaluated_at: 2026-06-01T12:00:00Z # optional: pins the freshness "now"
    records: # fixture evidence records
      - result: pass # required: pass | fail | na | inconclusive
        observed_at: 2026-06-01T00:00:00Z # optional, defaults to evaluated_at
        payload: # optional; read by SQL + JSON-path queries
          iam_users:
            - { mfa_enabled: true }
```

- **`expected_state`** is the control state you assert the query produces. An **expected-fail is a passing test**: a case that declares `expected_state: fail` passes when the query correctly returns `fail`.
- **`observed_at`** defaults to the case's evaluation instant, so a record is in-window by default — you only set it when you are testing freshness/staleness behaviour.
- **`payload`** is the evidence record's JSON body; the Rego path ignores it (it sees only `result`), while the SQL and JSON-path query languages read it.

### Worked example — the MFA control

`control.yaml` (Rego evidence query):

```yaml
bundle_schema_version: "1"
bundle_id: aws_iam_mfa_prod
title: "MFA on every prod IAM user"
scf_anchor_id: IAC-37
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: all_records_pass
    language: rego
    expression: |
      package evidence.query
      default result := "fail"
      result := "pass" if {
        count(input.records) > 0
        every r in input.records { r.result == "pass" }
      }
```

`tests/cases.yaml`:

```yaml
cases:
  - name: all-users-have-mfa # passing fixture
    expected_state: pass
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
  - name: one-user-without-mfa # failing fixture (expected-fail = passing test)
    expected_state: fail
    evaluated_at: 2026-06-01T12:00:00Z
    records:
      - result: pass
        observed_at: 2026-06-01T00:00:00Z
      - result: fail
        observed_at: 2026-06-01T06:00:00Z
```

Run it:

```
$ security-atlas-cli controls test ./aws-iam-mfa/
bundle: aws_iam_mfa_prod
  PASS  all-users-have-mfa (state=pass)
  PASS  one-user-without-mfa (state=fail)
2 passed, 0 failed, 0 errored of 2
```

### Behaviour + flags

- **Local, no network, no DB for Rego/JSON-path.** `controls test` runs entirely in-process for Rego and JSON-path queries — no Postgres required, no live evidence touched. A `sql`-language query needs a database connection; without one the SQL cases are reported as errors (never false passes).
- **`--json`** emits the full report as JSON for CI consumption.
- **Exit code.** The CLI exits **non-zero** on any failing or errored case, so it slots straight into a CI gate for community-contributed bundles. A bundle with no `tests/` directory is a warning, not a failure.
- **Freshness is faithful.** Records that exist but have aged out of the bundle's freshness window are `stale` (the freshness gate), so the query evaluates over an empty in-window set and returns its default branch. Zero records at all is `no_evidence` → `inconclusive` ("absence of evidence is not failure"). The runner reproduces the live engine exactly.

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
- ~~**Test execution.**~~ **Resolved (slice 496).** The `tests/` directory is now executed locally by `security-atlas-cli controls test` against the live evaluation engine — see §4a. Wiring bundle tests into the upload API as a hard gate ("tests must pass to upload") remains a follow-on.
