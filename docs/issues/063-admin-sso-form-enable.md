# 063 — Enable `/admin/sso` form save (post-slice-062 wire-up)

**Cluster:** Frontend / admin
**Estimate:** 0.5d
**Type:** AFK

## Narrative

Slice 060 (Admin settings UI) shipped the `/admin/sso` page with the OIDC config form intentionally disabled — a stopgap because the backend save endpoint `PATCH /v1/admin/sso` didn't exist on main at the time. The HITL log explicitly approved the stopgap. Slice 062 (Admin BFF backend endpoints) subsequently landed the endpoint. This slice removes the stopgap: drop the `disabled` attribute, wire the `onSubmit` handler to the existing BFF proxy, surface backend validation errors, and update the HITL log to reflect that the page is now fully functional.

The slice delivers value because the admin can now actually configure SSO from the browser instead of curl-ing the API — completing the v1 self-administration workflow.

## Acceptance criteria

- [ ] AC-1: `/admin/sso` form `disabled` attribute removed; all fields become editable.
- [ ] AC-2: `onSubmit` handler wired to the BFF proxy at `web/app/api/admin/sso/route.ts` (new — proxies to `PATCH /v1/admin/sso`). Existing `web/app/api/admin/credentials/route.ts` pattern is the reference (auth header forwarding, tenant context, etc.).
- [ ] AC-3: Submit-button states: `idle | submitting | success | error`. `submitting` disables the button and shows a spinner. `error` shows the backend's JSON `error` field below the form without clearing the user's input.
- [ ] AC-4: `client_secret` field stays write-only (`type="password"` + `autoComplete="new-password"`) per slice 060 P0; empty submit means "leave existing" per slice 062's contract.
- [ ] AC-5: Successful save displays a transient toast/banner (~3s) and re-fetches the config so the GET-rendered fields reflect the just-saved state (sans `client_secret`).
- [ ] AC-6: Discovery preflight button stays as-is — read-only check, never persists state. Verifies on every click.
- [ ] AC-7: Playwright E2E test extended: bootstrap test in `web/e2e/admin-bootstrap.spec.ts` now reaches `/admin/sso`, fills the form, submits, asserts the GET reload shows the saved provider/client_id/discovery_url (and crucially DOES NOT show `client_secret`).
- [ ] AC-8: `docs/audit-log/admin-ui-review.md` updated with a new section noting "SSO form save enabled by slice 063 on <date>" so the historical record stays coherent.
- [ ] AC-9: `CHANGELOG.md` entry under `[Unreleased]/Changed`.

## Constitutional invariants honored

- **Slice 034 AC-9 (write-once secret):** `client_secret` continues to be write-only at the UI; empty submit means "leave existing" (slice 062's handler-side contract).
- **Slice 035 RBAC:** the admin gate on `/admin/*` (slice 060 AC-7) continues to fire; a non-admin who reaches this page sees the 403 panel.
- **AI-assist boundary:** no AI auto-fills the form. Every submit is a human click.

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.5 (Auth model — admin role)
- `docs/issues/060-admin-settings-ui.md` (the slice this completes)
- `docs/issues/062-admin-bff-backend-endpoints.md` (the slice this binds to)

## Dependencies

- **060** (UI shell)
- **062** (backend `PATCH /v1/admin/sso` + `POST /v1/admin/sso/preflight`)

Both merged on 2026-05-13.

## Anti-criteria (P0 — block merge)

- Does NOT show `client_secret` in the GET-rendered form fields (slice 034 AC-9 contract — write-only).
- Does NOT auto-submit on field-blur or any other event — only the explicit Save button submits.
- Does NOT remove the discovery preflight button (operator-safe pre-save sanity check; slice 060 HITL-approved).
- Does NOT change the backend contract — this is a thin UI slice; if a backend tweak is needed, that's a separate slice.

## Skill mix (3–5)

- Next.js 15 App Router (server component → client component form interaction)
- TanStack Query mutation pattern
- BFF proxy route under `web/app/api/admin/sso/route.ts`
- Playwright E2E (extending the existing bootstrap spec)
