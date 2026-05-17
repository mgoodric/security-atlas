# ADR 0001 — FrameworkScope predicate lifecycle workflow

**Status:** Accepted · Honored (verified 2026-05-15 by slice 071 audit — four-state lifecycle `draft → review → approved → activated` is implemented in slice 018 + reused by slice 030 OSCAL export's framework-scope intersection compute)

**Date:** 2026-05-11

**Resolves:** [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) #19

**Implements through:** [`docs/issues/018-framework-scope-intersection.md`](../issues/018-framework-scope-intersection.md)

---

## Context

`FrameworkScope` rows carry the predicate that determines which scope cells count "in scope" for a given framework version (canvas §5.5). Two facts about who edits and who approves a predicate are unavoidable:

1. **The org's compliance lead knows where the systems live.** They draft and propose changes.
2. **The auditor approves what counts.** Their sign-off is the audit-binding event that the framework version downstream depends on.

Three secondary realities shape the workflow:

3. **Approval and activation are different events in time.** An auditor may approve a predicate weeks before the org wants it to take effect, because audit-period cutovers rarely align with calendar wall-clock. We need to record both moments separately.
4. **PCI / HIPAA scope reduction is the dominant control lever.** Customers narrow their PCI scope aggressively to reduce the assessment surface. The opposite — silently broadening scope after approval — is the failure mode that turns into "the auditor approved scope X but you've been operating under scope Y."
5. **Most auditors sign off offline.** The audit-binding evidence is usually a signed memo or email, not a click in the platform. The system must record this evidence faithfully and not pretend it has cryptographic provenance it doesn't have.

`Plans/canvas/11-open-questions.md` #19 deferred the workflow shape pending UX design.

## Decision

**Four-state lifecycle** with a separate activation step:

```
[draft] --submit--> [review] --approve--> [approved] --activate--> [activated]
   ^                                          |                         |
   |                                          |                         | superseded by
   |                                          |                         | new active version
   +<-----any predicate diff------------------+                         |
                                                                        v
                                                                  [superseded]
```

| State        | Transitions in                                                 | Transitions out                                                          |
| ------------ | -------------------------------------------------------------- | ------------------------------------------------------------------------ |
| `draft`      | new record · re-edit from `review`/`approved` (predicate diff) | `submit` → `review`                                                      |
| `review`     | `submit` from `draft`                                          | `approve` → `approved` · `revise` → `draft` · `predicate edit` → `draft` |
| `approved`   | `approve` from `review`                                        | `activate` → `activated` · `predicate edit` → `draft`                    |
| `activated`  | `activate` from `approved`                                     | superseded by a new `activated` row (this row → `superseded`)            |
| `superseded` | superseded by another row's activation                         | terminal                                                                 |

**Approval evidence shape:**

- **Primary (always recorded):** in-app attestation. When an approver clicks `Approve`, the system records `approver_user_id`, `approved_at`, and `predicate_hash_at_approval` (sha256 of the canonicalized predicate JSON at the moment of approval). This is the load-bearing audit trail.
- **Secondary (optional):** file upload. The approver may attach a signed auditor memo (PDF / image / email export) via `approval_evidence_file_url` pointing at the slice-036 S3 artifact store. The system records the file's content hash but does **not** verify the signature on the memo (that's the auditor's domain).

**Schema (slice 018 lands this):**

- `framework_scopes.state` text (CHECK enum: `draft | review | approved | activated | superseded`)
- `framework_scopes.predicate` jsonb (the scope predicate — boolean over scope dimensions, same shape as slice 017's `applicability_expr`)
- `framework_scopes.predicate_hash` text (sha256 of canonicalized `predicate`, recomputed on every save, indexed)
- `framework_scopes.approver_user_id` uuid NULL (set on approve)
- `framework_scopes.approved_at` timestamptz NULL (set on approve)
- `framework_scopes.predicate_hash_at_approval` text NULL (snapshot of `predicate_hash` at approve-time)
- `framework_scopes.approval_evidence_file_url` text NULL (optional; opaque URL to slice-036 storage)
- `framework_scopes.approval_evidence_file_hash` text NULL (sha256 of the uploaded file)
- `framework_scopes.effective_from` timestamptz NULL (set on activate)
- `framework_scopes.superseded_by` uuid NULL self-reference (set when a successor activates)
- `framework_scopes.superseded_at` timestamptz NULL

**Re-approval rule (strict — applies to all frameworks in v1):**

Any change to the `predicate` field bounces the row back to `draft`, clearing `approver_user_id`, `approved_at`, `predicate_hash_at_approval`, and any uploaded evidence file. Concretely: a database trigger on `BEFORE UPDATE` compares `OLD.predicate_hash` to `NEW.predicate_hash`; if they differ AND `OLD.state IN ('review', 'approved')`, the trigger forces `NEW.state = 'draft'` and clears the approval columns. The UI surfaces this as a banner: `"approval invalidated — predicate changed, resubmit for review"`.

This is the **strictest of the three options considered** (the others being `narrowing-only re-approves` and `never auto-invalidate`). The decision rationale:

- For PCI / HIPAA, any predicate edit is a scope event that the auditor needs to see.
- For SOC 2, strict re-approval is over-careful but the friction is low (auditor re-signs annually anyway).
- It is **cheaper to relax later** (add a `framework_scopes_reapproval_policy` per-framework config in v1.x if SOC 2 friction shows up) **than to discover audit-period drift** caused by a sneakily-broadened predicate.

**Activation timing:**

`activate` is a separate explicit action because audit-period cutovers don't align with approval timing. The compliance lead picks `effective_from` (any timestamp, past or future). When the timestamp passes, downstream consumers (control-state evaluation, OSCAL export, sample-pull primitives) start using this row as the active scope. The prior `activated` row's `superseded_by` column is set to this row's id.

Only one row per `(tenant_id, framework_version_id)` may be in state `activated` AND have `effective_from <= now()`. Enforced by a partial unique index.

## Consequences

**Positive:**

- Clean separation of "auditor agreed" vs "scope is in effect" — the two largest sources of audit-period drift confusion.
- Strict re-approval gives PCI the posture it needs without per-framework config in v1.
- In-app attestation produces a tamper-evident audit trail even when the auditor never logs in (the attestation is by the compliance lead, recording that the auditor's offline memo authorizes the approval).
- `predicate_hash_at_approval` lets the platform prove later that the approval was for the exact predicate text at that moment, defeating the "but the predicate said X when you approved it" disagreement.

**Negative:**

- More UI surface than a two-state workflow (four states + activation form vs one button).
- Strict re-approval may be over-strict for SOC 2 (where auditors typically re-look at scope only at annual cycle anyway). If friction surfaces, a per-framework config (`reapproval_on_change: any | narrowing | never`) can be added in a v1.x slice without invalidating existing data.
- Database-level state-machine enforcement (the BEFORE UPDATE trigger) is harder to test than application-level logic. Slice 018 must include an integration test that proves a predicate edit on an `approved` row forces back to `draft`.

**Neutral:**

- The optional file upload depends on slice 036 (S3 artifact store). Slice 018 can ship without it (record only the hash + a stub URL placeholder); enabling the upload UI is a small follow-up after 036 lands.

## Alternatives considered (rejected)

- **Two-state (draft → approved/active).** Rejected because activation timing matters too much for audit-period cutovers.
- **Three-state with combined approve+activate.** Rejected for the same reason — and because the compliance lead is usually a different human (or moment) than the auditor.
- **Edit-with-audit-log only (no approval gate).** Rejected for PCI/HIPAA where scope drift without sign-off is a control failure, not just a hygiene issue.
- **Narrowing-only re-approves.** Reasonable for SOC 2 but wrong for PCI. We can add it later as a per-framework toggle.
- **File-only evidence (no in-app attestation).** Rejected because the in-app attestation is the tamper-evident anchor; the file is the auditor's own document and the system can't verify it.

## Related decisions

- Resolves open question #19 (FrameworkScope ownership).
- Composes with the #13 resolution (build multi-tenant from day one): the approver role is tenant-scoped via RLS like every other primitive.
- Composes with slice 014 (schema registry) approver-role pattern: the same `IsAdmin`-style role gate plus a tenant-scoped approver role.
- The optional evidence file is stored via slice 036 (S3 artifact store) — slice 018 ships URL+hash columns; 036 supplies the upload endpoint.
