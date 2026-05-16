# 103 — /settings page (per slice 093 mockup)

**Cluster:** Frontend
**Estimate:** 2d
**Type:** AFK
**Status:** `ready`

## Narrative

Implementation slice for `Plans/mockups/settings.html`. Today `/settings` 404s (audit F-4).

Per design doc §4: this is the USER-facing settings page ONLY — profile, appearance, notifications, personal API tokens, active sessions. Tenant-wide / admin settings stay at `/admin/*`. The page has a clear "Tenant administration → /admin" cross-link at the top for users with the admin role.

## Acceptance criteria

### Sections (5 panels)

- [ ] AC-1: Profile section reading `GET /v1/me` — display name (editable PATCH), email (read-only OIDC source), tenant role (read-only badge), OIDC subject (read-only).
- [ ] AC-2: Appearance section — theme picker (light / dark / system). Persisted to localStorage + `PATCH /v1/me { theme }` if the backend supports it (verify; if not, localStorage-only is fine and the spillover slice is "server-side theme persistence").
- [ ] AC-3: Notifications section reading `GET /v1/me/notifications` — per-event in-app + email toggles for: audit-period assignment, policy ack due, risk review overdue, control drift. PATCH per toggle.
- [ ] AC-4: API tokens section reading `GET /v1/admin/credentials` scoped to the calling user — list of personal credentials with last-4, scope predicate, allowed kinds, issued_at, last_used_at. `Issue new token` button → modal → plaintext shown ONCE then never re-displayed. `Revoke` button per row with confirm.
- [ ] AC-5: Active sessions section — list of currently signed-in browsers; `Sign out` button per row + `Sign out all other sessions` global button. Reads from a session-list endpoint (verify exists; if not, this section becomes a placeholder + spillover slice for the endpoint).

### Cross-business

- [ ] AC-6: Cross-link at the top: "Tenant administration → /admin". Visible to admin role only; hidden for non-admins (use existing `getSessionMe().is_admin` per slice 097 D3 pattern).
- [ ] AC-7: Empty state per design doc §4: settings doesn't need one (always populated by the OIDC-synced profile).

### Tests + quality

- [ ] AC-8: Vitest unit tests for token-issuance modal flow + plaintext-shown-once invariant + theme persistence.
- [ ] AC-9: Playwright spec `web/e2e/settings.spec.ts`: profile renders, theme toggle persists, notification toggle PATCHes, token issuance flow shows plaintext once.

## Constitutional invariants honored

- **Invariant 6:** tenant isolation via BFF; user-scoped reads only.
- **AI-assist boundary:** the page surfaces user prefs; no AI assistance involved.
- **Audit log (canvas §4.6.5):** API token issuance + revocation MUST hit the audit log — verify the backend already does this; if not, spillover slice.

## Canvas references

- `Plans/mockups/settings.html`
- `Plans/canvas/12-ui-fill-in-design-decisions.md` §4 (the SCOPE definition)
- `internal/api/me/notifications.go`
- `internal/api/me/audit_period.go` (if exists)
- `internal/api/admincreds/http.go` (`ListItem`)

## Dependencies

- **093** — merged
- **098** — RECOMMENDED (some shell + form patterns) but not a hard blocker since settings is form-driven not list-driven
- **034** (OIDC + sessions) — merged
- **035** (RBAC) — merged
- **051** (admincreds tenant derivation hotfix) — merged

## Anti-criteria (P0)

- **P0-A1:** Does NOT show tenant-wide settings — admin lives at `/admin/*` (design doc §4 is non-negotiable).
- **P0-A2:** Does NOT re-display API token plaintext after the initial issuance modal closes — plaintext shown ONCE then never again (security-critical invariant).
- **P0-A3:** Does NOT grant non-admin users access to admin-scoped endpoints just because they can open `/settings` (RBAC enforced at the backend, not just at the cross-link visibility).
- **P0-A4:** Does NOT bundle tenant-administration migration (e.g. moving any current `/admin/*` page to `/settings`) — that's a different decision.
- **P0-A5:** Does NOT use vendor-prefixed tokens in test fixtures.

## Skill mix

- Next.js + form-driven page (PATCH per section, no bulk submit)
- shadcn/ui Form + Switch + Dialog primitives
- Token-issuance modal with secure-plaintext-once pattern (slice 062/063 admin-creds precedent)
- Session-list / sign-out endpoint integration (if it exists)

## Notes

- The plaintext-once invariant (AC-4 / P0-A2) is security-critical. Look at slice 062 (admin BFF endpoints) + 063 (admin settings UI) for the existing precedent — match that flow exactly.
- For AC-2 theme persistence: localStorage is the v1 fallback. Server-side theme persistence is a nicety, not a v1 requirement. Don't expand scope.
- For AC-5 session listing: if the endpoint doesn't exist, the section renders a "Coming soon — session management lands in a follow-up slice" stub + a spillover slice gets filed for the backend.
