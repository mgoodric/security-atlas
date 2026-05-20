# Evidence

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What an Evidence record is — a single, provenanced observation
    - How evidence flows in (ingestion) and how controls read it
      (evaluation)
    - How freshness, drift, and the append-only ledger work
    - How to push evidence from your own tools
<!-- prettier-ignore-end -->

An **Evidence record** in security-atlas is _a single observation about
reality at a point in time_. Provenance is mandatory; no anonymous
evidence. Once written, the record is **never mutated** — corrections
land as new records that supersede.

## The shape

| Field             | What it means                                                                                         |
| ----------------- | ----------------------------------------------------------------------------------------------------- |
| `id`              | UUIDv7 — time-ordered, so the ledger is naturally sortable by ingestion order.                        |
| `evidence_kind`   | The schema this record matches (e.g. `aws.s3.bucket_encryption_state.v1`). Registered, semver-pinned. |
| `control_id`      | What control this record feeds. Many-to-one.                                                          |
| `scope_id`        | Which scope cell this applies to.                                                                     |
| `observed_at`     | When the underlying system state was observed (NOT when we received it).                              |
| `ingested_at`     | When the platform received it. `now()` at write time.                                                 |
| `provenance`      | Connector ID, source system ID, source record key, query hash, runner ID.                             |
| `result`          | `pass` · `fail` · `na` · `inconclusive`                                                               |
| `payload`         | Raw observation as JSONB (redacted per policy).                                                       |
| `payload_uri`     | For artifacts > 1 MB — S3-compatible object store reference.                                          |
| `hash`            | sha256 of the payload — used for dedup and tamper detection.                                          |
| `freshness_class` | `realtime` · `daily` · `weekly` · `monthly` · `quarterly` · `annual` — inherited from the control.    |
| `valid_until`     | When this record stops being current. Past this date the record is **stale**, not deleted.            |

## Ingestion vs evaluation — two separated stages

Constitutional invariant 2: **ingestion and evaluation are separated
stages** with the append-only ledger between them. Evaluation never
writes to evidence. This means:

- A bug in evaluation never corrupts the record. You can fix the bug and
  re-run.
- New controls can be evaluated against historical evidence (retroactive
  coverage).
- Point-in-time replay is always possible — "what did we know on
  2026-03-15?" is a query, not an archaeology project.

```
[ Source ] ──► [ Connector or push ] ──► [ Ingestion: canonicalize · redact · hash · scope-tag ]
                                                          │
                                                          ▼
                                            [ Evidence ledger (append-only) ]
                                                          │
                                                          ▼   read-only
                                                [ Evaluation stage ]
                                                          │
                                                          ▼
                                              [ Control state per scope ]
```

## Freshness

Each control declares a `freshness_class`. Records age out at the
class's `max_age`:

| Class       | Max age | Example controls                                |
| ----------- | ------- | ----------------------------------------------- |
| `realtime`  | 24 h    | Production firewall config, prod IAM root usage |
| `daily`     | 7 d     | EDR coverage, MFA enforcement                   |
| `weekly`    | 30 d    | Vulnerability scan results                      |
| `monthly`   | 90 d    | Access review, vendor security questionnaire    |
| `quarterly` | 120 d   | DR test, tabletop exercise                      |
| `annual`    | 400 d   | Penetration test, policy reaffirmation          |

When the latest record for a `(control, scope_cell)` is past
`valid_until`, the cell is **stale**. The historical record is preserved
for audit replay; the dashboard shows a freshness warning and the
[drift](../walkthroughs/evaluation-pipeline.md) signal fires.

## Browsing evidence

Sign in and open **Evidence** in the sidebar. The browser lists records
across all controls in the active tenant, filterable by:

- Control
- Evidence kind
- Scope cell
- Time range (`observed_at` or `ingested_at`)
- Result (pass / fail / na / inconclusive)
- Provenance (connector / pusher / manual)

Each record opens a detail view with the full JSONB payload, the source
provenance trail, and the chain of supersedence (which records were
superseded by this one, which records it superseded).

## Pushing evidence from your own tools

Anything that produces evidence — CI pipelines, custom internal tools,
middleware, manual upload — can write directly to the ledger via the
**push profile** of the [Evidence
SDK](https://github.com/mgoodric/security-atlas/blob/main/Plans/EVIDENCE_SDK.md).

A minimal push via the CLI:

```sh
just atlas-cli evidence push \
  --kind sast.scan_result.v1 \
  --control-id <control id> \
  --scope-id <scope id> \
  --observed-at "$(date -u +%FT%TZ)" \
  --result pass \
  --payload ./scan-result.json \
  --idempotency-key "ci-$GITHUB_RUN_ID"
```

The CLI handles:

- Schema validation against the registered `evidence_kind`
- Idempotency (re-sending the same `idempotency-key` is a no-op)
- Authentication (short-lived OIDC token from your CI IdP, or a
  platform-issued API key)
- Retries with exponential backoff
- A receipt with the canonical record ID

See the [Connector authoring guide](../connector-authoring.md) for
multi-record streaming, scope inference, and middleware patterns.

## Exporting evidence

The ledger is exportable per slice 138 — CSV / JSON / XLSX. The export
deliberately **excludes** the raw `payload` column (vendor secrets like
bucket-policy JSON can ride payload bodies):

```sh
curl -fsS -X GET "http://localhost:8080/v1/admin/evidence/export?format=jsonl" \
  -H "Authorization: Bearer $ATLAS_TOKEN" \
  -o evidence-2026-Q2.jsonl
```

For full payload access, use the per-record detail API (which enforces
the same RBAC + tenant isolation but is rate-limited to discourage bulk
exfiltration).

## Next steps

- [Controls →](controls.md) — what evidence feeds
- [Scope →](scope.md) — where evidence applies
- [Connector authoring →](../connector-authoring.md) — write your own
  evidence source

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
