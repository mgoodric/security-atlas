# 191 — SDK migration to `client_credentials` × 4 languages + CLI device-code + legacy bearer-token retirement

**Cluster:** Backend / SDK / CLI
**Estimate:** 3-4d
**Type:** JUDGMENT
**Status:** `not-ready` (fifth slot in the auth-substrate-v2 spine; gate: 190 merged)

## Narrative

Slices 187 (foundation), 188 (`/oauth/token`), 189 (`/oauth/authorize` + PKCE + frontend), 190 (JWT middleware + revoke + introspect) shipped the OAuth AS issuance + consumption paths. Slice 191 **migrates every callsite** — the four SDKs (Go, Python, TypeScript, Java v2) and the CLI — from the legacy API-key bearer flow (slice 003 Evidence SDK + slice 034's credstore) to OAuth `client_credentials`. Once the migration is complete, the legacy bearer-token middleware retires from `internal/api/httpserver.go`.

**Sequence per consumer:**

1. **Go SDK** (`pkg/sdk-go/`): adds `oauth.NewClient(client_id, client_secret, issuer_url) (*Client, error)` that acquires a JWT via `POST /oauth/token` and refreshes before expiry (60s clock skew). Existing API-key constructor stays as `pkg/sdk-go/legacy/` with a deprecation shim until v3.
2. **Python SDK** (`sdk/python/pyatlas/`): mirror of Go SDK. `pyatlas.OAuthClient(client_id, client_secret, issuer_url)`.
3. **TypeScript SDK** (`sdk/typescript/`): mirror.
4. **Java SDK** (`sdk/java/` — v2 placeholder; if it doesn't exist as a real package yet, file as spillover; this slice ships only Go/Python/TS if Java doesn't exist).
5. **CLI device-code flow** (`cmd/atlas-cli/cmd/login.go`): NEW command `atlas login` implementing **RFC 8628 Device Authorization Grant**. CLI POSTs to `/oauth/device_authorization` (NEW endpoint in this slice), gets a verification_uri + user_code, prints "Visit <url> and enter <code>", polls `/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code` until the user completes. Stores the resulting JWT in `~/.config/atlas/credentials.json`.
6. **API-key migration tool** (`cmd/atlas-cli/cmd/oauth_migrate.go`): NEW command `atlas oauth migrate-api-key <api_key>` that issues a new OAuth client_credentials pair scoped to the same identity + roles as the API key. Operators run this to migrate existing integrations.
7. **Legacy bearer-token middleware retirement**: slice 034's bearer middleware is REMOVED from `internal/api/httpserver.go`. Remaining requests carrying legacy API keys (which are now redundant after migration) return 410 Gone with a body explaining the migration path.

**What this slice ships in detail:**

1. **Device Authorization Grant endpoint** `POST /oauth/device_authorization` (RFC 8628 §3.1): accepts `client_id` form param, returns RFC 8628 §3.2 response (`device_code`, `user_code`, `verification_uri`, `verification_uri_complete`, `expires_in`, `interval`). Generates `device_code` (64B random base64url) + `user_code` (8-char ABCDEFGHJKLMNPQRSTUVWXYZ23456789 — no ambiguous chars). Stores in NEW `oauth_device_codes` table with TTL 15 min. The CLI displays the user_code; the user navigates to verification_uri, logs in via OIDC, sees a prompt "Approve device code: ABCD-1234", clicks Approve. The CLI's polls then succeed.
2. **Device-code redemption** extends slice 188's `/oauth/token` with `grant_type=urn:ietf:params:oauth:grant-type:device_code`. Form params: `device_code`, `client_id`. If user hasn't approved yet → `{"error":"authorization_pending"}` 400 (RFC 8628 §3.5). If approved → mint JWT scoped to the approving user's identity. One-shot via `approved_at IS NOT NULL` + `consumed_at IS NULL` + `UPDATE ... RETURNING`.
3. **Device approval UI** at `web/app/oauth/device/page.tsx`: route `/oauth/device` (optionally with `?user_code=<code>` for the verification_uri_complete shortcut). Authenticated via slice 034 OIDC session. Renders the user_code + an Approve/Deny button pair. On Approve, calls a new internal endpoint `POST /oauth/device_authorization/approve` with `user_code` + the OIDC-authenticated user's identity → server updates `oauth_device_codes` row.
4. **`oauth_device_codes` table** (NEW migration): `device_code TEXT PK · user_code TEXT UNIQUE · client_id TEXT · expires_at · approved_at NULL · approved_by_user_id UUID NULL · approved_by_idp_issuer TEXT NULL · approved_by_idp_subject TEXT NULL · approved_by_current_tenant_id UUID NULL · approved_by_available_tenants UUID[] · approved_by_roles JSONB · approved_by_super_admin BOOL · consumed_at NULL`. Index `(user_code)`, `(expires_at)`. NOT tenant-scoped.
5. **Go SDK OAuth client** `pkg/sdk-go/oauth/`: `NewClient(client_id, client_secret, issuer_url) (*Client, error)` + `Token(ctx) (string, error)` (returns cached JWT, refreshes if `time.Until(exp) < 60s`). Reuses slice 187's `jwt` package for claim types (via a public re-export at `pkg/sdk-go/oauth/claims.go`).
6. **Python SDK OAuth client** `sdk/python/pyatlas/oauth.py`: `OAuthClient(client_id, client_secret, issuer_url)` + `token()` method. Same semantics.
7. **TypeScript SDK OAuth client** `sdk/typescript/src/oauth.ts`: `OAuthClient(client_id, client_secret, issuer_url)` + `getToken()` method. Same semantics.
8. **CLI `atlas login`** (RFC 8628 device-code flow): non-interactive after the initial browser tab. Stores JWT in `~/.config/atlas/credentials.json` (mode 0600).
9. **CLI `atlas oauth migrate-api-key <key>`**: looks up the API key's identity, issues a corresponding OAuth client, prints client_id + plaintext secret ONCE. Operator records the new credentials, then revokes the old API key separately.
10. **Legacy bearer-token middleware removal**: slice 034's `credstore`-backed middleware is DELETED from `internal/api/httpserver.go`. The `credstore` package itself stays for now (drained later). Requests carrying an unknown bearer return 401; requests carrying a legacy API key shape (recognized by prefix or format) get a special 410 response with migration guidance.

**SCOPE DISCIPLINE — what's deliberately out:**

- Refresh-token grant — v3 deferred.
- DPoP — v3 deferred.
- mTLS client auth — v3 deferred.
- Java SDK if no `sdk/java/` directory exists today (file as spillover; the slice ships Go/Python/TS).
- Frontend tenant-switcher dropdown — slice 192.
- Migrating from API-key-style credstore storage to OAuth-client-style — out of scope; the credstore package stays. Removing it is v3.
- Slice 003 Evidence SDK push protocol change — the wire shape stays `Authorization: Bearer <token>`; only the token acquisition flow migrates. No proto changes.

## Threat model

**S — Spoofing.** Forged device_code or user_code.

- Mitigation: device_code is 64B random (512 bits entropy). user_code is 8-char alphanumeric (~10^12 combinations) with 15-min TTL and rate-limited polling — brute-force infeasible within TTL.

**T — Tampering.** CLI poll loop intercepted; attacker swaps the device_code.

- Mitigation: CLI uses HTTPS to the atlas instance. Device_code is tied to the client_id; mismatched client_id at redemption → 400.

**R — Repudiation.** Device approvals need audit trail.

- Mitigation: every approval writes to `oauth_token_exchanges`-style log (or extend that table; engineer picks).

**I — Information disclosure.** API-key migration command echoes secrets.

- Mitigation: stdout-only ONCE pattern, same as slice 188's `atlas oauth issue-client`. Document in operator guide that secret is unrecoverable.

**D — Denial of service.** Device-code endpoint floods.

- Mitigation: per-client_id rate limit at `/oauth/device_authorization` (default 30/min/client). Per-device_code rate limit on the token-poll path (default 1/5s — RFC 8628 §3.5 documents 5s as the default `interval`).

**E — Elevation of privilege.** Device-code approved by user A, redeemed for a JWT scoped to user B.

- Mitigation: `oauth_device_codes` row stores the approving user's identity. Redemption uses that identity for the minted JWT; client_id from the redemption request only validates that the right CLI is asking, not whose identity to mint for.

**Verdict:** `has-mitigations`. The migration semantics are the hard part; the device-code flow is well-trodden territory.

## Acceptance criteria

### Device Authorization Grant endpoint

- **AC-1.** NEW handler `internal/api/oauth/device_authorization.go` mounted at `POST /oauth/device_authorization`. Accepts `application/x-www-form-urlencoded`.
- **AC-2.** Form params: `client_id` (required). Validates against `oauth_clients`.
- **AC-3.** Generates `device_code` (64B random base64url) + `user_code` (8-char unambiguous alphabet ABCDEFGHJKLMNPQRSTUVWXYZ23456789, formatted as `ABCD-1234`). Inserts into `oauth_device_codes` with `expires_at = now() + 15min`.
- **AC-4.** Returns RFC 8628 §3.2 response: `{"device_code":"<>", "user_code":"ABCD-1234", "verification_uri":"<atlas-instance>/oauth/device", "verification_uri_complete":"<atlas-instance>/oauth/device?user_code=ABCD-1234", "expires_in":900, "interval":5}`.

### Device-code redemption (extends slice 188's `/oauth/token`)

- **AC-5.** Slice 188's `/oauth/token` extended with `grant_type=urn:ietf:params:oauth:grant-type:device_code` path. Form params: `device_code`, `client_id`.
- **AC-6.** Looks up row by `device_code`. If not found → 400 + `error=invalid_grant`. If expired → 400 + `error=expired_token` (RFC 8628 §3.5). If consumed → 400 + `error=invalid_grant`. If not yet approved → 400 + `error=authorization_pending`.
- **AC-7.** On approved + not-consumed: UPDATE row to set `consumed_at = now()` via `RETURNING` (one-shot). Mints JWT with the approving user's identity.
- **AC-8.** Per-device_code poll rate limit: at most 1 request per 5 seconds (RFC 8628 §3.5 default). 429 + `error=slow_down` on violation.

### Device approval UI

- **AC-9.** NEW route `web/app/oauth/device/page.tsx`. Authenticated via slice 034 OIDC session (redirects to login if not).
- **AC-10.** Reads `?user_code=<code>` query param (optional pre-fill from `verification_uri_complete`). Renders input box if absent.
- **AC-11.** Approve button POSTs to NEW `/oauth/device_authorization/approve` endpoint with `user_code` + (server pulls user identity from session).
- **AC-12.** NEW handler `/oauth/device_authorization/approve` (internal — not in the RFC) updates the `oauth_device_codes` row: sets `approved_at = now()` + the OIDC user's full identity (idp_issuer, idp_subject, current_tenant_id, available_tenants, roles, super_admin). Returns 200 on success.
- **AC-13.** Deny button POSTs to `/oauth/device_authorization/deny` — sets `consumed_at = now() + interval '1s'` (effectively rejects the code). Returns 200.

### `oauth_device_codes` migration

- **AC-14.** NEW migration creates `oauth_device_codes` per Narrative §4. Reversible.

### Go SDK OAuth client

- **AC-15.** NEW package `pkg/sdk-go/oauth/` with `Client` type. Methods: `Token(ctx) (string, error)` returns cached JWT, refreshes if `time.Until(exp) < 60*time.Second`.
- **AC-16.** Acquires initial token via `POST /oauth/token` with `grant_type=client_credentials`.
- **AC-17.** Background refresh: if a call to `Token()` finds the cached token within 60s of expiry, refreshes synchronously.
- **AC-18.** Thread-safe: concurrent `Token()` callers share a single refresh via `sync.Mutex` + condition variable.

### Python SDK OAuth client

- **AC-19.** NEW module `sdk/python/pyatlas/oauth.py` with `OAuthClient` class. Methods: `token()` returns cached JWT, refreshes if within 60s of expiry. Thread-safe via `threading.Lock`.

### TypeScript SDK OAuth client

- **AC-20.** NEW module `sdk/typescript/src/oauth.ts` with `OAuthClient` class. Methods: `getToken()` returns cached JWT, refreshes if within 60s of expiry.

### CLI device-code flow

- **AC-21.** NEW command `atlas login` (`cmd/atlas-cli/cmd/login.go`). Reads `--issuer` flag (or `ATLAS_ISSUER` env). Calls `/oauth/device_authorization` to get user_code + verification_uri. Prints user-facing instructions: `"Visit <verification_uri_complete> and approve code <user_code>"`. Polls `/oauth/token` with `grant_type=device_code` every `interval` seconds.
- **AC-22.** On success, stores JWT in `~/.config/atlas/credentials.json` with mode 0600. JSON shape: `{"access_token":"<>","expires_at":"<ISO>","issuer":"<>"}`.
- **AC-23.** Subsequent CLI commands read this file and add `Authorization: Bearer <jwt>` to their HTTP calls. (Most CLI subcommands already do this; this slice's change is the source of the token.)

### API-key migration command

- **AC-24.** NEW command `atlas oauth migrate-api-key <api_key>` (`cmd/atlas-cli/cmd/oauth_migrate.go`). Looks up the API key in `credstore` (slice 034). Issues a new OAuth client via `oauthclient.Issue` with the API key's identity + roles + tenant grants seeded into a new mapping table OR via the user's `user_roles` (engineer picks; document in decisions log).
- **AC-25.** Prints `client_id: <uuid>` + `client_secret: <base64>` to stdout ONCE.
- **AC-26.** Does NOT auto-delete the source API key. The operator runs `atlas credstore delete <key>` separately after confirming the new credentials work.

### Legacy bearer-token middleware retirement

- **AC-27.** Slice 034's bearer-token middleware REMOVED from `internal/api/httpserver.go`. Only the JWT middleware (slice 190) handles `/v1/*` auth.
- **AC-28.** A new "deprecation responder" middleware mounted on `/v1/*` (before JWT middleware) detects legacy API-key-shape bearers (the prefix patterns are stable from slice 034). If detected, returns 410 Gone + body `{"error":"api_key_deprecated", "migration_url":"<atlas-instance>/docs/migration/oauth"}`.
- **AC-29.** The `credstore` package itself is NOT deleted in this slice. v3 spillover.

### Tests + docs

- **AC-30.** Integration tests for device-code flow end-to-end: device_authorization → approval → token redemption → JWT validates on `/v1/*`.
- **AC-31.** SDK unit tests for Go/Python/TS oauth clients: token caching, refresh-before-expiry, concurrent access.
- **AC-32.** CLI test for `atlas login` against a mock OAuth endpoint.
- **AC-33.** Integration test for the legacy 410 responder: send a legacy-shape bearer, assert 410 + migration_url in body.
- **AC-34.** Discovery doc updated: `device_authorization_endpoint` advertised; `grant_types_supported` includes the device-code grant URN.
- **AC-35.** Operator-facing migration doc at `docs-site/docs/migration/oauth.md` (or similar) — what operators do to migrate.
- **AC-36.** JUDGMENT decisions log at `docs/audit-log/191-sdk-migration-decisions.md`: D1 (Java in scope or spillover), D2 (API-key migration identity-mapping shape), D3 (credstore retirement timing), D4 (deprecation responder vs hard removal), D5 (CLI device-code interval default).

## Constitutional invariants honored

- **Tenant isolation** (invariant #6): all SDK callsites continue to obtain tenant-scoped tokens via OAuth flows.
- **Audit trail** for credential lifecycle events.

## Canvas references

- OQ #21 RESOLVED (Reading D) — this slice closes the SDK side of the commitment.
- `docs/adr/0003-oauth-authorization-server.md` (slice 187).
- Slice 003 (Evidence SDK push protocol) — wire shape unchanged; token acquisition flow migrates.
- Slice 034 (OIDC RP / credstore) — bearer middleware retired.

## Dependencies

- **#187, #188, #189, #190** — all auth-substrate-v2 spine slices must be `merged`. **Gate: 190 merged.**
- **#003** Evidence SDK push protocol (no breaking change; the wire still uses `Authorization: Bearer <token>`).
- **#034** OIDC RP + credstore — this slice retires the bearer middleware mounted by 034.

## Anti-criteria (P0 — block merge)

- **P0-191-1.** Does NOT modify the wire protocol on `/v1/evidence:push` or any other `/v1/*` endpoint. Only the token acquisition flow changes.
- **P0-191-2.** Does NOT delete the `credstore` package or the `users`/`user_credentials` tables. v3 spillover.
- **P0-191-3.** Legacy 410 responder MUST include a migration URL in the response body. Operators encountering this response need to know what to do.
- **P0-191-4.** Device-code user_code MUST use the unambiguous alphabet (no 0/O/1/I/L). Confusable chars are a UX failure mode.
- **P0-191-5.** CLI `atlas login` MUST persist credentials with mode 0600. World-readable credential files are unacceptable.
- **P0-191-6.** Does NOT implement refresh-token grant. v3 deferred.
- **P0-191-7.** Does NOT implement DPoP. v3 deferred.
- **P0-191-8.** Device-code redemption MUST be one-shot via `UPDATE ... RETURNING`. Replay vulnerabilities are unacceptable in any token-exchange path.
- **P0-191-9.** API-key migration tool prints secret to stdout ONCE. NEVER persists plaintext anywhere.
- **P0-191-10.** Does NOT enable the JWT middleware on `/oauth/*` or `/.well-known/*` paths (the unauthenticated-route discipline from slice 190 is sacrosanct).
- **P0-191-11.** The deprecation responder MUST be FAIL-CLOSED: legacy bearer → 410, no fallthrough to any other auth mechanism. The migration deadline is when this slice merges.

## Skill mix (3-5)

- `grill-with-docs`
- `tdd`
- `database-designer` (oauth_device_codes migration)
- `security-review` (auth retirement is high-stakes — easy to ship a regression)
- `simplify`
- `ship-gate`
- `changelog-generator`

## Notes for the implementing agent

### This slice is THE retirement

Slice 190's `internal/api/httpserver.go` keeps the legacy bearer-token middleware as a coexisting path. THIS slice removes it. Read slice 190's coexistence shape first; the cutover order in this slice's PR matters:

1. Add the 410 responder (catches in-flight legacy clients).
2. Remove the legacy middleware mount.
3. Verify the JWT middleware alone covers `/v1/*`.

Reverse that order = auth bypass window during deployment.

### Java SDK might not exist

`sdk/java/` may be a placeholder directory. Check before scoping work. If no real package exists, file as spillover (the slice ships Go/Python/TS only) and document in decisions log D1.

### Device-code UX

The default `interval` is 5 seconds (RFC 8628 §3.5). Don't shorten it without measuring DB load — every poll is an indexed lookup but it adds up.

### Spillover candidates

- Java SDK if `sdk/java/` doesn't exist today.
- credstore package retirement (v3).
- Refresh-token grant (v3).
- DPoP (v3 optional).
- mTLS client auth (v3).

### Provenance

Filed 2026-05-21 as auth-substrate-v2 spine slot 5.
