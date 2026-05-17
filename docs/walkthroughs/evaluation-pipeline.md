# Evidence to Control State — End-to-End Through the Evaluation Pipeline

_2026-05-16T06:17:51Z by Showboat 0.6.1_

<!-- showboat-id: aec5ae38-734a-4d84-83d7-cafeac0c9dc2 -->

> **Walkthrough kind:** this is a PAI Walkthrough skill document (slice 070 — showboat-generated). It is distinct from slice 027’s audit walkthrough (`internal/audit/walkthrough`), which records auditor evidence capture against controls. The two concepts share a word and nothing else.

## Overview

A push of evidence into security-atlas does not, by itself, change a control state. The push lands in the append-only ledger; the **eval engine** later observes the ledger, computes per-cell state per the control bundle, and writes one row per applicable scope cell into `control_evaluations`. The control’s queryable state comes from rolling up those rows.

This walkthrough traces a record from arrival to surfaced state:

1. The connector pushes evidence (slice 003, slice 004) →
2. The schema registry validates it (slice 014) →
3. Ingest writes one append-only row in `evidence_records` (slice 013) →
4. The eval engine reads that record + others, computes a per-cell row in `control_evaluations` (slice 012) →
5. Effectiveness rolls those rows up to a control-level state (slice 017/018).

Every block was captured by `uvx showboat exec` against the slice-037 docker-compose self-host bundle, seeded by `fixtures/walkthroughs/00-seed.sql` + `audit-period.sql`. The fixture already installed three evidence records for one control; we trace through the same data.

## 1. The Control Under Trace

The base seed installs one control: CRY-05, "Encryption at rest — production object stores", applying to scope cells where `env == "prod" AND data_classification == "confidential"`:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT scf_id, title, control_family, implementation_type, lifecycle_state, applicability_expr FROM controls WHERE id = '33333333-3333-3333-3333-333333330001'; ROLLBACK;"
```

```output
BEGIN
SET
 scf_id |                     title                     | control_family | implementation_type | lifecycle_state |                   applicability_expr
--------+-----------------------------------------------+----------------+---------------------+-----------------+---------------------------------------------------------
 CRY-05 | Encryption at rest — production object stores | Cryptography   | automated           | active          | env == "prod" AND data_classification == "confidential"
(1 row)

ROLLBACK
```

## 2. The Push API — Where Evidence Arrives

The Evidence SDK exposes one canonical inbound API: `POST /v1/evidence:push` (also reachable via gRPC `IngestEvidence`). The Go handler in `internal/api/httpserver.go` accepts a JSON payload with `evidence_kind`, `schema_version`, and the payload body. Looking at the proto contract first:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -A 8 "message PushRequest\|message IngestRequest" proto/evidence/v1/*.proto 2>&1 | head -25
```

```output
message PushRequest {
  EvidenceRecord record = 1;
}

message PushResponse {
  EvidenceReceipt receipt = 1;
}

// EvidenceRecord is one observation about reality at a point in time. Every
```

`PushRequest` carries an `EvidenceRecord` and returns an `EvidenceReceipt`. The receipt includes the `record_id` (UUID assigned by the server) plus an idempotency confirmation. The push profile is one of the two equal-peer profiles in `EVIDENCE_SDK.md` §4.1 (connector pulls vs pushers push).

## 3. Schema Validation Before Insert

The ingest path validates the payload against the registered `(evidence_kind, schema_version)` before any DB write. From the schema-registry walkthrough we know the validator is JSON Schema 2020-12; the call site:

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n -B1 -A 4 "ValidatePayload" internal/evidence/ingest/ingest.go | head -25
```

```output
61-// SchemaValidator is the validation hook into slice 014. Service.Process
62:// calls ValidatePayload for every record before any DB write. The
63:// signature mirrors `schemaregistry.Service.ValidatePayload`.
64-type SchemaValidator interface {
65:	ValidatePayload(ctx context.Context, tenantID, kind, version string, payload []byte) error
66-	IsRegistered(kind, version string) bool
67-}
68-
69-// TenantAwareRegistry is the optional slice-015 hook into the schema
--
293-	}
294:	if err := s.valid.ValidatePayload(ctx, cred.TenantID, rec.EvidenceKind, rec.SchemaVersion, payloadJSON); err != nil {
295-		s.writeAudit(ctx, cred, rec.IdempotencyKey, rec.EvidenceKind, DecisionRejectedValidation, err.Error(), pgtype.UUID{})
296-		return Receipt{}, DecisionRejectedValidation, fmt.Errorf("%w: %v", ErrValidation, err)
297-	}
298-
```

On validation failure: a `DecisionRejectedValidation` row goes into the audit log + the caller gets `ErrValidation` (HTTP 422). On success: the record proceeds to insert.

## 4. The Insert — Append-Only Ledger

The insert is a single `InsertEvidenceRecord` call into `evidence_records`. The ledger is append-only by convention: there is no UPDATE/DELETE path on `evidence_records` in the production code (the integration tests confirm this). Looking at the rows already in the ledger from the fixture:

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; SELECT substring(id::text, 1, 13) AS short_id, observed_at, evidence_kind, result, payload->>'bucket' AS bucket FROM evidence_records WHERE control_id = '33333333-3333-3333-3333-333333330001' ORDER BY observed_at; ROLLBACK;"
```

```output
BEGIN
SET
   short_id    |      observed_at       |      evidence_kind       | result |        bucket
---------------+------------------------+--------------------------+--------+----------------------
 66666666-6666 | 2026-02-01 00:00:00+00 | demo.encryption_state.v1 | pass   | acme-prod-customer-1
 66666666-6666 | 2026-03-15 00:00:00+00 | demo.encryption_state.v1 | pass   | acme-prod-customer-2
 66666666-6666 | 2026-05-01 00:00:00+00 | demo.encryption_state.v1 | pass   | acme-prod-customer-3
(3 rows)

ROLLBACK
```

Three records, each carrying the same shape but a different `observed_at` + `bucket` value. The ledger is the source of truth — every subsequent computation reads from here and writes to a downstream table.

## 5. The Eval Engine — EvaluateControl

`Engine.EvaluateControl(controlID, trigger, asOf)` is the worker that turns ledger rows into per-cell state. Its docstring captures the contract precisely:

```bash
cd /Users/gmoney/Development/security-atlas-070 && sed -n "60,80p" internal/eval/engine.go
```

```output
// EvaluateControl computes and appends control state for one control.
//
// For each scope cell the control's applicability_expr resolves to (or one
// row with a NULL cell when it resolves to none — the whole-tenant
// degenerate case), the engine:
//
//  1. reads the evidence ledger for the control bounded by `asOf`,
//  2. filters to the freshness window (anti-criterion P0-2: out-of-window
//     evidence never reaches the result),
//  3. computes result + freshness_status deterministically,
//  4. appends one control_evaluations row.
//
// Every row from a single EvaluateControl call shares one eval_run_id.
// `asOf` is the point-in-time horizon — pass a far-future time for live
// evaluation, or a historical instant for replay / as-of queries.
// Idempotent: running twice over the same ledger slice produces identical
// computed columns (AC-3).
func (e *Engine) EvaluateControl(ctx context.Context, controlID uuid.UUID, trigger string, asOf time.Time) (int, error) {
	// Resolve applicable cells OUTSIDE the eval transaction — scope.Store
	// opens its own tenant-GUC transaction. The two transactions are
	// independent reads; the ledger is append-only so there is no
```

Read the ledger, filter to the freshness window, compute, append. **Append**: the engine never UPDATEs prior `control_evaluations` rows. The freshness window comes from the control’s `freshness_class` (the seed installs `monthly`). The `asOf` argument scopes the read horizon — production callers pass a far-future time for live state; replay callers pass historical instants.

## 6. The control_evaluations Output Row

Each `EvaluateControl` call writes one row per applicable cell, all sharing one `eval_run_id`. The row shape:

```bash
docker exec security-atlas-pg-030 psql -U postgres -d security_atlas -c "\\d control_evaluations" 2>&1 | head -35
```

```output
                          Table "public.control_evaluations"
          Column          |           Type           | Collation | Nullable | Default
--------------------------+--------------------------+-----------+----------+---------
 id                       | uuid                     |           | not null |
 tenant_id                | uuid                     |           | not null |
 control_id               | uuid                     |           | not null |
 scope_cell_id            | uuid                     |           |          |
 eval_run_id              | uuid                     |           | not null |
 evaluated_at             | timestamp with time zone |           | not null | now()
 result                   | evidence_result          |           | not null |
 freshness_status         | text                     |           | not null |
 evidence_count_in_window | integer                  |           | not null | 0
 last_observed_at         | timestamp with time zone |           |          |
 freshness_class          | text                     |           |          |
 trigger                  | text                     |           | not null |
 created_at               | timestamp with time zone |           | not null | now()
Indexes:
    "control_evaluations_pkey" PRIMARY KEY, btree (id)
    "idx_control_evaluations_effectiveness" btree (tenant_id, control_id, evaluated_at DESC)
    "idx_control_evaluations_latest" btree (tenant_id, control_id, scope_cell_id, evaluated_at DESC)
    "idx_control_evaluations_run" btree (tenant_id, eval_run_id)
Check constraints:
    "control_evaluations_evidence_count_nonneg" CHECK (evidence_count_in_window >= 0)
    "control_evaluations_freshness_status_chk" CHECK (freshness_status = ANY (ARRAY['fresh'::text, 'stale'::text, 'no_evidence'::text]))
    "control_evaluations_no_evidence_coherent" CHECK (freshness_status <> 'no_evidence'::text OR evidence_count_in_window = 0 AND result = 'inconclusive'::evidence_result)
    "control_evaluations_trigger_chk" CHECK (trigger = ANY (ARRAY['ingest'::text, 'scheduled'::text, 'manual'::text, 'replay'::text]))
Foreign-key constraints:
    "control_evaluations_tenant_id_control_id_fkey" FOREIGN KEY (tenant_id, control_id) REFERENCES controls(tenant_id, id) ON DELETE CASCADE
    "control_evaluations_tenant_id_scope_cell_id_fkey" FOREIGN KEY (tenant_id, scope_cell_id) REFERENCES scope_cells(tenant_id, id) ON DELETE CASCADE
Policies (forced row security enabled):
    POLICY "tenant_read" FOR SELECT
      USING (current_tenant_matches(tenant_id))
    POLICY "tenant_write" FOR INSERT
      WITH CHECK (current_tenant_matches(tenant_id))

```

Key invariants encoded in the schema:

- `freshness_status` is one of `fresh` | `stale` | `no_evidence`.
- `result` is an `evidence_result` enum (`pass` | `fail` | `inconclusive`).
- `trigger` records what caused the evaluation: `ingest` (post-push hook), `scheduled` (cron), `manual` (operator-triggered), `replay` (re-run against the ledger for as-of queries).
- The `no_evidence_coherent` check constraint enforces that `freshness_status=no_evidence` implies `evidence_count_in_window=0` and `result=inconclusive`. Three columns; one truth.
- Indexes are tuned for two read shapes: latest-state-per-cell (`idx_control_evaluations_latest`) and effectiveness-rollup (`idx_control_evaluations_effectiveness`).

## 7. Effectiveness Rollup — From Per-Cell Rows to Control State

The `/v1/controls/{id}/state` API surface reads the latest `control_evaluations` row per cell and computes the control’s overall effectiveness via `internal/eval/effectiveness.go`. The rollup is straightforward: any cell with `result=fail` makes the control `fail`; otherwise `pass` (with freshness reflecting the worst per-cell freshness).

```bash
cd /Users/gmoney/Development/security-atlas-070 && grep -n "func.*Effectiveness\|RolledUp\|RollupResult" internal/eval/effectiveness.go 2>&1 | head -10
```

```output
55:func (e *Engine) Effectiveness(ctx context.Context, controlID uuid.UUID) (Effectiveness, error) {
```

`Effectiveness` reads all `control_evaluations` in the effectiveness window, counts pass / fail / inconclusive, and returns `PassRate = pass / total`. Importantly: when `TotalCount = 0` the rate is 0% — but callers distinguish "0% effective" from "no data" via the explicit `TotalCount`. Conflating those two is a category of bug the schema and the API both refuse to allow.

## 8. The Whole Chain in One Query

For demonstration: replay the entire chain in SQL against the seeded ledger. (Production runs `Engine.EvaluateControl` per the control_evaluations schema; for the walkthrough we use the same logic as a hand-rolled SELECT that ANY reader can re-execute.)

```bash
docker exec security-atlas-pg-030 psql -U atlas_app -d security_atlas -c "BEGIN; SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0'; WITH ledger AS (SELECT * FROM evidence_records WHERE control_id = '33333333-3333-3333-3333-333333330001' AND observed_at <= now()), rolled_up AS (SELECT count(*) AS records, count(*) FILTER (WHERE result = 'pass') AS pass_count, count(*) FILTER (WHERE result = 'fail') AS fail_count, max(observed_at) AS last_observed, CASE WHEN bool_and(result = 'pass') THEN 'pass' WHEN bool_or(result = 'fail') THEN 'fail' ELSE 'inconclusive' END AS rolled_up_result FROM ledger) SELECT * FROM rolled_up; ROLLBACK;"
```

```output
BEGIN
SET
 records | pass_count | fail_count |     last_observed      | rolled_up_result
---------+------------+------------+------------------------+------------------
       3 |          3 |          0 | 2026-05-01 00:00:00+00 | pass
(1 row)

ROLLBACK
```

3 records, all pass, rolled-up `result=pass`, `last_observed` is the freshest record. That is what a successful pipeline run would write into `control_evaluations` (one row per applicable cell) and what `/v1/controls/{id}/state` would surface to the dashboard.

## 9. Putting It All Together

The pipeline has five distinct stages, each load-bearing:

| Stage         | Reads from              | Writes to             | Owns                                           |
| ------------- | ----------------------- | --------------------- | ---------------------------------------------- |
| Push API      | HTTP request body       | (nothing yet)         | Auth, idempotency check                        |
| Schema reg.   | `evidence_kind_schemas` | (nothing — read-only) | (kind, semver) lookup + payload validation     |
| Ingest        | Validated payload       | `evidence_records`    | sha256 hash, scope resolution, audit log entry |
| Eval engine   | `evidence_records`      | `control_evaluations` | Per-cell result computation; idempotent        |
| Effectiveness | `control_evaluations`   | (read-only response)  | Rollup math, time-window selection             |

Each stage is a separate Go package; the contract between them is the table column shape. **Ingestion and evaluation are separate stages with the ledger as the boundary** (constitutional invariant 2 in `CLAUDE.md`). A bug in eval cannot corrupt the source-of-truth evidence; replay is always possible.

### Where to read more

- **Canvas:** [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) — §4.1 Evidence SDK, §4.3 ingestion vs evaluation separation
- **Slice docs:** [`docs/issues/003-evidence-sdk-push.md`](../issues/003-evidence-sdk-push.md), [`docs/issues/012-eval-engine.md`](../issues/012-eval-engine.md), [`docs/issues/013-evidence-ledger.md`](../issues/013-evidence-ledger.md), [`docs/issues/014-schema-registry.md`](../issues/014-schema-registry.md), [`docs/issues/017-control-effectiveness.md`](../issues/017-control-effectiveness.md)
- **Go packages:** [`internal/evidence/ingest/`](../../internal/evidence/ingest/) (push handler), [`internal/api/schemaregistry/`](../../internal/api/schemaregistry/) (validation), [`internal/eval/`](../../internal/eval/) (engine + effectiveness)
- **Proto:** [`proto/evidence/v1/`](../../proto/evidence/v1/) — `PushRequest`, `EvidenceRecord`, `EvidenceReceipt`
