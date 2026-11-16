# 1954 — risk_audit_log in the unified audit log: decisions log

Issue: mgoodric/security-atlas#1954 (from the #1926 review of #1749).
`risk_audit_log` rows now appear in the unified audit log, a risk PATCH
with no credential is refused instead of being recorded as `system`, and
placement moves have regression tests against the aggregation engine.

## Detection-tier classification

- **detection_tier_actual:** integration.
  `TestPatchRisk_NoCredentialWritesNoRow_1954` fails against the old
  `actorFrom` fallback: the PATCH answers 200 and moves the risk with
  actor `system`. It passes with the fix.
- **detection_tier_target:** unit. `TestPatchRisk_NoCredentialIs401`
  now covers the same refusal without a database.

## Decisions

### D1 — one kind, `risk`, for every risk_audit_log row

The ticket suggested `risk_placement`. Since then the table has gained
`category_changed` (#1812) and `fields_changed` (#2104) rows, so a
placement-only name would mislabel two of its three actions. The kind is
`risk` and the row's `action_type` becomes the entry's `action`
(`placement_changed`, `category_changed`, `fields_changed`). That way the
kind names the source table, as every other kind does, and the action
tells the three changes apart. `target_type` is `risk` and `target_id` is
the risk id. `payload_json` holds `id`, `before_state` and `after_state`,
following the pattern the other branches use.

### D2 — the timestamp is `created_at`

`risk_audit_log` names its timestamp `created_at`, not `occurred_at`. The
UNION branch aliases it and strips it from the payload. No migration
renames the column, because a rename would touch the append-only table
and its readers (`ListRiskAuditLog`, `/v1/risks/{id}/audit-log`) for no
behavioural gain.

### D3 — visibility follows the other tenant-public kinds

The rows are tenant-scoped by the table's existing `tenant_read` RLS
policy, which the unified query runs under. Tenant isolation is asserted
in `TestUnifiedAuditLog_ShowsRiskRows_1954`. For non-privileged callers on
`/v1/activity/unified`, `risk` is treated like `taxonomy` and
`aggregation_rule`: visible, not hidden like `feature_flag`. The only
identity on a row is `actor`, the credential id, which every other kind
already exposes and the `actor_name` join resolves the same way. The
before/after states hold risk fields that `GET /v1/risks/{id}` already
returns to the same callers, so no new identity data reaches them.

### D4 — module switches: deferred to #2173, consistent with `exception`

With Risk switched off (#2171), the unified log and Activity still show
`risk` rows, just as they still show `exception` rows with Exceptions off.
Filtering combined views by module is #2173's scope ("Activity" is in its
list, and #2352 asks it to name the unified log too). Adding a
module-aware predicate for one kind here would set a second mechanism
before #2173 picks one.

### D5 — a missing credential is a 401; no writer falls back to `system`

`actorFrom` in `internal/api/risks` now writes a 401 (`credential missing`)
and returns false when the request carries no credential or one with an
empty id. It is checked right after the tenant context, before the body is
parsed, so nothing is written. Writers of `risk_audit_log`:

| Writer                                                | Actor                                       |
| ----------------------------------------------------- | ------------------------------------------- |
| `PATCH /v1/risks/{id}` (`risk.Store.UpdatePlacement`) | the caller's credential id; 401 without one |

There is no background or system writer of `risk_audit_log`. The
aggregation engine refreshes meta-risks through `UpdateMetaRiskInherentScore`
and never writes this table. The category and theme admin handlers in the
same package call `actorFrom` too and get the same 401. They were
already unreachable without a credential, because `requireTaxonomyAdmin`
refuses first, so the change does not alter what they do.

The aggregation-rules and mapping-overlay handlers have their own
`actorFrom` with the same `system` fallback. They are outside this
ticket, which is about risk_audit_log. The fallback there is raised in
the PR for a follow-up ticket.

### D6 — regression tests live in `internal/risk/aggrule`

The three placement tests drive `risk.Store.UpdatePlacement` with the
store wired as `httpserver.go` wires it (`riskStoreAsWired`), next to the
#1867/#1880 lock-order tests whose helpers they reuse (`openTenantTx`,
`waitBlockedBy`). In the concurrent test the move is observed waiting on
the evaluation's rule lock through `pg_blocking_pids`. The interleaving is
therefore fixed, not left to timing.
