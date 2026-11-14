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
2. **Requesters are recorded by person.** `submitted_by` on policies and framework scopes, and `requested_by` on exceptions, hold the users(id) for a person. A machine is recorded by its principal (`credstore.PrincipalOf`), never by its per-token JWT id. A requester recorded as a session id (`jwt:<token id>`) before this change cannot be compared. Policies and framework scopes refuse to approve it until it is re-submitted. Exceptions cannot be re-requested, so such a row keeps its original rule: the approving credential must differ.
3. **Self-approval is an organisation setting, off by default.** `tenants.allow_self_approval` ("Allow self-approval") lets the requester approve their own record. Only an admin can change it (`PATCH /v1/tenants/{id}`), and every change writes a `me_audit_log` row (`tenant_self_approval_update`) with the before and after values.
4. **The opt-in relaxes only "same person".** A machine credential still never approves, whatever the setting.
5. **A self-approval is visibly marked.** The record carries `self_approved = true`. That flag appears on the API wire, in the policy and exception CSV/xlsx exports, and as a "Self-approved" marker in the UI. The audit log names it too: a `policy_audit_log` row with action `self_approved`, the `self_approved` field in the framework-scope workflow log's after-image, and the reason on an exception's `approved` audit row.
6. **The exceptions CHECK honours the setting through the flag.** A CHECK cannot read `tenants` at write time. The setting is therefore denormalised onto the row. The CHECK becomes `approved_by IS NULL OR approved_by <> requested_by OR self_approved`. The application reads `allow_self_approval` in the same transaction as the approval and sets `self_approved` only when it allowed a self-approval. A write that skips the application rule cannot hide a same-person approval, because without the flag the CHECK still refuses it. With the flag, the record says so.

## Alternatives considered

- **A trigger that reads `tenants.allow_self_approval`.** This would keep the database as the only judge. But it splits the rule between a trigger and Go, duplicates the "person" logic in PL/pgSQL, and still needs the flag to mark the record. Rejected: the denormalised flag gives the same guarantee (no unmarked self-approval) with one rule in one language.
- **Per-record override ("approve my own, just this once").** This puts the decision in the hands of the person it constrains. Rejected: the opt-in is an organisation-level policy decision, set by an admin and audited.
- **No opt-in; require a second person always.** This is the safest rule. It blocks the solo operator, the primary v1 user, from completing any approval. Rejected; the marker keeps the opt-in honest instead.
- **Silent self-approval when the setting is on.** Rejected. An auditor must be able to tell a self-approval from a second person's approval without reading the tenant's settings history.

## Consequences

- Turning Allow self-approval on does not rewrite history. Only approvals made while it is on are flagged. Turning it off again stops new self-approvals and leaves earlier flags in place.
- Exception approval is now people-only. Before, any approver-role credential could approve. An approver-role unbound API key, OAuth client or bootstrap admin now gets 403.
- Approval-reset paths clear the flag together with the approver. A framework-scope predicate or applicability edit bounces the scope to draft and resets `self_approved`.
- New approval surfaces call `approval.Check` and `approval.AllowSelf` rather than re-implementing the comparison. A second mechanism needs a reason a reviewer can check.
