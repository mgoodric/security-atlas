# 029 — Audit Hub threaded comments

**Cluster:** Audit workflow
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement threaded comments on first-class audit objects (Control, Finding, Sample, Walkthrough). Auditor leaves a comment; auditee receives a notification, replies in-product, attaches additional evidence. The comment thread is retained as an audit artifact, exported to OSCAL assessment-results `observation` annotations (slice 030). This is _not_ a separate messaging product — it's annotations on existing objects. Practitioner research showed this is the single most-valued feature when migrating between GRC tools (Drata's audit hub pattern). The slice delivers value because audit conversations leave email/Drive/Slack and become part of the audit record.

## Acceptance criteria

- [ ] AC-1: `POST /v1/audit-notes` creates a comment on `scope: control | sample | walkthrough | finding` with body, author, visibility (`auditor_private | shared`)
- [ ] AC-2: Replies thread to the parent via `parent_note_id`
- [ ] AC-3: Notifications fire on new comment (email or in-app — at least one channel)
- [ ] AC-4: Comments are scoped to an `AuditPeriod`; visible per visibility rules
- [ ] AC-5: Comments exportable: included as `observation` annotations in slice 030's OSCAL output
- [ ] AC-6: Comments are immutable once posted (edits create reply chains, not in-place mutation)

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** comments are pinned to an AuditPeriod; immutable after period freeze
- **Anti-pattern rejected (vanity trust centers):** focused, not bolted-on social features

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.5 (Audit Hub pattern)

## Dependencies

- #025

## Anti-criteria (P0)

- Does NOT permit edit-in-place of posted comments
- Does NOT leak `auditor_private` comments to auditees
- Does NOT exceed audit-record scope (this is not a chat product)

## Skill mix (3–5)

- Go threaded-comment data model
- Postgres recursive CTE for thread retrieval
- Notification dispatch (email + webhook sink)
- Visibility-based query filtering
- Next.js comment UI
