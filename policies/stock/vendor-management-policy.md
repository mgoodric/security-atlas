---
title: Vendor Management Policy
version: 1.0.0
owner_role: tenant_admin
approver_role: security_lead
linked_control_ids:
  - TPM-01
  - TPM-03
  - TPM-04
acknowledgment_required_roles:
  - procurement_owner
  - security_lead
source_attribution: community_draft
---

# Vendor Management Policy

## Purpose

This policy defines how the organization evaluates, onboards, monitors, and
offboards third-party vendors that handle the organization's data or access
the organization's systems. Vendor risk is the organization's risk; this
policy makes that explicit and assigns ownership.

## Scope

This policy applies to every third party that:

- Processes, stores, or transmits the organization's data, including
  customer data
- Holds access credentials to the organization's systems
- Provides infrastructure on which the organization's production systems run
- Provides a service that, if unavailable, would degrade the organization's
  ability to deliver to customers

## Policy

### Vendor classification

Every vendor is classified at onboarding:

- **Critical** — production-dependency or processes restricted/confidential
  data. Requires security review every 12 months and a contractual data
  protection addendum.
- **Standard** — processes internal data or holds non-privileged access.
  Requires security review every 24 months.
- **Operational** — no data processing, no system access (e.g., catering,
  facilities). Tracked but no recurring security review.

### Pre-onboarding security review

Critical and Standard vendors complete a security review before contract
signature. The review evaluates:

- Independent attestation (SOC 2 Type II, ISO 27001, or equivalent) — current
  within 12 months
- Data protection posture for the categories of data they will handle
- Incident notification commitments — at minimum 72 hours, written into the
  contract
- Sub-processor list and the vendor's own vendor-management practices

Vendors that cannot produce a current attestation document receive a
self-completed assessment (CAIQ or equivalent) and the gap is recorded as
an accepted risk on the vendor's record.

### Ongoing monitoring

- Critical and Standard vendors are reviewed on the cadence above. Reviews
  are recorded in this platform.
- The procurement owner is notified 60 days before a contract auto-renew
  date so the renewal can be evaluated against current posture.
- Material changes to a vendor's posture (breaches, ownership changes,
  withdrawal of an attestation) are reviewed within 30 days.

### Offboarding

When a vendor relationship ends:

- Access is revoked within five business days of contract end.
- Data is returned or attested-destroyed per the contractual data handling
  terms. The destruction attestation is filed against the vendor record.
- The vendor record is marked terminated and retained for at least seven
  years for audit history.

## Roles and responsibilities

- **Procurement owner** — initiates vendor onboarding; tracks renewal dates;
  initiates offboarding.
- **Security lead** — owns this policy; conducts pre-onboarding reviews;
  accepts vendor-level risk; signs off on ongoing reviews.
- **Engineering / IT owners** — provide the technical scope (what data,
  what access) for each vendor; participate in security reviews of vendors
  whose service their team consumes.

## Enforcement

Engaging a Critical or Standard vendor before completion of the
pre-onboarding security review is a policy violation and is treated as a
security incident.

## Review and revision

This policy is reviewed annually and on every material change to the data
classification matrix or the regulatory environment.
