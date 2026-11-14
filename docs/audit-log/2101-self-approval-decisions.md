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

## D2 — The database legs: a denormalised flag, and a trigger for the setting

The ADR records the trade-off. Briefly: `exceptions_sod` becomes `approved_by IS NULL OR approved_by <> requested_by OR self_approved`. The store reads `tenants.allow_self_approval` in the approval transaction and sets the flag only when the rule allowed a self-approval. `exceptions_self_approved_has_approver` keeps the flag off an unapproved row. `exceptions_denied_by_sod` is unchanged; the opt-in covers approval, not denial.

The first cut stopped there. Review (the #1965 precedent) required the setting to hold in the database too. `self_approval_requires_setting` is a `BEFORE INSERT OR UPDATE` trigger on policies, framework scopes and exceptions. It refuses a row that newly becomes `self_approved` while its tenant's setting is off, and it checks only the setting. The person rule stays in Go, so the trigger does not duplicate credential-shape logic. A row already flagged is not re-checked, so turning the setting off later does not block unrelated updates to it. The lookup runs as the writing role; under RLS an invisible tenants row reads as off, which fails closed.

## D3 — Legacy exception requesters keep their original rule

Exceptions recorded `requested_by` as the credential id. For a JWT caller that is `jwt:<jti>`. Policies and scopes refuse a session-recorded submitter until it is re-submitted. An exception has no re-request path, so refusing would strand every in-flight request. A `jwt:` requester is therefore compared against the approving credential id, which is exactly the pre-#2101 behaviour, and then only the "only people approve" half of the rule applies. New requests record the person.

Denial gets the same two-way comparison (found in review). `Deny` takes a `Denier{Principal, CredentialID}` and refuses when `requested_by` equals either. Comparing the principal alone would have let a person whose legacy request was recorded as `key_…` deny it with that key, because the key now resolves to their user id.

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

`framework_scopes` had no submitter column; the workflow log held only the credential id. The migration adds `submitted_by` and backfills it for rows already in `review` from the log's latest `submit` row. A `key_<uuid>` actor whose key is bound to a user (`api_keys.issued_by`) is mapped to that user (found in review); any other actor is kept verbatim. A backfilled `jwt:` submitter, or a review row the backfill could not fill (NULL), is refused until it is re-submitted. Unlike policies, a NULL scope submitter is not waved through, because the column is new and NULL means unknown. The submit query accepts a re-submit of either. The backfill is a named, idempotent function, `framework_scopes_backfill_submitted_by()`, so its mapping is under test. The workflow-log `actor` stays the credential id, as before.

## D6 — Exception approval is people-only

Following Matt's #1807 decision ("only people may approve") across modules, an approver-role machine credential (unbound key, OAuth client, bootstrap admin) can no longer approve an exception. Integration tests that approved with `key_approver` now approve as a person bound to that key.

## D7 — Exports and UI

The policy and exception exports gain a `self_approved` column. Policy appends it at the end; exception places it after `approved_at`, where the approval columns sit. The framework-scope SoA export describes decisions, not the scope's approval, so it is unchanged. The UI shows a "Self-approved" badge on the policy detail header, on the framework-scope row and on the exceptions list. A settings-tab toggle for the setting is not in this change. The tenant settings tab has no read path for tenant settings yet: gate mode is not read back, and the cadence has its own endpoint. Admins set it with `PATCH /v1/tenants/{id}` for now.

## D8 — The framework-scope e2e

`framework-scope-soa.spec.ts` and `framework-scope-diff.spec.ts` each submit and approve as the one e2e user. Their fixtures upsert the demo tenant's `tenants` row, as `controls-top-bar.sql` does, with Allow self-approval on, and leave it on. A per-spec toggle (on in `beforeAll`, off in `afterAll`) was rejected. The two specs run in parallel workers, so one spec's `afterAll` could turn the setting off while the other was approving. `framework-scope-soa.spec.ts` also asserts the Self-approved marker.
