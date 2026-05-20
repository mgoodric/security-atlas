# Policy

<!-- prettier-ignore-start -->
!!! info "What you'll learn"

    - What a Policy is in security-atlas (and why it must link to
      controls)
    - How policies are authored, reviewed, published, and acknowledged
    - How exceptions to policy work
<!-- prettier-ignore-end -->

A **Policy** in security-atlas is a governance document that references
controls. The inverse — "controls implement policies" — is the working
direction; the platform records both sides of the link so a control
without a policy is loud and a policy without a control is loud.

> A policy without a linked control is a Word doc. A control without a
> linked policy is engineer cargo-culting. — canvas §2.6

## The shape

| Field                            | What it means                                                                   |
| -------------------------------- | ------------------------------------------------------------------------------- |
| `title`, `version`               | Policies are heavily versioned — every published change cuts a new version row. |
| `effective_date`                 | When this version takes effect (may be future-dated).                           |
| `body_md`                        | Markdown source. Rendered to PDF for attestation; never lossy.                  |
| `owner`, `approver`              | RACI on the policy itself.                                                      |
| `acknowledgment_required_role[]` | Roles whose members must acknowledge this policy annually.                      |
| `linked_control_ids[]`           | What controls this policy governs.                                              |
| `status`                         | `draft` · `under_review` · `approved` · `published` · `superseded`              |

## v1 policy library

The platform ships **5 high-signal stock policies** — not 50
placeholders. v1 explicitly rejects the "policy template warehouse"
anti-pattern (canvas §1.6):

| Policy                      | Covers                                                          |
| --------------------------- | --------------------------------------------------------------- |
| Information Security Policy | The top-level commitment + governance structure.                |
| Access Control Policy       | IdP enforcement, least privilege, periodic review.              |
| Change Management Policy    | Code change review, deploy approvals, emergency change.         |
| Incident Response Policy    | Detection, classification, communication, post-incident review. |
| Vendor Management Policy    | Third-party risk, DDQ cadence, offboarding.                     |

Each ships as a starting template — you edit `body_md` to reflect your
org's actual practice before publishing. The seed policies are
not authoritative until you `publish` them.

## Browsing and editing policies

Sign in and open **Policies** in the sidebar. The list view shows every
policy with current version, status, owner, and link count to
controls.

Click a policy to open the detail view:

- The Markdown body renders on the page.
- The version history tab shows every prior published version.
- The **Controls** tab shows what this policy governs — clicking a
  control opens its detail view.
- The **Acknowledgments** tab shows current acknowledgment status by
  role member (per slice 107 — `?include=ack_rate`).

## The policy lifecycle

```
draft ──► under_review ──► approved ──► published ──┬──► superseded
                                                    │
                                                    └──► published (new version)
```

- **draft** — author is writing.
- **under_review** — reviewers (approver role) provide feedback.
- **approved** — approver has signed off; not yet effective.
- **published** — live, in effect. Acknowledgments may now be collected.
- **superseded** — a new version is published; this version remains in
  the audit trail.

Transitions are RBAC-gated. A policy in `published` does NOT mutate —
edits create a new version row.

## Acknowledgments

Each `published` policy with `acknowledgment_required_role` set
generates an acknowledgment task for every member of those roles:

1. The member sees the task on their dashboard.
2. They open the policy and read the body.
3. They click **Acknowledge** — the platform records the
   acknowledgment as an evidence record (`policy.acknowledgment.v1`)
   linked to the policy version they read.
4. The acknowledgment expires after the org's configured
   acknowledgment cadence (typically annual).

The board pack (and the SOC 2 audit export) reports current
acknowledgment rate per policy. Stale acknowledgments roll up as a
warning.

## Policy exceptions

Reality intrudes — sometimes a control cannot meet the policy for a
specific scope cell or system. Exceptions are first-class:

| Field                                        | What it means                                                          |
| -------------------------------------------- | ---------------------------------------------------------------------- |
| `control_id`                                 | What control is being excepted.                                        |
| `scope_predicate`                            | Where the exception applies (a subset of the control's applicability). |
| `justification`                              | Why. Required, free-text, surfaces in the auditor view.                |
| `compensating_control_ids[]`                 | What compensating controls are in place instead.                       |
| `requested_by`, `approved_by`, `approved_at` | RACI on the exception itself.                                          |
| `expires_at`                                 | When the exception ends — required, no open-ended exceptions.          |

Open **Policies → Exceptions** to view the register, or
**Controls → Exceptions** to see exceptions per control. An exception
in effect shows on the control detail view as a banner and on every
[AuditPeriod](../first-audit.md) report that overlaps its window.

## Policy in the audit export

When you export an OSCAL SSP, each in-scope control includes:

- Its current implementation narrative (from the control)
- The policies that govern it (links + version + effective date)
- Open exceptions against it
- Acknowledgment evidence for those policies (if
  `acknowledgment_required_role` is set)

This is what auditors expect — the documented policy chain, the
implementation, the exceptions, the attestations. The platform
generates it from the live data, not a separate "policy bundle"
artifact.

## Next steps

- [Controls →](controls.md) — what policies govern
- [Risks →](risks.md) — what policies treat
- [First audit →](../first-audit.md) — how policies appear in the OSCAL
  export

---

## Was this helpful?

Tell us in [GitHub
Discussions](https://github.com/mgoodric/security-atlas/discussions).
