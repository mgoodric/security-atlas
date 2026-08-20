# OE-628 — access-review / recertification campaigns: decisions log

**Type:** JUDGMENT
**Status at build:** OPENENGINE-628 filed from the 2026-07-30 program-completeness
review; first fire landed the store + migration + tests and opened PR #1545
(all checks green); this fire resolved the PR's merge conflict with `main`
and fixed the evidence-emission defect recorded as D1.

- detection_tier_actual: manual_review
- detection_tier_target: unit

The calls below were made by the implementing engineer and recorded here
rather than blocking the merge on a human sign-off; the maintainer iterates
post-deployment.

## What this slice does (one paragraph)

An operational access-certification workflow: a tenant-scoped campaign
snapshots entitlement review items from SCIM (`users` ⨝ `scim_group_members`
⨝ `scim_groups`, manual-CSV fallback), assigns reviewers round-robin,
records keep/revoke attestations with mandatory reasons, tracks completion,
exports the revoke list as CSV (the operator's enforcement handoff — no code
path revokes access), emits `access_review.completion.v1` evidence mapped to
SOC 2 CC6.3 on completion, and creates due reminders in the `notifications`
table. Three new tables, all with the four-policy FORCE RLS shape
(invariant #6).

## Decisions made

### D1 — Evidence kind aligned to the registered schema (correctness)

The first fire emitted `access_review.recertification.v1` with the raw
`Rollup` struct as payload. That kind is registered nowhere: the schema
registry ships `access_review.completion.v1` (schema
`internal/api/schemaregistry/schemas/access_review.completion/1.0.0.json`,
`additionalProperties: false`), and the CC6.3 bundle's evidence query
(`controls/soc2/soc2_cc6_3_periodic_access_review/control.yaml`) is keyed on
that same kind — so the completion evidence could never satisfy the control
it exists to certify. Caught in manual review after CI passed; should have
been a unit-tier catch, hence `detection_tier_target: unit` and the new
`TestCompletionEvidenceMatchesRegisteredSchema`, which pins the kind, the
semver, AND payload shape to the embedded registry schema.

**Decision:** emit `access_review.completion.v1` / `1.0.0` with a
schema-conformant payload. Field mapping: `review_id` = campaign UUID;
`completed_by` = campaign `created_by`; `reviewer_role` =
`"campaign_reviewer"` (reviewers are per-campaign assignments, not role
holders); `users_reviewed` = distinct principals across items;
`users_terminated` = distinct principals with ≥1 revoke DECISION (decision
recorded, enforcement is the operator's); `users_role_changed` = 0 (not
tracked by this workflow); `notes` carries the item/decision rollup.

### D2 — Best-effort control_id resolution (eval reachability)

The eval engine loads evidence via `control_id = $2 OR control_ref =
<control-uuid-string>` — a bundle-slug `control_ref` alone is invisible to
it. **Decision:** at `Complete` time, resolve the tenant's CC6.3 control by
`bundle_id = 'soc2_cc6_3_periodic_access_review' AND superseded_by IS NULL`
and set `control_id` when the SOC 2 kit is imported; otherwise leave it NULL
with the slug in `control_ref` for traceability. No hard dependency on the
kit being present.

### D3 — Direct ledger INSERT, not `ingest.Service.Process` (emission path)

Reference emitters (policyacks, controls/attest) route through
`ingest.Service.Process`, which needs a `credstore.Credential` — an
HTTP-layer construct this store-level module does not yet have (no HTTP
surface, see D5). The merged sibling `internal/securityawareness` (OE-626)
builds proto records for a future ingest caller and lands no evidence today.
**Decision:** keep the single-transaction direct INSERT (idempotent via
`access-review:<campaign>:completion` key, atomic with the campaign-status
flip). When the HTTP surface lands (child OE), the handler can orchestrate
through `ingest.Service` the way policyacks does; the schema-conformant
payload from D1 makes that migration a mechanical swap.

### D4 — Reminder runtime wiring deferred to a child OE (scope)

`FireDueReminders` is idempotent per (reviewer, campaign, UTC day) and
integration-tested, but has no production caller — it needs a
tenant-enumerating sweeper (the `internal/metrics/scheduler` shape: migrator
pool enumerates, app pool executes per-tenant under RLS). The program's
established decomposition (OE-630 store → OE-661 runtime wiring → OE-663
API → OE-664 UI for personnel-security) puts runtime wiring in its own
slice. **Decision:** file a child OE for the sweeper + compliance-calendar
UNION branch rather than growing this green PR.

### D5 — Store-first, no HTTP/UI surface in this slice (scope)

Same precedent as D4: the store is the tracer bullet; `internal/api`
registration, the attestation UI, and the revoke-CSV download endpoint are a
child OE. `WriteRevokeCSV(w io.Writer, ...)` is already handler-shaped.

### D6 — Merge-conflict resolution (mechanics)

PR #1545 went DIRTY after five subsequent merges to `main`. Conflicts were
mechanical: `sqlc.yaml` (schema-list union, filename order),
`scripts/integration-shards.txt` and `internal/db/dbx/models.go`
(auto-merged; `just sqlc-generate` against the merged schema list produced
zero drift, confirming the generated code is canonical). The migration keeps
its `20260612110000` timestamp: `migrate.sh` applies any file absent from
the `schema_migrations` ledger by basename, so out-of-order insertion is
safe for existing deployments, and same-timestamp files already exist on
`main`.
