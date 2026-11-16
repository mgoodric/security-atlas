# ADR 0024 — Approvals Require a Second Person; Self-Approval Is an Explicit, Organisation-Level, Visibly Marked Opt-In

**Status:** Accepted

**Date:** 2026-11-14

**Decides:** who may approve a record that needs approval (a policy, a framework scope, an exception, and a risk acceptance once its transitions exist), and how an organisation of one runs those lifecycles at all.

**Ticket:** [#2101](https://github.com/mgoodric/security-atlas/issues/2101), under [#2071](https://github.com/mgoodric/security-atlas/issues/2071) (finish the record lifecycles). Builds on [#1807](https://github.com/mgoodric/security-atlas/issues/1807) / #2180, which added segregation of duties to policy approval and decided that only people approve.

---

## Context

Approval is the moment a second person stands behind a record. An auditor reading "approved by" on a policy or an exception assumes that person is not the one who asked. Before this change the rule was applied unevenly:

- **Policies** refused the submitter (#1807) and refused machine credentials.
- **Framework scopes** had no check: the person who submitted a scope could approve it.
- **Exceptions** had a database CHECK, `approved_by <> requested_by`, but compared raw credential ids. A person with two sessions has two `jwt:<token id>` credential ids, so they could request from one session and approve from the other.

The primary user is a solo security leader (canvas §1). For that user a strict two-person rule makes the policy, scope and exception lifecycles impossible to complete. The rule has to be the default, and an organisation of one still needs a way through.

## Decision

1. **One rule, in one place.** `internal/approval.Check` decides every approval. Only a **person** approves: the users(id) behind the credential (`credstore.CallerUserID`). An unbound API key, an OAuth client or the bootstrap admin never approves. The approver must not be the requester, compared by person rather than by session. Policies, framework scopes and exceptions call `Check`. Risk acceptance adopts it when its transitions are built.
2. **Requesters are recorded by person.** `submitted_by` on policies and framework scopes, and `requested_by` on exceptions, hold the users(id) for a person. A machine is recorded by its principal (`credstore.PrincipalOf`), never by its per-token JWT id. A requester recorded as a session id (`jwt:<token id>`) before this change cannot be compared. Policies and framework scopes refuse to approve it until it is re-submitted; so does a framework scope in review with no recorded submitter. The migration backfills framework scopes already in review from the workflow log, mapping a submit made with a key bound to a user (`api_keys.issued_by`) to that user. Exceptions cannot be re-requested, so such a row keeps its original rule: the approving credential must differ.
3. **Self-approval is an organisation setting, off by default.** `tenants.allow_self_approval` ("Allow self-approval") lets the requester approve their own record. Only an admin can change it (`PATCH /v1/tenants/{id}`), and every change writes a `me_audit_log` row (`tenant_self_approval_update`) with the before and after values.
4. **The opt-in relaxes only "same person".** A machine credential still never approves, whatever the setting.
5. **A self-approval is visibly marked.** The record carries `self_approved = true`. That flag appears on the API wire, in the policy and exception CSV/xlsx exports, and as a "Self-approved" marker in the UI. The audit log names it too: a `policy_audit_log` row with action `self_approved`, the `self_approved` field in the framework-scope workflow log's after-image, and the reason on an exception's `approved` audit row.
6. **The database enforces the setting as well as the application.** Two legs, so that a write which skips the application rule cannot produce an approval the organisation did not allow:

   - **The exceptions CHECK honours the setting through the flag.** A CHECK cannot read `tenants`, so the setting is denormalised onto the row as `self_approved`. The CHECK becomes `approved_by IS NULL OR approved_by <> requested_by OR self_approved`, and a second CHECK keeps the flag off a row nobody approved. Without the flag, a same-person approval is refused.
   - **A trigger guards the flag itself.** `self_approval_requires_setting` is a `BEFORE INSERT OR UPDATE` trigger on policies, framework scopes and exceptions. A row may newly become `self_approved` only while its tenant's `allow_self_approval` is on; a missing or invisible tenants row reads as off. The trigger checks the setting alone. Who may approve (a person; not the requester unless self-approval is allowed) stays in Go, in one place.

   The application reads `allow_self_approval` in the same transaction as the approval and sets `self_approved` only when it allowed a self-approval.

7. **Denial is never self-service.** The opt-in covers approval only. The requester cannot deny their own exception, compared by principal and by credential id, so a request recorded under a credential id before this change is still matched.

## Alternatives considered

- **The whole rule in a trigger.** This would make the database the only judge. But it duplicates the "person" logic (credential shapes, bound keys, legacy session ids) in PL/pgSQL, beside the Go that the API needs anyway for its error messages. Rejected. The trigger checks only the organisation setting, which is a single column lookup. That is the #1965 precedent: the database refuses what the setting forbids, and the application owns the rest.
- **The flag alone, no trigger.** This was the first cut of this ADR. The CHECK stops an unmarked self-approval, but a direct write could still set the flag while the setting is off. Rejected in review: the setting must hold in the database too.
- **Per-record override ("approve my own, just this once").** This puts the decision in the hands of the person it constrains. Rejected: the opt-in is an organisation-level policy decision, set by an admin and audited.
- **No opt-in; require a second person always.** This is the safest rule. It blocks the solo operator, the primary v1 user, from completing any approval. Rejected; the marker keeps the opt-in honest instead.
- **Silent self-approval when the setting is on.** Rejected. An auditor must be able to tell a self-approval from a second person's approval without reading the tenant's settings history.

## Consequences

- Turning Allow self-approval on does not rewrite history. Only approvals made while it is on are flagged. Turning it off again stops new self-approvals and leaves earlier flags in place.
- Exception approval is now people-only. Before, any approver-role credential could approve. An approver-role unbound API key, OAuth client or bootstrap admin now gets 403.
- Approval-reset paths clear the flag together with the approver. A framework-scope predicate or applicability edit bounces the scope to draft and resets `self_approved`.
- New approval surfaces call `approval.Check` and `approval.AllowSelf` rather than re-implementing the comparison. A second mechanism needs a reason a reviewer can check.
