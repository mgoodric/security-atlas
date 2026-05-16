# 105 — Risk-create UI for the /risks empty-state CTA

**Cluster:** Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced during slice 100, captured as follow-up per continuous-batch policy.

Slice 100 ships the `/risks` list view per `Plans/mockups/risks.html`. Per design doc §2 a true zero-state empty surface needs an `Add first risk` primary CTA (the most common path on a fresh install — most tenants start with zero risks). The slice-019 backend exposes `POST /v1/risks` for risk creation but no frontend surface owns the form: there is no `/risks/new`, no `/admin/risks/new`, and the slice-019 dashboard panels are read-only. Slice 100 therefore wires the empty-state CTA to `/admin` (the program-admin landing) as a placeholder so the path is at least non-404'ing, with this slice filed to lift the placeholder to a real risk-create form.

The slice is intentionally scoped tight: one route, one form, one POST. It does NOT extend the API, does NOT touch the existing risk-hierarchy view (slice 056), and does NOT add bulk-import / CSV upload (which would be a separate slice if a tenant has hundreds of risks to migrate in).

## Acceptance criteria

- [ ] AC-1: New page at `web/app/(authed)/risks/new/page.tsx` renders a server-component form bound to the slice-019 `POST /v1/risks` shape.
- [ ] AC-2: Required fields enforced client-side: `title`, `category`, `methodology` (default `nist_800_30`), `treatment` (default `mitigate`), `treatment_owner`, and a 5x5 inherent score (likelihood + impact dropdowns 1..5).
- [ ] AC-3: BFF route `web/app/api/risks/route.ts` adds a `POST` handler that forwards the bearer cookie + JSON body to `/v1/risks` upstream (slice-098 / slice-100 BFF pattern).
- [ ] AC-4: On success the form redirects to `/risks` and the new row appears in the list (TanStack Query cache invalidation on `["risks", "list"]`).
- [ ] AC-5: On validation error (4xx from upstream) the form surfaces the upstream error message inline without losing the user's input.
- [ ] AC-6: Slice 100's `/risks` empty-state CTA is re-pointed from `/admin` to `/risks/new`.
- [ ] AC-7: Vitest covers the BFF POST handler (success + 4xx propagation + missing-cookie 401).
- [ ] AC-8: Playwright spec `web/e2e/risks-create.spec.ts` quarantined per slice-079 pattern until slice 082 seed harness lands.

## Constitutional invariants honored

- **Invariant 6 (tenant isolation):** the BFF forwards bearer cookie; the platform enforces tenant isolation via RLS on the insert path (slice-033 pattern).
- **AI-assist boundary:** no AI on this surface — pure CRUD form.

## Canvas references

- `Plans/canvas/02-primitives.md` (Risk shape)
- `Plans/canvas/06-risk.md` §6.2 (5x5 grid)
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §2 (empty-state CTA contract)
- `internal/api/risks/handlers.go` (`createReq` shape — the form binds to this)
- Slice 019 (POST /v1/risks)
- Slice 100 (/risks list view + placeholder CTA)

## Dependencies

- **019** — merged (POST /v1/risks)
- **100** — IN-PROGRESS at time of writing; this slice flips to `ready` once 100 lands

## Anti-criteria (P0 — block merge)

- Does NOT extend the slice-019 backend — write path is unchanged.
- Does NOT add bulk-import / CSV upload — that is a separate future slice.
- Does NOT touch /risks/hierarchy — slice 100 P0-A1 still applies.
- Does NOT invent risk fields not on `createReq` — the form is a direct binding to the existing wire shape.

## Skill mix

- Next.js App Router server component + client form
- TanStack Query mutation + cache invalidation
- BFF POST handler (slice-094 + slice-098 + slice-100 cookie-to-bearer pattern)
- shadcn form primitives (Input, Select, Button)

## Notes

The slice is filed `not-ready` because the F-3 closure in slice 100 sequenced the sidebar realignment ahead of the risk-create form. Once slice 100 merges, this slice flips to `ready` and the empty-state CTA gets its real home.
