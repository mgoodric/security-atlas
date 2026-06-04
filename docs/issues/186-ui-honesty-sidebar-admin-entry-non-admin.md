# 186 — UI honesty: sidebar "Admin" entry shown to non-admin users

**Cluster:** Quality / UI hygiene + Auth
**Estimate:** 0.5d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 178 first-pass audit, captured per AC-17. The
sidebar component (`web/components/shell/sidebar.tsx`) renders a
`{ href: "/admin", label: "Admin" }` entry unconditionally. Non-admin
users see the entry, click it, and hit the admin layout's authz gate
which returns 403 / redirects them off the page.

The admin layout's authz gate is correct — no privilege-escalation
risk. But the UI chrome is misleading: the sidebar advertises a
capability the user doesn't have. The user clicks expecting access,
gets bounced, loses confidence in the tool's affordance honesty.

Per CLAUDE.md's primary-user definition — "the solo security leader
at a 50-150-person startup who runs the entire program alone" — this
is the user MOST likely to have admin privilege, which has made the
finding subtle for slice 178's static analysis. But the deferred
multi-tenant SaaS shape (and the v2+ "additional user with
non-admin role" pattern) makes this finding load-bearing.

The fix is small: conditionally render the entry. The sidebar needs
to read the bearer's role(s) — server-side via the auth layout, or
client-side via a `useCurrentUser` hook (slice 164 ships
`GET /v1/me` returning `roles`).

## Threat model

**S — Spoofing.** No new auth surface. The sidebar reads the
existing session bearer's roles via the existing `/v1/me`
endpoint (slice 164).

**T — Tampering.** A client-side role check is NOT a security
boundary — the admin layout's server-side authz gate is the
boundary, and stays unchanged. Hiding the sidebar entry is a UI-
chrome decision, not an access decision.

**Verdict.** **no-mitigations-needed.** The fix is UI-only and
reads existing auth context. The server-side authz gate remains
the security boundary.

## Acceptance criteria

- **AC-1.** The sidebar component reads the current user's roles
  (via `useCurrentUser` or the slice-164 `/v1/me` hook the sidebar's
  topbar component already consumes).
- **AC-2.** The `{ href: "/admin", label: "Admin" }` entry renders
  only if the current user has `roles` including any admin variant
  (`admin`, `super_admin`, `tenant_admin` — whichever the
  slice-033 + slice-051 + slice-164 stack expose).
- **AC-3.** Existing Playwright spec `admin-bootstrap.spec.ts`
  is updated: the test fixture's `TEST_BEARER` is an admin bearer,
  so the assertion that the sidebar entry is visible to that user
  passes; a new assertion is added that with a non-admin bearer the
  entry is absent.
- **AC-4.** Slice 178's first-pass F-178-6 finding is resolved on
  the next audit run. The slice-178 audit harness's heuristic for
  non-admin bearer (when a separate non-admin bearer is plumbed —
  future slice) will see the entry absent.
- **AC-5.** Unit test added: a render of the `Sidebar` component
  with a mocked `useCurrentUser` returning `roles=[user]` does
  NOT include the Admin entry; the same render with
  `roles=[admin]` DOES.

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer; application code is not the
  trust boundary).** This fix is ABOVE the auth layer — the UI
  chrome reads the bearer's role for chrome decisions, the
  server-side authz remains the only access decision.
- **Slice 178's spillover discipline.** One slice, one discrete
  fix.

## Canvas references

- `Plans/canvas/05-scopes.md` §5.4 — tenant isolation
- `Plans/canvas/12-ui-fill-in-design-decisions.md` — sidebar
  ordering, "Admin" placement at end
- `docs/audit-log/178-ui-honesty-first-pass.md` — F-178-6

## Dependencies

- **#178** (UI honesty audit harness) — `in-progress`.
- **#164** (`/v1/me` endpoint exposing roles) — `merged`. The
  current-user shape the sidebar reads.
- **#033** (RLS + tenancy plumbing) — `merged`. The role-on-cred
  source of truth this fix consumes via slice 164's read API.

## Anti-criteria (P0 — block merge)

- **P0-186-1.** Does NOT replace the server-side authz gate with a
  client-side check. The admin layout's gate stays exactly as it
  is; this slice ONLY changes the sidebar's render conditional.
- **P0-186-2.** Does NOT introduce a new authentication surface or
  bearer issuance flow.
- **P0-186-3.** Does NOT touch the slice-178 audit harness.
- **P0-186-4.** A failure to fetch `/v1/me` falls back to "hide the
  Admin entry" — failing closed, never open. The current user MAY
  briefly see the entry hidden during the initial fetch; this is
  acceptable (rendering ghost admin chrome would be worse than a
  brief gap).

## Skill mix (3-5)

1. Next.js App Router — server-component sidebar that reads auth
   context from the layout
2. React hooks — `useCurrentUser` client-side fallback for
   slice-164 consumers
3. Playwright spec extension — non-admin bearer + assertion that
   sidebar entries are role-gated
4. Auth-context plumbing — slice 164 + 051 conventions
