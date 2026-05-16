# 110 — BFF forwards atlas_session cookie alongside bearer for /v1/me/sessions current-flag

**Cluster:** Frontend / BFF
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 108 (`/v1/me/*` profile + preferences + sessions
endpoints). Slice 108 D4 documented that `GET /v1/me/sessions` cannot
flag the current session row when the request is on the BFF-bearer
auth path (cookie name `sa_session_token`, carries an `api_keys`
bearer), because the slice 034 `sessions` table is wired to
`/auth/*` (OIDC login) but not to `/v1/*` request authentication.

The slice 108 handler reads the `atlas_session` cookie if present and
flags the matching session row as `is_current: true`; when the cookie
is absent every row stays unflagged. The honest fallback works but
the `/settings` page's "current session" UX requires the cookie to be
present.

This spillover slice closes the gap: the BFF forwards BOTH
`sa_session_token` (the bearer) AND `atlas_session` (the OIDC
session id) on every `/v1/me/sessions` request so the platform
handler can match the row.

## Acceptance criteria

- [ ] AC-1: `web/lib/auth.ts` constants extended with an
      `OIDC_SESSION_COOKIE = "atlas_session"` export.
- [ ] AC-2: BFF route `/api/me/sessions` reads both cookies and
      forwards `atlas_session` as a `Cookie: atlas_session=<value>`
      header on the upstream `/v1/me/sessions` GET.
- [ ] AC-3: BFF route `/api/me/sessions/{id}` does the same on
      DELETE (so the per-session revoke flow can correctly tell
      "this is my current session" if needed for the UI confirm
      dialog).
- [ ] AC-4: BFF route `/api/me/sessions` (no id) does the same on
      DELETE (so the "sign out other devices" path knows which
      session to keep).
- [ ] AC-5: An integration test (or vitest BFF route test) asserts
      the upstream fetch carries the cookie header.
- [ ] AC-6: The `/settings` page Sessions section shows a "current"
      badge on the row matching the request's session.

## Constitutional invariants honored

- **Invariant 6**: No new tables; no RLS change. Pure cookie
  forwarding at the BFF layer.

## Dependencies

- **034** (slice 034 OIDC + sessions table) — merged
- **108** (slice 108 `/v1/me/sessions` handler that reads the
  `atlas_session` cookie) — landing in the same window

## Anti-criteria (P0)

- **P0-A1**: Does NOT change the bearer-auth semantic. The
  `sa_session_token` bearer remains the primary auth carrier; the
  `atlas_session` cookie is informational ONLY.
- **P0-A2**: Does NOT forward `atlas_session` on ANY route other
  than `/api/me/sessions*`. Cookie scope is narrow; we do not leak
  the OIDC session id to handlers that don't need it.

## Skill mix

- TypeScript BFF route edit in `web/app/api/me/sessions/*`
- vitest BFF route test
- Optional Playwright spec for the current-badge UX

## Notes

If a future slice cuts the BFF over to OIDC-session bearer (replacing
the `sa_session_token` api_keys bearer entirely), this slice's
cookie-forwarding becomes redundant. That cutover is a much larger
slice (auth model migration); this spillover is the minimal fix for
the slice 108 UX gap.
