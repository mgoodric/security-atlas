# 042 — Audit workspace view (sample-pull + walkthrough + comments) + auditor login

**Cluster:** Frontend views
**Estimate:** 2.5d
**Type:** AFK

## Narrative

Build the audit workspace — the view auditors land in after signing in. Layout: top bar shows the active `AuditPeriod` + scope; left nav shows assigned controls; main area shows the current control with the auditor's testing notes (private), sample-pull controls, walkthrough recording, and the Audit Hub comment thread on each control. The auditor's workflow is end-to-end: sign in → pick a control → see population → pull sample → review evidence → leave comment → optionally record walkthrough → mark finding. The slice delivers value because the audit cycle that the binary v1 success test depends on can complete in the platform — without auditors leaving for email/Drive/Slack.

## Acceptance criteria

- [ ] AC-1: `/audit` route lands the auditor in their assigned AuditPeriod context
- [ ] AC-2: Left nav lists controls in scope for the period
- [ ] AC-3: For each control: population summary, sample-pull form (n, seed), pulled samples with annotation form
- [ ] AC-4: Walkthrough recorder allows narrative + attachment upload; saves to slice 027 record
- [ ] AC-5: Comment thread visible on the control; auditor and auditee comments visually distinguished; private notes only visible to auditor
- [ ] AC-6: Sign-out clears all auditor session state; subsequent sign-ins resume to assigned period
- [ ] AC-7: Tab between controls without losing in-progress sample annotations

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** the view is bounded to frozen evidence
- **Auditor first-class:** matches the persona requirements from canvas §1.4

## Canvas references

- `Plans/canvas/08-audit-workflow.md` (all subsections)
- `Plans/mockups/index.html` (not pictured, but mockup-equivalent)

## Dependencies

- #025, #026, #027, #029

## Anti-criteria (P0)

- Does NOT permit auditor to see data outside their assigned AuditPeriod / scope
- Does NOT permit auditee to read auditor's private testing notes
- Does NOT lose in-progress sample annotations on tab switch

## Skill mix (3–5)

- Next.js + shadcn/ui
- TanStack Query with optimistic updates
- Auditor session state management
- Multi-form coordination
- Visual design for auditor workspace
