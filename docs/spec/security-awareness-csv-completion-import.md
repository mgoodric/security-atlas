# Security-awareness training: CSV completion-import contract

Bulk import of training completions (and optional phishing-simulation
results) against **existing** assignments, for compliance teams whose LMS
hands them a CSV export (OPENENGINE-659, follow-up to OPENENGINE-626).
This is the practical bridge until an LMS connector exists: matched rows
complete their assignment with `completion_source = 'csv'` and carry the
same `security_awareness.training_completion.v1` evidence record the
manual completion path builds.

Entry point: `securityawareness.(*Store).ImportCompletionsCSV`
(`internal/securityawareness/csvimport.go`). The importer does not create
people, courses, campaigns, or assignments — rows that cannot be resolved
to an existing assignment are reported per row and skipped.

## File shape

RFC 4180 CSV, UTF-8 (a leading BOM is tolerated). The first row is the
header; column names match case-insensitively and unknown columns are
ignored, so raw LMS exports can carry extra fields. Parser caps (same
posture as `connectors/manual`): 100,000 data rows, 1 MiB per field;
exceeding either aborts the whole import before anything is written.

## Columns

| Column                   | Required                | Meaning                                                                                                                                            |
| ------------------------ | ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| `work_email`             | one person key required | Person key, matched case-insensitively against `security_training_people.work_email` (tenant-unique at the DB layer, so never ambiguous)           |
| `source_person_id`       | one person key required | Preferred person key when present; matched against `security_training_people.source_person_id`                                                     |
| `person_source`          | no                      | Narrows `source_person_id` to `manual` / `hris` / `scim`. Without it, a `source_person_id` matching people across sources is reported as ambiguous |
| `course_code`            | yes                     | Matched case-insensitively against `security_training_courses.code`                                                                                |
| `campaign_name`          | yes                     | Matched case-insensitively against `security_training_campaigns.name` within the course                                                            |
| `completed_at`           | yes                     | RFC 3339 timestamp, or `YYYY-MM-DD` (interpreted as UTC midnight)                                                                                  |
| `phishing_simulation_id` | no                      | Presence enables the phishing column group for the row                                                                                             |
| `phishing_sent_at`       | with simulation id      | RFC 3339 or `YYYY-MM-DD`                                                                                                                           |
| `phishing_outcome`       | with simulation id      | One of `no_click`, `clicked`, `reported`, `credential_submitted`                                                                                   |
| `phishing_clicked_at`    | no                      | RFC 3339 or `YYYY-MM-DD`                                                                                                                           |
| `phishing_reported_at`   | no                      | RFC 3339 or `YYYY-MM-DD`                                                                                                                           |

When both person keys are present, `source_person_id` wins. Phishing
columns without `phishing_simulation_id` are a per-row error, not
silently dropped.

## Semantics

- **One transaction per import.** Row-level failures (unresolvable
  person/assignment, validation errors, conflicts) are collected in the
  per-row report and never abort the batch; an infrastructure error rolls
  the entire batch back. The batch never partially corrupts state.
- **Idempotent re-import.** A row whose assignment is already completed
  at the same instant reports `already_complete` and rewrites nothing
  (phishing results still upsert, deduplicated on
  `(assignment, simulation_id)`).
- **No overwrites.** A row whose assignment is completed at a different
  instant — for example, a manual completion recorded in the UI — is a
  per-row `error` (`refusing to overwrite`). Reconciling divergent
  records is a human decision, not an import side effect.
- **Evidence parity with manual completion.** Every newly imported row
  carries a `BuildCompletionEvidence` record in its `RowResult`, exactly
  what the manual `Complete` path produces, with
  `completion_source: csv` in the payload. The evidence idempotency key
  is derived from assignment id + completion date, so re-pushing after a
  re-import deduplicates at the ledger.
- **Tenant isolation.** All resolution queries run under the calling
  tenant's RLS context; a row can never resolve to — or complete —
  another tenant's assignment.

## Example

```csv
work_email,source_person_id,person_source,course_code,campaign_name,completed_at,phishing_simulation_id,phishing_sent_at,phishing_outcome,phishing_reported_at
alice@example.com,,,SAT-2026,2026 annual,2026-07-10T09:00:00Z,sim-2026-07,2026-07-01,reported,2026-07-02T08:00:00Z
,rippling:worker-2,hris,SAT-2026,2026 annual,2026-07-11,,,,
```

## Report

`ImportReport` carries `Imported` / `AlreadyComplete` / `Failed` counts
plus one `RowResult` per data row: 1-based row number, status
(`imported`, `already_complete`, `error`), resolved assignment id, the
error message for failed rows, and the evidence record for imported
rows.

## Related

- Model + `completion_source` CHECK constraints:
  `migrations/sql/20260730000000_security_awareness_training.sql`
- LMS-connector direction (decision note):
  `docs/audit-log/659-csv-completion-import-decisions.md`
