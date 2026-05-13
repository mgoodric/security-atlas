---
title: Change Management Policy
version: 1.0.0
owner_role: tenant_admin
approver_role: engineering_lead
linked_control_ids:
  - CHG-02
  - CFG-02
  - CHG-04
acknowledgment_required_roles:
  - engineer
  - engineering_lead
  - sre
source_attribution: community_draft
---

# Change Management Policy

## Purpose

This policy governs how changes to production systems are proposed,
reviewed, deployed, and recorded. Its premise is that production changes
must be reviewed, traceable, and reversible — not because review is itself
the goal, but because the audit trail is the only credible signal that the
organization knows what is running in production.

## Scope

This policy applies to every change that touches:

- Production code in customer-serving systems
- Production infrastructure (compute, network, storage, identity)
- Configuration of production systems, including feature flags above a
  named blast radius
- Database schemas in production
- Production secrets and access credentials

Changes to development and staging environments are outside the scope of
this policy. Emergency changes follow the variant process below.

## Policy

### Standard change

Every standard production change requires:

1. **Proposal** — a pull request or change record that describes the
   intent, the affected systems, and the rollback plan. PR description
   templates are provided.
2. **Review** — at least one human reviewer who is not the author. The
   reviewer evaluates correctness, security, and the rollback plan.
3. **Automated checks** — CI tests pass; static analysis clean; security
   scans clean for the relevant change class.
4. **Deployment record** — the deployment is recorded by the deployment
   pipeline, including author, reviewer, time, and the change record
   reference.

### Emergency change

When production is materially degraded and standard review would extend the
outage, the on-call engineer may deploy with post-hoc review. The
post-hoc review must be completed within one business day and recorded
against the same change record.

Emergency-change use is reviewed monthly by the engineering lead.
Repeated emergency-change use against the same surface is treated as a
process bug, not a workflow.

### Database schema change

Schema changes follow the standard change process plus:

- A documented rollback path. Schemas that cannot be rolled back (e.g.,
  destructive column drops) require explicit engineering lead approval
  and a documented data preservation step.
- A migration deploy step separate from the application deploy.
- Verification step that confirms the post-migration schema matches
  expectation before the application reads from it.

### Configuration change

Production configuration changes (feature flags, runtime parameters)
above a named blast radius — meaning a flag that, if flipped, affects
more than five percent of production traffic — follow the standard change
process. Below-threshold flips may be made by the on-call engineer with
a logged audit entry.

### Access credential change

Rotation of production credentials follows the standard change process
plus the additional step of recording the rotation in the secrets
manager's audit log and notifying the security lead within one business
day.

## Roles and responsibilities

- **Engineering lead** — owns this policy; approves emergency changes
  post-hoc; reviews emergency-change frequency monthly.
- **Author engineer** — proposes the change with a complete description
  and rollback plan.
- **Reviewer engineer** — evaluates correctness, security, and rollback
  plan; does not approve their own work.
- **On-call engineer** — may deploy emergency changes; owns post-hoc
  review submission.
- **Security lead** — reviews change records affecting security
  primitives (auth, encryption, audit logging).

## Audit trail

The deployment pipeline records every change record, reviewer, deployment
time, and outcome. This record is the system of record for change
management evidence. Manual deployments outside the pipeline are policy
violations.

## Review and revision

This policy is reviewed annually and on every material change to the
deployment pipeline or production architecture.
