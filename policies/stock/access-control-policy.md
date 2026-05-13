---
title: Access Control Policy
version: 1.0.0
owner_role: tenant_admin
approver_role: security_lead
linked_control_ids:
  - IAC-01
  - IAC-07
  - IAC-22
acknowledgment_required_roles:
  - employee
  - contractor
source_attribution: community_draft
---

# Access Control Policy

## Purpose

This policy governs how identities are established, granted access to
information systems, reviewed, and removed. The premise is least privilege:
every identity holds the minimum access required to perform its role and no
more, and that access is provably reviewed on a recurring cadence.

## Scope

This policy applies to all human and non-human identities that access the
organization's information systems, including:

- Employee, contractor, and intern accounts
- Service accounts and API keys
- Third-party integrations and SaaS-to-SaaS connections
- Shared accounts (which are forbidden by default; see below)

## Policy

### Identity provisioning

- Identities are provisioned through the organization's identity provider
  (OIDC) wherever the integration supports it. Direct password-only accounts
  on individual systems are forbidden when the system supports SSO.
- Multi-factor authentication is required on every identity. Phishing-resistant
  factors (WebAuthn / FIDO2 / hardware tokens) are required for any role
  that holds production system access.
- Shared accounts are forbidden. Where a system architecturally requires a
  shared service account, ownership is assigned to a named individual, the
  credential is stored in the organization's secrets manager, rotation is
  scheduled, and every use is logged.

### Authorization

- Access is granted by role, not by individual. Role-to-permission mappings
  are documented in this platform's RBAC configuration; per-individual
  permission grants are reviewed by the security lead.
- The principle of least privilege governs every role. New permissions are
  granted only when a documented job duty requires them.
- Production system access is segregated from development access. Identities
  that hold both production and development access carry an annotation and
  are reviewed quarterly.

### Access review

- Access to systems holding `restricted` or `confidential` data is reviewed
  quarterly by the system owner.
- Access to systems holding `internal` data is reviewed annually.
- Reviews are recorded in this platform; an access review with an empty
  approval list is not a complete review.

### De-provisioning

- Termination, role change, or contract end triggers same-day revocation of
  human-facing access (SSO disable, MFA factor removal). Service accounts
  the departing person owned are reassigned within five business days.
- Inactive accounts (no authentication event in 90 days) are flagged for
  review and disabled within 14 days unless the owner justifies retention.

## Roles and responsibilities

- **Security lead** — owns this policy; reviews per-individual permission
  grants; signs off on access review completion quarterly.
- **System owners** — perform quarterly access reviews on the systems they
  own; sign off explicitly per review.
- **People operations** — initiate provisioning on hire; initiate
  de-provisioning on termination or contract end the day of.

## Enforcement

Violations are treated as security incidents under the Incident Response
Plan. Repeat violations may result in disciplinary action.

## Review and revision

This policy is reviewed annually and on every material change to the
identity provider, SSO architecture, or RBAC model.
