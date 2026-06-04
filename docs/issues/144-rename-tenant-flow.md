# 144 — Rename-tenant flow (per-tenant admin or super_admin)

**Cluster:** Backend / Frontend / Multi-tenancy
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141. The slice 141 bootstrap creates a tenant named "Default Tenant" — the user MUST be able to rename it post-bootstrap (the maintainer's explicit ask).

**What this slice ships:**

- NEW endpoint `PATCH /v1/tenants/{id}` with body `{name?}` — gated on `(super_admin) OR (per-tenant admin in that tenant)`. (Slug rename out of scope — affects URLs.)
- NEW settings UI on existing `/settings` page (slice 103) — tenant-name input field, save button. Renders for admin + super_admin roles only.
- BFF route `web/app/api/tenants/[id]/route.ts`.
- Uniqueness constraint on `tenants.name` (case-insensitive; slug stays the immutable identifier).
- Audit-log integration via slice 124 unified aggregator: new `kind='tenant_rename'`.

**Scope discipline (what is OUT):**

- **Slug rename** — out of scope (affects URLs; potential breaking change for integrations + per-tenant URL routing if it ever ships).
- **Tenant logo / branding fields** — out of scope; future slice if needed.
- **Tenant description / metadata fields** — out of scope.
- **Tenant rename history rendering** — out of scope (the audit-log captures the trail; no dedicated UI to view tenant-rename history at v1).

## Threat model

Inherits slice 141. Rename-specific:

| STRIDE                       | Threat                                                                                                                                                                                           | Mitigation                                                                                                                                                                                                                                                      |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **T** Tampering              | Name injection — caller passes name with control characters / null bytes / HTML / Unicode confusables (`Аcme` Cyrillic А vs Latin A) that confuses the picker UI or impersonates another tenant. | Server-side validation: trim; reject control chars + null; cap at 64 UTF-8 bytes; normalize NFC; case-insensitive uniqueness check. Reject Unicode confusables via a confusables-detection library (or document accepted-risk for v1 if library cost too high). |
| **R** Repudiation            | Tenant rename without audit trail — picker shows "Acme" yesterday + "Globex" today; nobody knows who renamed it.                                                                                 | `tenant_rename` audit-log row written same-transaction; records actor + from + to. Surfaces in slice 124 unified aggregator.                                                                                                                                    |
| **E** Elevation of privilege | Per-tenant admin in Tenant A renames Tenant A to "Tenant B" (impersonation). Doesn't grant access to actual Tenant B's data, but confuses users / picker.                                        | Uniqueness constraint on `tenants.name` (case-insensitive) blocks. Returns 409 on conflict.                                                                                                                                                                     |

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `PATCH /v1/tenants/{id}` handler; auth gate `(super_admin) OR (per-tenant admin)`; body validation (name strip / cap / normalize).
- [ ] AC-2: Atomic transaction: UPDATE tenants.name + INSERT audit-log row.
- [ ] AC-3: Case-insensitive uniqueness constraint at schema level (`UNIQUE LOWER(name)` via expression index); 409 on conflict.
- [ ] AC-4: BFF route + settings UI extension.
- [ ] AC-5: Slice 124 unified audit-log aggregator extension: new `kind='tenant_rename'`.
- [ ] AC-6: Confusables-detection (or documented accepted-risk decision in D1 of the decisions log).
- [ ] AC-7: Cross-tenant isolation test (Tenant A admin renaming Tenant A doesn't allow access to Tenant B; can't rename Tenant B).
- [ ] AC-8: Playwright e2e on `/settings` page rename flow.
- [ ] AC-9: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 141.

## Canvas references

- `Plans/canvas/02-primitives.md` — tenants primitive shape.

## Dependencies

- **#141** Multi-tenant login (merged) — extends `tenants` write surface.
- **#103** Settings page (merged) — extends.
- **#124** Unified audit-log aggregator (merged) — new `kind='tenant_rename'`.
- Note: depends on **141 only**; per-tenant admin can rename their own tenant WITHOUT super_admin (slice 142) existing. If a deployment has not yet shipped slice 142, the auth gate just admits per-tenant admin only.

## Anti-criteria (P0 — block merge)

- Inherits slice 141 anti-criteria.
- **P0-RT-1** Slug NEVER renamable in this slice (out of scope; affects URLs).
- **P0-RT-2** Uniqueness constraint case-insensitive (NOT just exact-match) to block trivial impersonation.
- **P0-RT-3** Audit-log row written same-transaction; no out-of-band writes.
- **P0-RT-4** NO branding / logo / metadata fields in this slice.
- **P0-RT-5** NO vendor-prefixed test fixture tokens.

## Skill mix

- slice 141 packages (consume).
- Go integration tests + Playwright e2e.

## Notes for the implementing agent

Smallest spillover of the four. Pickup time ~0.5d. The Unicode confusables question (T → AC-6) is the only JUDGMENT call worth grilling at pickup.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 141. User explicitly asked for "rename the default tenant" in the original idea.
