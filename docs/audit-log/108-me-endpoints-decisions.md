# Slice 108 — /v1/me/\* endpoints · decisions log

> JUDGMENT slice — Claude resolved the design questions inline and recorded
> them here per the slice-development workflow (CLAUDE.md ÂÂ "AI-assist
> boundary"). No human sign-off gate on the build-time calls below; the
> maintainer iterates post-deployment if any decision proves wrong.

## Context

Slice 103 (`/settings` user-facing page) shipped three sections that
require backend endpoints that did not exist on `main`:

- Profile (display name, email, time_zone, IdP subject) — `GET /v1/me`
- Notifications (per-event-per-channel toggles) — `GET/PATCH /v1/me/preferences`
- Active sessions (per-device session list + revoke) — `GET/DELETE /v1/me/sessions`

Slice 103 worked around the gap with localStorage fallbacks and
"server-side X pending" banners (slice 098 D1 precedent). Slice 108
closes the gap so the slice 103 page can render real data and persist
cross-device.

## Design decisions

### D1 — Reuse `users` table; do NOT create a sibling `user_profiles`

The slice file AC-2 proposes a new `user_profiles` table holding
`display_name + email + oidc_subject + time_zone`. The existing slice
034 `users` table already carries `display_name + email + idp_issuer +
idp_subject` and is populated by the OIDC callback's
`UpsertUserByIdpSubject` query. Only `time_zone` is missing.

**Decision:** add `time_zone TEXT NOT NULL DEFAULT ''` to `users` via an
additive ALTER. No new `user_profiles` table.

Rationale:

- The slice's own anti-criterion P0-A1 forbids fabrication beyond what
  the existing tables hold. A sibling 1:1 table would be the opposite —
  inventing storage for data we already have.
- A 1:1 sibling table breaks the principle of "one canonical user row
  per (tenant, identity)" and creates a perpetual join cost on every
  `GET /v1/me`.
- Migration size: one `ALTER TABLE … ADD COLUMN IF NOT EXISTS time_zone
TEXT NOT NULL DEFAULT ''` vs three migrations (create + RLS + grants)
  for the sibling table.
- The slice 034 `UpsertUserByIdpSubject` INSERT path is unaffected by
  the additive column (NULL default).

This is a deviation from the slice file's wording but honors its
intent (one canonical profile surface, no fabrication). The slice
file's AC-2 is reworded inline in the implementation to point at
`users` instead.

### D2 — time_zone default is `''` (empty = browser-derived)

Three candidates for the default:

1. `'UTC'` — backend lies; user calendar renders in the wrong zone.
2. OS-default — impossible from the backend (we don't see the OS).
3. `''` (empty) — honest "not set; fall back to browser
   `Intl.DateTimeFormat().resolvedOptions().timeZone`."

**Decision:** option 3. The wire returns `time_zone: null` for empty;
PATCH validates non-empty values via Go `time.LoadLocation` so a typo
gets a 400 at write time instead of confusing the renderer.

### D3 — Preferences storage = per-event-per-channel rows (NOT JSONB blob)

Two candidates for `user_notification_preferences`:

1. **JSONB blob** — one row per user, JSONB column carries the matrix.
   Read = one SELECT. PATCH = read-modify-write the whole blob.
2. **Per-cell rows** — one row per `(tenant, user, event, channel)`.
   Read = N rows (max 8 with current taxonomy). PATCH = N atomic
   UPSERTs.

**Decision:** option 2 (per-cell rows).

Rationale:

- AC-5 says PATCH MERGES (not replaces). Per-cell UPSERTs are the
  natural primitive of merge; JSONB read-modify-write has a
  lost-update race when two PATCHes interleave.
- Matches the precedent set by `policy_acknowledgments` /
  `evidence_freshness` — every per-user-per-X surface in this codebase
  is a row, not a blob.
- CHECK constraints enumerate the allowed `(event, channel)` tuples
  so a typo at the API layer fails at the DB layer (defense in depth
  against the userprefs.Upsert whitelist).
- Cell count is bounded: 4 events × 2 channels = 8 max per user.
  Read cost is trivial.

Alternatives rejected:

- JSONB blob — see merge race above.
- Single TEXT column with serialized JSON — same problem as JSONB,
  no Postgres JSON-path indexing.

### D4 — `is_current` flag uses atlas_session cookie when present; degrades gracefully

The slice 034 `sessions` table is wired to `/auth/*` (OIDC login)
but NOT to `/v1/*` request authentication — `/v1/*` uses the
`api_keys` bearer (HMAC) carried in the `sa_session_token` cookie by
the BFF. A `/v1/me/sessions` call therefore has no direct way to know
"which `sessions` row am I currently on" because the request isn't
authenticated against a session row — it's authenticated against an
api_keys row.

Three candidates:

1. **Strict** — always return `is_current: false` (we can't know).
   Honest but bad UX.
2. **Cookie-bridged** — if the request carries an `atlas_session`
   cookie, match the row by id and flag `is_current`; otherwise leave
   every row unflagged.
3. **Bearer cookie forwarding** — extend the BFF to forward both
   `sa_session_token` (bearer) AND `atlas_session` (session id) to
   the platform on every /v1/\* request, so the platform sees the
   session id alongside the bearer.

**Decision:** option 2 (cookie-bridged). Option 3 is the right
long-term fix but is out of scope for this slice's surface; a
spillover slice will file the BFF cookie-forwarding work.

Anti-criterion P0-A1 honored: we don't fabricate a "current" flag.

### D5 — New `me_audit_log` table + extend `admin_audit_log_v` view

The slice 062 admin audit-log view unifies seven per-domain
audit-log tables (decision / evidence / exception / feature flag /
artifact / sample / audit-period). Each domain owns its own table;
the view's `CREATE OR REPLACE` makes adding a branch a one-migration
operation.

**Decision:** add `me_audit_log` as the eighth branch.

Rationale:

- The `/me/*` mutations (profile, preferences, sessions) are a
  distinct domain from any of the seven existing branches. Squishing
  into one of them (e.g. piggybacking on `decision_audit_log`)
  would break the admin filter UX (`?event_type=profile.update`
  makes no sense in the authz decisions table) and the admin filter
  semantic.
- Migration cost: one new table + one `CREATE OR REPLACE VIEW` =
  one file.
- Append-only via the slice 062 invariant: SELECT + INSERT RLS
  policies only; no UPDATE / DELETE policies and no UPDATE / DELETE
  GRANT on the application role.

### D6 — Cross-user session revoke is OUT OF SCOPE

Slice 108 surface is `/v1/me/*` — the caller is acting on themselves.
Admin "revoke another user's session" is a separate admin slice. The
SQL WHERE clause's `user_id = $caller_user_id` guard ensures a
cross-user id never touches another user's row; we return 404 (not 403) to avoid the existence-oracle.

### D7 — 400 on unknown preference event/channel keys; no silent ignore

The schema's CHECK constraint enforces this at the storage layer,
but a typo client → server should fail with an actionable 400 BEFORE
the SQL CHECK fires (better error message + faster feedback). The
`userprefs.Store.Upsert` pre-flight check validates every key in the
input before the first write.

### D8 — Empty-diff PATCH skips audit-log write

An empty-diff PATCH `/v1/me` carries no security-relevant change.
Writing a `me_audit_log` row for it adds noise to admin review with
zero signal. The handler diffs the request body against the current
row; if no field changed, skip both the UPDATE and the audit-log
INSERT.

Anti-criterion ISC-A5 encodes this.

### D9 — Bridge `cred.UserID` from `api_keys.issued_by` when present

Discovered during implementation: the `apikeystore.credentialFromRow`
function set `cred.UserID = credID` (the api_keys id, NOT a real
users.id). Bootstrap admin credentials never had an `issued_by`; OIDC
flow set it but the credential layer threw it away.

**Decision:** thread `row.IssuedBy` through to `cred.UserID` when
present; fall back to `credID` (the existing slice 023 hack) when
not. Handlers that need a real users.id check `uuid.Parse(cred.UserID)`
and degrade gracefully for bootstrap credentials (return a synthetic
"API key" profile per P0-A1).

This is the minimal fix that unblocks the slice; the slice 023
`RebindBearerUserIDForTests` hack continues to work for integration
tests that mint a bootstrap credential and bind it to a seeded users
row.

## Spillover slices filed

- **110** — BFF forwards `atlas_session` cookie alongside
  `sa_session_token` bearer so `/v1/me/sessions` can flag the current
  session for bearer-only request paths.

## sqlc regen reset (slice 109 follow-through)

`sqlc generate` re-applied the slice 109 hand-narrows
overwrite. The two narrows (`policies.sql.go` ListPoliciesWithAckRate

- `scf_anchors.sql.go` ListSCFAnchorsForVersionWithStateRow /
  ListSCFAnchorsLatestWithStateRow) were re-applied verbatim from the
  slice 109 source. No new hand-narrows introduced by this slice.

## Anti-criteria honored

- **P0-A1** (no fabrication): GET /v1/me returns only what `users` +
  `credstore.Credential` hold; bootstrap credentials get a synthetic
  "API key" profile labeled as such.
- **P0-A2** (reuse slice 034 cred model): sessions table reused
  verbatim; new ListSessionsForUser / RevokeSessionForUser queries
  layered on top.
- **P0-A3** (no IdP roundtrip per GET /v1/me): GET reads from `users`
  - credential only; no `oidc_idp_configs` lookup or IdP token call.
- **ISC-A1** (no new user_profiles table): see D1.
- **ISC-A3** (no cross-user session listing): see D6.
- **ISC-A4** (no silent ignore of unknown preference keys): see D7.
- **ISC-A5** (no audit-log row on empty-diff PATCH): see D8.
