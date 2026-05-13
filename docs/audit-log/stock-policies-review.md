# Stock policies — HITL pre-merge spot-check audit log

> Pre-merge HITL gate for slice 022. The 5 stock policy markdown files
> under `policies/stock/` ship with `source_attribution: community_draft`
> (agent-authored draft). This file is the audit trail of the human
> spot-check that approves those drafts for the v1 release. PR #022 is
> held open at `in-review` until the 5 policy bodies are reviewed and
> the reviewer signs below.

## Review status

**Status:** APPROVED — all 5 policies ship as drafted
**Reviewer:** Matt Goodrich
**Review date:** 2026-05-13
**Total stock policies:** 5
**Source attribution:** all `community_draft` (agent-authored draft text — to be superseded by community-contributed or org-internal policies on adoption)

## Review priority order

The five stock policies should be reviewed in this order. Each is
audit-binding once approved + published, so the review covers structure,
content accuracy, role assignments, and the SCF anchor codes the
frontmatter declares.

1. **Information Security Policy** (`policies/stock/information-security-policy.md`)

   - Foundational umbrella policy; sets the tone for the other four.
   - Frontmatter declares: owner_role=tenant_admin, approver_role=security_lead.
   - Linked SCF anchors: `GOV-01` (Governance), `GOV-04` (Steering Committee),
     `RSK-01` (Risk Management Program).
   - Review the leadership commitment language, scope statement, and the
     classification ladder (restricted / confidential / internal / public).

2. **Access Control Policy** (`policies/stock/access-control-policy.md`)

   - Most behaviorally specific of the five — covers MFA, SSO, least
     privilege, access review cadence, deprovisioning SLAs.
   - Linked SCF anchors: `IAC-01` (Identification & Authentication),
     `IAC-07` (User Provisioning & Lifecycle), `IAC-22` (Account Management).
   - Review the MFA stance (phishing-resistant required for production
     access), the shared-account ban, and the 90-day inactivity threshold.

3. **Vendor Management Policy** (`policies/stock/vendor-management-policy.md`)

   - Three-tier classification (Critical / Standard / Operational) and
     the review cadence per tier.
   - Linked SCF anchors: `TPM-01` (Third-Party Management),
     `TPM-03` (Third-Party Risk Assessments), `TPM-04` (Third-Party Contracts).
   - Review the 12-month attestation requirement, 72-hour breach
     notification clause, and the 60-day pre-renewal notification window.

4. **Incident Response Plan** (`policies/stock/incident-response-plan.md`)

   - Most operationally detailed — severity ladder (SEV-1..4), response
     SLAs, post-incident review cadence, external notification clock.
   - Linked SCF anchors: `IRO-04` (Incident Response Plan),
     `IRO-01` (Incident Handling), `IRO-02` (Incident Response Testing).
   - Review the severity definitions, the containment-over-investigation
     principle, the ten-business-day post-incident review SLA, and the
     external-notification ownership (security lead, not on-call engineer).

5. **Change Management Policy** (`policies/stock/change-management-policy.md`)
   - Most easily over-engineered; review for proportionality.
   - Linked SCF anchors: `CHG-02` (Change Control Process),
     `CFG-02` (Configuration Change Control), `CHG-04` (Change Management
     Audit Trail).
   - Review the emergency-change variant, the schema-change additional
     requirements, the named-blast-radius threshold (5% of production
     traffic), and the deployment-pipeline-as-system-of-record stance.

## Per-policy review log

(Reviewer: complete one row per policy reviewed. Format: title | overall
approve/revise/reject | notes.)

| Policy                      | Decision  | Reviewer notes |
| --------------------------- | --------- | -------------- |
| Information Security Policy | _pending_ |                |
| Access Control Policy       | _pending_ |                |
| Vendor Management Policy    | _pending_ |                |
| Incident Response Plan      | _pending_ |                |
| Change Management Policy    | _pending_ |                |

## SCF anchor verification

The frontmatter `linked_control_ids` arrays reference SCF anchor codes
(e.g. `GOV-01`). The CLI seeder resolves these via the `controls` table
at seed time. Slice 010 (SOC 2 control kit) is not yet merged, so the
resolved-link count will be **0 on a fresh deploy** — every seeded
policy will surface the `orphan_policy` warning on read until the SOC 2
control kit lands and the matching controls exist.

This is the expected v1 behavior. The HITL reviewer confirms only that
the anchor codes are the right ones, not that they currently resolve.

## HITL decisions

(To be filled during the pair-review session. Use the format from slice
007's `soc2-mapping-review.md`: each decision its own subsection with
the rationale.)

## HITL decisions (2026-05-13)

Pair-review session between orchestrator + reviewer Matt Goodrich. All 5 stock policies approved as drafted. Decisions:

- **Information Security · Access Control · Vendor Management · Incident Response** approved as-is at `approver_role: security_lead`. Structure (Purpose / Scope / Policy / Roles / Enforcement / Review) is sound; SCF anchor links match canonical control families; word counts (430–620) appropriate for stock-policy templates.
- **Change Management Policy** approved as-is at `approver_role: engineering_lead` — defensible deviation from the security_lead default. Change management is engineering-owned; routing through security_lead would create a bottleneck on routine deploys. The CHG-02 / CFG-02 / CHG-04 anchor links cover the security-relevant guardrails.
- **Incident Response Plan's SEV-1..4 SLA ladder** approved as drafted (no override). Adopters can localize the response SLAs without changing the structure.
- All 5 ship as `community_draft` source attribution. Adopters can mark policies `org_internal` when they revise the body to match their actual program.
- AC-3 link-to-≥3-controls satisfied uniformly (3 SCF anchor codes per policy, no orphans).

## Sign-off

- Reviewer name: Matt Goodrich
- Reviewer role: solo security leader / project owner
- Review date: 2026-05-13
- Total policies reviewed: 5 (full bundle)
- Policies approved as-is: 5
- Policies revised before merge: 0
- Policies rejected: 0
- Source attribution: all `community_draft` — adopters re-attribute when they author org-internal revisions
- Signature / commit SHA of merge: (filled by orchestrator after squash-merge)
