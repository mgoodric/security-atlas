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
the risk id. `payload_json` holds the row `id` and `changed_fields`, the
names of the fields whose value differs between the before and after
states. It holds no field values (D3).

### D2 — the timestamp is `created_at`

`risk_audit_log` names its timestamp `created_at`, not `occurred_at`. The
UNION branch aliases it and strips it from the payload. No migration
renames the column, because a rename would touch the append-only table
and its readers (`ListRiskAuditLog`, `/v1/risks/{id}/audit-log`) for no
behavioural gain.

### D3 — the unified row names the changed fields, never their values

The rows are tenant-scoped by the table's existing `tenant_read` RLS
policy, which the unified query runs under. Tenant isolation is asserted
in `TestUnifiedAuditLog_ShowsRiskRows_1954`.

The first version of this PR projected `before_state` and `after_state`
into `payload_json` and claimed the same callers could already read those
values on `GET /v1/risks/{id}`. That was wrong, and the PR review caught
it. The unified log and its export are open to auditors, and
`/v1/activity/unified` to control owners. OPA denies both roles
`/v1/risks` (`policies/authz/auditor.rego`). The states hold titles,
descriptions, scores and the treatment owner, so projecting them
leaked risk content to roles that may not read risks.

The branch now builds `payload_json` as `{id, changed_fields}`.
`changed_fields` is the sorted list of keys in `after_state` whose value
differs from `before_state`: `[level]` for a level-only move,
`[category, category_id]` for a category change, and the edited field
names for `fields_changed`. A reader learns that a risk changed and what
kind of change it was, which is what an audit trail across modules needs.
The values stay on `GET /v1/risks/{id}/audit-log`, behind the risk read
policy. A `CASE` keeps `jsonb_object_keys` off a non-object state, so one
malformed row cannot fail the whole unified query.

`TestUnifiedAuditLog_RiskRowsCarryNoFieldValues_1954` covers four
readers: an auditor on the unified log, an auditor on the JSON and the
CSV export, and a control owner on Activity. Each is a real user row
with a `user_roles` grant, because the unified log's role probe reads
`user_roles`. The test PATCHes distinctive title, description and
treatment-owner values and asserts that each response contains the risk
row but none of those values, `before_state` or `after_state`. It fails
on all four against the first version's projection. Test servers don't
wire OPA, so the `/v1/risks` denial itself is not asserted there.

`risk` stays visible to non-privileged Activity callers, as `taxonomy`
and `aggregation_rule` are. The only identity on a row is `actor`, the
credential id, as on every other kind.

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
