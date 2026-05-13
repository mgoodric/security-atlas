---
title: Incident Response Plan
version: 1.0.0
owner_role: tenant_admin
approver_role: security_lead
linked_control_ids:
  - IRO-04
  - IRO-01
  - IRO-02
acknowledgment_required_roles:
  - on_call
  - security_lead
  - engineering_lead
source_attribution: community_draft
---

# Incident Response Plan

## Purpose

This plan defines how the organization detects, contains, eradicates,
recovers from, and learns from security incidents. The objective is to
minimize harm to customers, employees, partners, and the organization
itself, while preserving evidence and meeting contractual and regulatory
notification obligations.

## Scope

This plan applies to every security incident, defined as an actual or
suspected unauthorized access to, disclosure of, modification of, or
destruction of information; or any failure of the organization's
information security controls that exposes the organization to such
harm.

## Severity classification

Incidents are classified at declaration:

- **SEV-1 (critical)** — confirmed unauthorized access to restricted data;
  active intrusion in production; ransomware; loss of authentication
  control. Response is immediate and 24x7.
- **SEV-2 (high)** — credible suspicion of intrusion; unauthorized access
  to confidential data; meaningful service availability loss with security
  signal. Response within one business hour.
- **SEV-3 (moderate)** — security policy violation by a known actor with
  no customer impact; security control failure surfaced by monitoring
  without evidence of exploitation. Response within one business day.
- **SEV-4 (low)** — informational finding; near-miss; security event
  worth recording but no response action required.

## Lifecycle

### Detection

Sources: SIEM alerts, EDR alerts, customer reports, vendor notifications,
employee reports. Every detection source has a documented routing rule
into the incident tracker.

### Triage

The on-call engineer:

1. Acknowledges within the SLA above.
2. Confirms whether the signal is a security incident, an operational
   issue, or a false positive.
3. Assigns a severity.
4. Opens an incident record in this platform.

### Containment

Containment is the priority over investigation. For SEV-1 and SEV-2:

- Isolate the affected system(s) from production where feasible.
- Rotate credentials suspected of compromise.
- Disable identities suspected of compromise.
- Document containment actions in the incident record as they happen.

### Eradication

After containment, identify and remove the root cause:

- Patch the exploited vulnerability.
- Remove malicious artifacts.
- Restore systems from clean backups when integrity is in question.

### Recovery

Bring systems back online with monitoring elevated. Recovery is complete
when monitoring is stable for a full business cycle (24 hours minimum).

### Post-incident review

Every SEV-1 and SEV-2 incident has a written post-incident review within
ten business days, covering:

- Timeline of detection, containment, eradication, recovery
- Root cause
- Contributing factors
- Control failures that allowed the incident to occur or to escalate
- Corrective actions with owners and target dates
- Lessons captured in the Decision Log

The post-incident review document is filed against the incident record
and surfaced in the monthly board brief.

### Notification

External notification follows the contractual and regulatory clock:

- Customer notification per the data processing addendum (typically 72
  hours from confirmed unauthorized access to customer data).
- Regulatory notification per applicable law (GDPR Art. 33: 72 hours;
  state breach laws: per state).
- Vendor notification when a vendor's product was the entry point.

The security lead owns external communications; the on-call engineer
does not communicate externally during an active incident.

## Roles and responsibilities

- **On-call engineer** — first responder; acknowledges, triages,
  contains; updates the incident record.
- **Security lead** — incident commander for SEV-1 and SEV-2; owns
  external communications; signs off on the post-incident review.
- **Engineering leads** — provide subject-matter experts for the affected
  systems; own corrective actions in their domains.
- **Executive sponsor** — informed for SEV-1; consulted on external
  communications.

## Exercises

This plan is exercised at least annually via a tabletop exercise covering
at least one SEV-1 and one SEV-2 scenario. Exercise outcomes are filed as
evidence.

## Review and revision

This plan is reviewed annually and after every SEV-1 incident.
