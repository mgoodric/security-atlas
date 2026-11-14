# framework-scope workflow log — decisions log (#1786)

Prefactor for the Statement of Applicability (#1744). Adds
`framework_scope_workflow_log`, an append-only, tenant-scoped history of every
framework-scope predicate change and state transition.

detection_tier_actual: none
detection_tier_target: none

## Decisions

**D1 — Written by the store, in the change's own transaction.**
`internal/frameworkscope.Store` inserts the log row on the same tx as the
`framework_scopes` write. A rolled-back transition leaves no log row, and a
log-write failure fails the transition. A DB trigger was the alternative. It
would catch writes that bypass the store, but there is no actor GUC
(`app.current_user` does not exist at v1), so a trigger could not record who
made the change.

**D2 — Actor is the calling credential id (`cred.ID`).** This matches
`exception_audit_log`. Approve also records `cred.UserID` on the scope row
as `approver_user_id`, unchanged.

**D3 — One action per store operation.** The actions are `create`,
`predicate.update`, `submit`, `approve`, `activate` and `supersede`. A reset
to draft is not a separate action: it is the `predicate.update` row whose
`from_state` is `review`/`approved` and whose `to_state` is `draft`. That
mirrors the BEFORE UPDATE trigger that performs the reset. Activating a
successor writes two rows in one tx: `supersede` on the predecessor, then
`activate` on the successor. SoA tickets extend the `action` CHECK for
applicability changes.

**D4 — before/after are full row snapshots as JSON**, with the predicate
embedded as JSON rather than base64. `before` is NULL on `create`.

**D5 — Append-only by grant and policy.** `atlas_app` gets SELECT + INSERT
only, and RLS has `tenant_read` + `tenant_write` with no UPDATE/DELETE policy
(the `me_audit_log` shape). There is no FK to `framework_scopes`, so the
history outlives the row (same as `exception_audit_log`).

**D6 — Surfaced through the unified admin audit log** (`/v1/admin/audit-log/unified`,
the `/audit-log` page) as a tenth kind, `framework_scope`. The legacy
`admin_audit_log_v` view (`/v1/admin/audit-log`) is not extended. No web
page reads it, and its branch list has been stale since slice 124.
`framework_scope` rows are tenant-public in the non-privileged Activity
feed, as exception rows are.
