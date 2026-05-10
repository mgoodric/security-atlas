# 021 — Exception / waiver workflow with auto-expiry + expiration calendar API

**Cluster:** Risk register
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the exception/waiver workflow per `06-risk.md` §6.3. An Exception is always scoped (specific scope cells) and time-bounded (`expires_at` required, max 365 days, no auto-renewal). State machine: `requested → approved | denied → active → expired`. Active exceptions flip the affected control's evaluation behavior (treats failing evidence as "excepted" rather than "fail" in the dashboard). Expired exceptions revert control evaluation to normal. Expose an expiration calendar API for the dashboard's "Upcoming items" panel. The slice delivers value because real programs have real exceptions and the model handles them with discipline.

## Acceptance criteria

- [ ] AC-1: `POST /v1/exceptions` creates with required fields: `control_id`, `scope_cell_predicate`, `justification`, `compensating_controls[]`, `expires_at`
- [ ] AC-2: `expires_at > now + 365d` is rejected
- [ ] AC-3: Workflow: `requested → approved` requires `approved_by` role check; transition logged
- [ ] AC-4: An active exception on a control × scope cell flips that cell's evaluation to `excepted` (visible in dashboard, not counted as fail)
- [ ] AC-5: A daily job marks `expires_at < now` exceptions as `status=expired`; control evaluation reverts to normal
- [ ] AC-6: `GET /v1/exceptions/expiring?within=30d` returns expiring-soon list (powers dashboard "Upcoming items")
- [ ] AC-7: Audit log captures every state transition

## Constitutional invariants honored

- **Invariant 9 (manual evidence first-class):** exception is a form of structured documentation alongside evidence
- **Anti-pattern rejected (audit-period evidence pollution):** exceptions are evidence-trail-recorded; auditor sees the explicit waiver chain, not silent acceptance

## Canvas references

- `Plans/canvas/06-risk.md` §6.3 (exception/waiver workflow)

## Dependencies

- #019, #017

## Anti-criteria (P0)

- Does NOT permit auto-renewal of exceptions
- Does NOT permit `expires_at` exceeding 365 days
- Does NOT silently expire exceptions without dashboard surfacing

## Skill mix (3–5)

- Go workflow / state-machine handlers
- Postgres state transitions
- Scheduled jobs (daily cron)
- Predicate-based scope matching (slice 017 utilities)
- Calendar API design
