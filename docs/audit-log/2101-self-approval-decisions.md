# #2101 — Approval rule and the self-approval opt-in: decisions log

**Ticket:** [#2101](https://github.com/mgoodric/security-atlas/issues/2101). Approver differs from requester; an organisation-level self-approval opt-in; a marked flag; the ADR.
**ADR:** [0024](../adr/0024-approvals-require-a-second-person.md).

- detection_tier_actual: integration
- detection_tier_target: integration

> One bug surfaced during the build, at the integration tier. A JWT whose
> subject names no person (an OAuth client, the test harness's
> `test-viewer:` subjects) has a per-token credential id `jwt:<jti>`. The
> policy handler recorded that id as `submitted_by`, and the shared rule
> reads the `jwt:` prefix as a pre-#1807 session submitter, so approval of
> anything such a caller submitted was refused, even for a different
> person. The framework-scope HTTP tests caught it. Fixed by recording a
> non-person by its principal (`credstore.PrincipalOf`) in both handlers.

## D1 — One rule, one package

`internal/approval.Check(requester, approver, allowSelf)` decides. Each module maps its three errors (not a person / self-approval / requester unknown) onto its own error values, so the HTTP bodies stay module-specific and the existing #1807 policy errors are unchanged. Policy's `checkApprover` now delegates; it was the "extend this function" hook #1807 left.

## D2 — The exceptions CHECK: a denormalised flag, not a trigger

The ADR records the trade-off. Briefly: `exceptions_sod` becomes `approved_by IS NULL OR approved_by <> requested_by OR self_approved`. The store reads `tenants.allow_self_approval` in the approval transaction and sets the flag only when the rule allowed a self-approval. A trigger reading `tenants` was rejected: it would put half the rule in PL/pgSQL. `exceptions_denied_by_sod` is unchanged; the opt-in covers approval, not denial.

## D3 — Legacy exception requesters keep their original rule

Exceptions recorded `requested_by` as the credential id. For a JWT caller that is `jwt:<jti>`. Policies and scopes refuse a session-recorded submitter until it is re-submitted. An exception has no re-request path, so refusing would strand every in-flight request. A `jwt:` requester is therefore compared against the approving credential id, which is exactly the pre-#2101 behaviour, and then only the "only people approve" half of the rule applies. New requests record the person.

## D4 — Where "the audit log shows it"

Each module already had its own audit trail, and each gets the marker in the form that trail already uses:

| Module          | Audit trail                    | Marker                                                                                |
| --------------- | ------------------------------ | ------------------------------------------------------------------------------------- |
| Policy          | `policy_audit_log`             | a row with action `self_approved` (new CHECK value; existing values kept)             |
| Framework scope | `framework_scope_workflow_log` | `self_approved` (and `submitted_by`) in the `approve` row's after-image               |
| Exception       | `exception_audit_log`          | the `approved` row's reason is `self-approved: the organisation allows self-approval` |
| Setting change  | `me_audit_log`                 | action `tenant_self_approval_update`, before/after values                             |

Ordinary policy approvals still write no `policy_audit_log` row; adding one for every approval is outside this ticket.

## D5 — Framework scopes record a submitter

`framework_scopes` had no submitter column; the workflow log held only the credential id. The migration adds `submitted_by` and backfills it for rows already in `review` from the log's latest `submit` row. The backfill writes credential ids, so a backfilled `jwt:` submitter is refused until it is re-submitted. The submit query now accepts a re-submit of such a row, as the policy query already did. The workflow-log `actor` stays the credential id, as before.

## D6 — Exception approval is people-only

Following Matt's #1807 decision ("only people may approve") across modules, an approver-role machine credential (unbound key, OAuth client, bootstrap admin) can no longer approve an exception. Integration tests that approved with `key_approver` now approve as a person bound to that key.

## D7 — Exports and UI

The policy and exception exports gain a `self_approved` column. Policy appends it at the end; exception places it after `approved_at`, where the approval columns sit. The framework-scope SoA export describes decisions, not the scope's approval, so it is unchanged. The UI shows a "Self-approved" badge on the policy detail header, on the framework-scope row and on the exceptions list. A settings-tab toggle for the setting is not in this change. The tenant settings tab has no read path for tenant settings yet: gate mode is not read back, and the cadence has its own endpoint. Admins set it with `PATCH /v1/tenants/{id}` for now.

## D8 — The framework-scope e2e

`framework-scope-soa.spec.ts` submits and approves as the one e2e user. The spec now turns Allow self-approval on for the demo tenant in `beforeAll` and off in `afterAll`. Its fixture inserts the demo tenant's `tenants` row, as `controls-top-bar.sql` does, so the PATCH has a row to update. The spec also asserts the Self-approved marker.
