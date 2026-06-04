# 108 — `/v1/me/*` profile, preferences, and sessions endpoints

**Cluster:** Backend / API
**Estimate:** 2-3d
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 103 (`/settings` user-facing page), captured as follow-up per continuous-batch policy. Slice 103 ships the `/settings` page per the design captured in slice 093 (mockup `Plans/mockups/settings.html` + design doc `Plans/canvas/12-ui-fill-in-design-decisions.md` §4). Design doc §4 binds the settings sections to backend wire shapes; on `main`, three of those wire shapes do not exist:

| section         | wire source the design wants                                                   | reality on `main`                                                                                        |
| --------------- | ------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| Profile         | `GET /v1/me` → `{ display_name, email, tenant_role, oidc_subject, time_zone }` | no such endpoint; profile data lives in the credential context but is never returned                     |
| Notifications   | `GET /v1/me/preferences` + `PATCH /v1/me/preferences/{event}`                  | `GET /v1/me/notifications` exists but returns audit-note inbox items (slice 029), not preference toggles |
| Active sessions | `GET /v1/me/sessions` + `DELETE /v1/me/sessions/{id}`                          | no session-list or session-revoke endpoints exist on the backend                                         |

Slice 103 therefore ships those sections with localStorage-only persistence + a "server-side X pending" banner per section (slice 098 D1 precedent — labeled empty / placeholder when the endpoint doesn't exist; no fabrication, anti-criterion P0-A5). This slice closes that gap by adding the three endpoint families so the `/settings` page can render real data and persist preferences cross-device.

## Acceptance criteria

### Profile

- [ ] AC-1: `GET /v1/me` returns `{ user_id, tenant_id, display_name, email, tenant_role, oidc_subject, time_zone, is_admin, owner_roles }` derived from the calling credential. No path/query parameters; the caller is always the credential holder.
- [ ] AC-2: The handler reads from `credstore.Credential` (no DB roundtrip) for the fields already in context; `display_name` + `email` + `oidc_subject` + `time_zone` come from a new `user_profiles` table populated by the OIDC sign-in flow.
- [ ] AC-3: `PATCH /v1/me` accepts `{ display_name?, time_zone? }` — only user-mutable fields. Email and OIDC subject are read-only (managed by the IdP). The platform writes an audit-log entry for every PATCH per canvas §4.6.5.

### Preferences

- [ ] AC-4: `GET /v1/me/preferences` returns the caller's notification preference matrix:
      `{ audit_period_assignment: {in_app, email}, policy_ack_due: {in_app, email}, risk_review_overdue: {in_app, email}, control_drift: {in_app, email} }`. Default values (no row in DB) are `{in_app: true, email: true}` for every event.
- [ ] AC-5: `PATCH /v1/me/preferences` accepts a partial matrix and merges (no replacement semantics). The merge is atomic per-event per-channel.
- [ ] AC-6: New table `user_notification_preferences` with `(tenant_id, user_id, event, channel) UNIQUE` and `enabled boolean`. RLS by `tenant_id`.

### Sessions

- [ ] AC-7: `GET /v1/me/sessions` lists the caller's currently-valid session bearers with `{ id, last4, created_at, last_used_at, ip, user_agent, is_current }`. The session a request is on is flagged `is_current: true`. Tokens issued via `/v1/admin/credentials` are NOT included (those have a dedicated UI surface).
- [ ] AC-8: `DELETE /v1/me/sessions/{id}` revokes the named session. If the caller revokes their own current session the response is `204 No Content` and the next request returns 401 (no special-case branch).
- [ ] AC-9: `DELETE /v1/me/sessions` (no `{id}`) revokes ALL the caller's sessions EXCEPT the current one ("sign out other devices").
- [ ] AC-10: Sessions table reuses or extends the existing slice 034 credential storage; the integration test asserts a revoked session bearer returns 401 on the next call.

### Cross-cutting

- [ ] AC-11: Every PATCH and DELETE writes to the audit log (canvas §4.6.5) with `actor = caller user_id`, `action`, and a diff payload.
- [ ] AC-12: RLS enforces tenant isolation on every new table (canvas §5.4 / constitutional invariant #6).
- [ ] AC-13: Integration tests cover the full round-trip for each endpoint family (profile, preferences, sessions) including the Tenant A vs Tenant B RLS-isolation assertion.
- [ ] AC-14: `web/lib/api.ts` adds typed helpers `getMe`, `patchMe`, `getMyPreferences`, `patchMyPreferences`, `listMySessions`, `revokeMySession`. The `/settings` page (slice 103) flips from localStorage fallbacks to the real endpoints.

## Constitutional invariants honored

- **Invariant 2:** Preferences and sessions are not evidence; this is a read/write surface over user state, not the evidence ledger.
- **Invariant 6:** Tenant isolation via PostgreSQL RLS on every new table.
- **Audit log (canvas §4.6.5):** PATCH and DELETE on profile, preferences, and sessions all hit the audit log.

## Canvas references

- `Plans/canvas/12-ui-fill-in-design-decisions.md` §4 (the SCOPE definition)
- `Plans/mockups/settings.html` (the reference design)
- Slice 103 (`docs/issues/103-settings-page.md`) — the consumer

## Dependencies

- **034** (OIDC + admin credentials store) — merged
- **103** (`/settings` page that consumes these endpoints) — landing in the same window; ordering does not matter because slice 103 ships with localStorage fallbacks and a banner pointing here

## Anti-criteria (P0)

- **P0-A1:** Does NOT expose any field that is not derivable from `credstore.Credential` or the new `user_profiles` / `user_notification_preferences` / `user_sessions` tables — no fabrication.
- **P0-A2:** Does NOT introduce a separate auth surface; the existing slice-034 credential model is reused for sessions.
- **P0-A3:** Does NOT roundtrip the OIDC IdP on every `GET /v1/me`; profile fields are mirrored locally on sign-in and refreshed only on the OIDC token refresh boundary.

## Skill mix

- Go HTTP handler in `internal/api/me/`
- sqlc + Atlas migration for `user_profiles` + `user_notification_preferences` (sessions reuse the existing apikeystore tables)
- RLS policies on every new table (slice 002 pattern)
- Integration tests under `internal/api/me/` with the `//go:build integration` tag
- Frontend `web/lib/api.ts` helpers + slice-103 page flip from localStorage to real endpoints

## Notes

- The slice 103 page is already wired to render whatever a backend `getSessionMe()` returns; closing this slice is mostly a matter of extending `getSessionMe()` to call `GET /v1/me` and propagating the new fields through.
- This is a JUDGMENT-eligible slice: the shape of `user_notification_preferences` (per-event-per-channel rows vs JSONB blob), the time-zone default (UTC vs OS-default), and the session-list field set (include IP geo? include device-fingerprint?) are build-time decisions that need a decisions-log entry under `docs/audit-log/108-*-decisions.md`.
