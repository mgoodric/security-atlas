# Authz role + Rego policy spot-check audit log

> Pre-merge HITL gate for slice 035. The agent-authored DRAFT role
> enum (`migrations/sql/20260511000018_rbac_authz.sql` CHECK
> constraint) + the 10 seed Rego policies under `policies/authz/*.rego`
> ship with `community_draft` attribution. This file is the audit
> trail of the human spot-check that converts those drafts into the
> slice's merge-ready artifact. PR #035 is held open at `in-review`
> until the reviewer signs below.

## Review status

**Status:** APPROVED — 5 roles + 26-cell role × endpoint matrix + ABAC auditor × audit_period cell ship as drafted
**Reviewer:** Matt Goodrich
**Review date:** 2026-05-13
**Canonical role enum file:** `migrations/sql/20260511000018_rbac_authz.sql` (CHECK constraint on `user_roles.role`)
**Rego policy directory:** `policies/authz/`
**Source attribution:** `community_draft` (agent-authored, slice 035)
**Total Rego files:** 10

## Review priority order

The reviewer should walk the matrix from the role × endpoint table in
the PR body and verify the expected outcome matches the security
practitioner's intent. The priority order below clusters the cells
that are most likely to surface a real role-boundary mistake.

1. **auditor write surfaces** — auditor MUST NOT push evidence, write
   risks, or approve policies. The matrix asserts deny on each;
   reviewer confirms.
2. **control_owner write surfaces** — control_owner MAY submit
   evidence (attestation) and MAY NOT approve policies, write risks,
   or publish. The boundary is "attest, don't govern."
3. **viewer write surfaces** — viewer MUST be read-only across all
   tenant-scoped resources. The matrix asserts deny on every write
   for viewer.
4. **grc_engineer scope** — grc_engineer SHOULD have the full
   GRC-operator surface (write risks, controls, policies, framework
   scopes, exceptions) AND state transitions (submit / approve /
   publish). Confirm the operator role is permissive enough for a
   security-leader-of-one running the program.
5. **admin scope** — admin allows everything within tenant. Confirm
   this is the desired behavior and that there is NO emergency-bypass
   role above admin.
6. **ABAC: auditor × audit_period** — auditor with assigned periods
   `{A}` denied access to a sample with `audit_period_id=B`. The
   matrix test exercises this; reviewer confirms canvas §9.5 intent.
7. **Catalog public reads** — `defaults.rego` allows read for
   `anchors / frameworks / schemas / scf / themes / requirements /
ucf / scopes`. Confirm this list is correct for catalog surfaces.
8. **No emergency-bypass role** — the CHECK constraint enumerates
   exactly 5 roles. No `bypass`, no `superadmin`, no `system`.
   Verified by `migrations/sql/20260511000018_rbac_authz.sql`.

## Per-role review log

(Reviewer: append one row per role reviewed. Format: role | matrix
cells reviewed | approved? | reviewer notes.)

| role          | matrix cells reviewed | approved? | reviewer notes |
| ------------- | --------------------- | --------- | -------------- |
| admin         |                       |           |                |
| grc_engineer  |                       |           |                |
| control_owner |                       |           |                |
| auditor       |                       |           |                |
| viewer        |                       |           |                |

## Per-Rego-file review log

(Reviewer: append one row per file reviewed.)

| file                                | reviewed? | reviewer notes |
| ----------------------------------- | --------- | -------------- |
| `policies/authz/defaults.rego`      |           |                |
| `policies/authz/helpers.rego`       |           |                |
| `policies/authz/admin.rego`         |           |                |
| `policies/authz/grc_engineer.rego`  |           |                |
| `policies/authz/control_owner.rego` |           |                |
| `policies/authz/auditor.rego`       |           |                |
| `policies/authz/viewer.rego`        |           |                |
| `policies/authz/audit_periods.rego` |           |                |
| `policies/authz/scope_cells.rego`   |           |                |
| `policies/authz/system.rego`        |           |                |

## HITL decisions (2026-05-13)

Pair-review session between orchestrator + reviewer Matt Goodrich. All 5 roles and the 26-cell role × endpoint matrix approved as drafted. Decisions:

- **5 roles canonical** — `admin` / `grc_engineer` / `control_owner` / `auditor` / `viewer`. Maps to standard GRC personas; the CHECK constraint on `user_roles.role` enforces the closed set.
- **Separation-of-duties DENYs preserved as drafted:**
  - `control_owner` can NOT write risks or approve/publish policies (control-implementer ≠ governance approver)
  - `auditor` can NOT push evidence (taint risk — auditor independence)
  - `viewer` denies all writes (pure read role)
- **ABAC auditor × audit_period constraint** — auditors can only read sample populations within their assigned `audit_period_ids`. Period-match is mandatory, not optional. Matches canvas §8.4 (audit-period freezing) intent.
- **All 10 Rego files ship as community_draft** source attribution. Adopters can revise per their org's role model (e.g., split control_owner into separate sub-roles, add a "compliance_lead" between grc_engineer and admin) by editing the .rego files; the role enum CHECK constraint must be updated in lockstep.
- **No emergency-bypass code path** — confirmed. Default-deny without exception (P0 anti-criterion).

## Sign-off

- Reviewer name: Matt Goodrich
- Reviewer role: solo security leader / project owner
- Review date: 2026-05-13
- Total roles reviewed: 5 (full enum)
- Total matrix cells reviewed: 26 (the AC-5 representative set) + 1 ABAC cell
- Roles approved as-is: 5
- Roles revised before merge: 0
- Roles rejected: 0
- Source attribution: all `community_draft` — adopters re-attribute when they revise to match their org's role model
- Signature / commit SHA of merge: (filled by orchestrator after squash-merge)
