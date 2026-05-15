# 073 — first-time login UX — decisions log

Slice 073 is `Type: JUDGMENT` — credential surface design. This log
records the subjective build-time judgment calls made while landing
the first-time-login UX + bootstrap-token discoverability features,
in the JUDGMENT-slice format (Decisions made · Revisit once in use ·
Confidence per decision per `Plans/prompts/04-per-slice-template.md`
"Slice types") so the maintainer can re-evaluate them once the slice
is in real use. It does NOT block merge.

## Decisions made

### D1. `_internal` endpoint prefix vs first-class admin-only route

**Chosen: drop the `_internal` prefix; use a bearer-gated route at
`POST /v1/install/mark-first-signin`.**

The issue narrative hand-waved a `/v1/install/_internal/mark-first-signin`
shape with a comment that "the prefix is NOT access control". On reflection
that is a smell: the prefix would falsely suggest a privilege boundary
the route does not enforce, and any future reader scanning the route
table would have to read the handler body to learn that the gate is
actually the bearer-auth middleware that runs on every non-exempt
`/v1/*` route.

The handler's pre-conditions are exactly:

1. Authenticated bearer (the user who just signed in pastes a token;
   the bearer is in scope when the BFF route or the server action
   makes the call).
2. The DB-side UPDATE filter `WHERE first_signin_at IS NULL` enforces
   the actual idempotency property — a stale or malicious actor with a
   valid bearer cannot RE-mark first sign-in once it has flipped.

That is all the access control this surface needs. The bearer-auth
middleware in `internal/api/httpserver.go` (the
`httpAuthMiddlewareWithExemptions` chain) already enforces (1) because
`/v1/install/mark-first-signin` is NOT in the bearer-exempt list; the
SQL filter enforces (2). The path is therefore a plain
`/v1/install/mark-first-signin` with no decorative prefix.

The admin-only reset endpoint (D7 below) goes one step further: it
checks `cred.IsAdmin` defense-in-depth, alongside the OPA middleware.

**Confidence: high.** The pattern matches slice 060's admin-self
endpoint and slice 028's audit-period freeze (no decorative prefixes;
the middleware + handler logic are the access control).

**Revisit once in use:** if a future slice adds a class of "elevated but
non-admin platform metadata writes" we may want a shared middleware
factory that mounts them under a common prefix and applies a
consistent admin OR bearer-with-X-attr predicate. Today, this single
route does not justify that abstraction (Article VIII — no abstractions
without justification).

### D2. `--reset-bootstrap --force` threshold — exactly when is `--force` required?

**Chosen: `--force` is required when `platform_status.first_signin_at IS
NOT NULL`.** That is, the moment any user successfully signs in (whether
admin, viewer, OIDC-callback recipient, or pasted-bearer human), the
foot-gun gate engages.

The candidate thresholds considered:

| Threshold          | "Real user" definition                    | Rejected because                                                                                                           |
| ------------------ | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------------- |
| First user (any)   | First `signIn` action ever                | Chosen                                                                                                                     |
| First admin user   | First admin sign-in                       | Hides the foot-gun for the case where a non-admin reset happens first; non-admin re-issuance is still wrong                |
| Any active session | A non-expired session in `sessions` table | Coupled to slice 034's session implementation; "session exists right now" is more brittle than "sign-in has ever occurred" |
| First OIDC binding | An OIDC user with `idp_subject` linked    | Misses the local-mode self-host path entirely                                                                              |

The chosen threshold is the strongest signal that the platform is no
longer in fresh-install mode. It is also the cheapest to compute (a
single column on a singleton row) and the easiest to reason about
("has anyone ever signed in here?").

The `--force` flag does TWO things on the elevated reset path:

1. Allows the operation despite `first_signin_at IS NOT NULL`.
2. Clears `first_signin_at` so the install-state endpoint reports
   `first_install=true` again — the operator is explicitly declaring
   "treat this as a fresh install for UX purposes."

**Confidence: high.** The behavior matches the canvas's "deliberate
friction" anti-pattern for recovery operations (canvas §1.6 / P0-A6
in the slice).

**Revisit once in use:** if operators report that they want to re-issue
the bootstrap token WITHOUT resetting `first_signin_at` (because the
login page should stay in its already-signed-in copy), we'll add a
`--keep-status` orthogonal flag. The two concerns (re-arm the file vs
re-flag the install) are intertwined today because the slice's scope
is the discoverability of the FIRST sign-in; later iterations may
separate them.

### D3. Bootstrap-token file path default — `/var/lib/atlas` vs `${ATLAS_DATA_DIR}` vs `./atlas-data`

**Chosen: `${ATLAS_DATA_DIR}/bootstrap-token` with fallback
`/var/lib/atlas/bootstrap-token`.** Encapsulated in
`platform.BootstrapTokenPath(os.Getenv("ATLAS_DATA_DIR"))`.

The three candidate defaults:

| Path                                | When it makes sense                          | Rejected because                                        |
| ----------------------------------- | -------------------------------------------- | ------------------------------------------------------- |
| `/var/lib/atlas/bootstrap-token`    | FHS-correct for a daemon's mutable state     | Wrong for `./atlas` bare-binary devs on macOS / Windows |
| `${ATLAS_DATA_DIR}/bootstrap-token` | Container deployments (docker-compose, Helm) | Empty when run bare-binary                              |
| `./atlas-data/bootstrap-token`      | Bare-binary on a dev laptop                  | Pollutes CWD; surprising for production deploys         |

The env-var-first pattern lets each deployment shape configure its own
canonical location: docker-compose's `.env.example` sets
`ATLAS_DATA_DIR=/var/lib/atlas` (which is bind-mounted to
`./atlas-data` on the host); Helm's `values.yaml` sets the PVC mount
to `/var/lib/atlas`. The bare-binary fallback (`/var/lib/atlas`) is
FHS-correct but requires the operator to pre-create the directory with
the right ownership — which is the right friction for a daemon binary
running as root or a system user.

**Confidence: high.** The single env var with a sane fallback is the
twelve-factor-app-shaped default.

**Revisit once in use:** if bare-binary developers report friction
with `/var/lib/atlas`, we'll add a heuristic that falls back to
`./atlas-data` when the binary is running as a non-root, non-system
user. Today the operator can always set `ATLAS_DATA_DIR=./atlas-data`
explicitly.

### D4. Log shape when the bootstrap-token file is consumed

**Chosen: INFO-level slog line `"bootstrap-token file consumed and
deleted"` with a `path=` attribute carrying the REDACTED path
(parent-dir basename + filename only — never the full prefix). The
token plaintext is NEVER logged, not even hashed (P0-A2).**

The candidate shapes:

| Shape                                                   | Rejected because                                                                                                                                                     |
| ------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `INFO consumed; hash=<sha256[:8]>`                      | Tempts a future bug where the full hash gets logged; the hash is not useful operationally (the operator already knows the token was consumed because they signed in) |
| `INFO consumed; path=/var/lib/atlas/bootstrap-token`    | Full path is documented anyway, but redaction is overcautious-by-design — defense in depth costs nothing here                                                        |
| `DEBUG consumed`                                        | An event that happens exactly once per platform lifetime deserves INFO; an operator should be able to see it without raising the log level                           |
| **Chosen: `INFO consumed; path=atlas/bootstrap-token`** | The basename plus parent-basename gives enough operational signal to grep without echoing the full filesystem layout                                                 |

The `redactPath` helper in `internal/platform/bootstrap_file.go`
encapsulates the redaction logic. The test in
`bootstrap_file_test.go` asserts both (a) the consumed line is
emitted and (b) the token plaintext NEVER appears in the log.

**Confidence: high.** The redaction is overcautious but cheap; the
INFO level is the right severity for a once-per-lifetime event.

**Revisit once in use:** if operators want a structured event ID for
log routing (e.g. shipping to Loki and matching on
`event_id=bootstrap-token-consumed`), we'll add a `event=` attribute.
Today the message text is the join key.

### D5. Singleton-row RLS pattern — adapt slice 068 or new shape?

**Chosen: adapt slice 068's `tenant_id IS NULL OR
current_tenant_matches(tenant_id)` pattern to the no-tenant-id case
by using `USING (true)` on the read policy AND omitting all write
policies under FORCE ROW LEVEL SECURITY.**

The candidate shapes:

| Shape                                                                                       | Rejected because                                                                                                                                                                                                                                                                          |
| ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| No RLS, with `GRANT SELECT, UPDATE TO atlas_app`                                            | No defense-in-depth; a buggy app-pool handler could flip the marker                                                                                                                                                                                                                       |
| Two policies (public_read SELECT, elevated_write UPDATE) with FORCE                         | UPDATE policy needs a meaningful USING predicate; we don't have one (there is no `app.elevated_pool` setting to key on)                                                                                                                                                                   |
| **Chosen: one policy (public_read SELECT, USING true), no write policy, GRANT SELECT only** | Atlas_app cannot mutate at all — writes MUST go through the migrate pool (BYPASSRLS). The atlas server's elevated handlers use the migrate pool already; the CLI's reset-bootstrap path goes through an admin HTTP endpoint that uses the migrate pool. Zero "intentional bypass" surface |

The slice 068 pattern with `tenant_id IS NULL` worked because there
was still a tenant column to match against in the multi-tenant rows.
For a true singleton with no tenant_id at all, the cleanest expression
of "this table sits outside the tenancy model" is "RLS-FORCE, public
read, no write policy" — the absence of a write policy IS the access
control under FORCE RLS.

**Confidence: high.** The pattern is documented in the migration
header comment and the integration test
`TestAppPoolCannotWrite` asserts the load-bearing property: the app
pool cannot mutate the singleton row.

**Revisit once in use:** if we add a second platform-level singleton
table (e.g. a "fleet pairing key" or similar), we may want to factor
this shape into a SQL migration helper. Today, one table doesn't
justify the helper.

### D6. Should the existing `signIn` server action call `mark-first-signin` directly, or go through the BFF route?

**Chosen: the server action calls atlas directly (the bearer is in
scope at action time). The dedicated BFF route at
`/api/install/mark-first-signin` exists for completeness + tests.**

The two paths considered:

| Path                                                                                                                   | Rejected because                                                                                           |
| ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------- |
| `signIn` action → BFF route → atlas                                                                                    | One extra hop on the sign-in hot path for zero added benefit; the bearer is already in scope in the action |
| **Chosen: `signIn` action → atlas directly; BFF route remains for client-side fallback paths and the vitest contract** | No extra hop; the BFF route is still testable in isolation per AC-13                                       |

The fire-and-forget shape inside `signIn` (the `try { ... } catch {}`)
is intentional: the metadata write must NEVER block the production
sign-in path (P0-A5). A failure here means the install-state endpoint
will continue to report `first_install=true` on the next visit; the
worst case is that the FirstInstallGuidance card persists for one
extra session, not that the user fails to sign in.

**Confidence: high.** The BFF route is still there for completeness;
the direct call is the production path.

**Revisit once in use:** if we add CSRF protection or per-action
rate-limiting we may want to channel the call through the BFF route
where those guards live. Today, the action is the right shape.

### D7. New admin endpoint for reset-bootstrap, vs extend the AdminCredentialsService gRPC

**Chosen: a new HTTP endpoint `POST /v1/admin/install/reset-bootstrap`
that takes `{token, force}` JSON and is admin-gated. The CLI flow is:
(1) call existing gRPC `Issue` to mint a fresh bearer; (2) POST the
new bearer + force flag to the new HTTP endpoint so atlas resets
`platform_status` and writes the new token to the file.**

The two candidate shapes:

| Shape                                                            | Rejected because                                                                                                                                       |
| ---------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Extend `AdminCredentialsService` proto with `ResetBootstrap` RPC | Couples a credential lifecycle concern to a platform_status concern; the proto would carry the `Force` flag forever as a credential-issuance parameter |
| **Chosen: separate HTTP endpoint**                               | The proto stays focused on credential CRUD; the platform_status reset is a HTTP-only admin operation with its own JSON contract                        |

The CLI's two-step flow keeps the existing `credentials issue` shape
intact for callers who don't want the reset behavior, and adds the
`--reset-bootstrap` flag as a thin wrapper that composes the two
calls.

**Confidence: medium-high.** A future slice that adds more recovery
operations may grow a dedicated `PlatformAdminService` proto. The HTTP
endpoint is the right shape for v1 because the operation is rare and
the JSON contract is trivial.

**Revisit once in use:** if we accumulate three or more "platform
recovery" HTTP endpoints, we'll cut a `PlatformAdminService` proto
and migrate them. Two is borderline; one is fine as a plain HTTP
route.

## Revisit once in use

A consolidated list of the "revisit later" items above, sorted by
when we expect to need them:

1. **D2 — `--keep-status` flag on `--reset-bootstrap`.** If operators
   report wanting to re-issue the token without flipping the UX back
   to fresh-install mode.
2. **D3 — bare-binary fallback heuristic.** If bare-binary developers
   on macOS / Windows hit friction with `/var/lib/atlas`.
3. **D7 — `PlatformAdminService` proto.** If a third recovery endpoint
   shows up.
4. **D4 — structured `event=` attribute.** If we ship a Loki / log
   routing recipe that wants a stable event key.
5. **D5 — singleton RLS migration helper.** If we add a second
   platform-level singleton table.

## Confidence summary

| Decision                               | Confidence  |
| -------------------------------------- | ----------- |
| D1 — drop `_internal` prefix           | high        |
| D2 — `--force` threshold = any sign-in | high        |
| D3 — `ATLAS_DATA_DIR` env-var first    | high        |
| D4 — INFO + redacted path              | high        |
| D5 — public-read + no-write RLS        | high        |
| D6 — direct call from `signIn` action  | high        |
| D7 — separate HTTP endpoint, not proto | medium-high |

No decision in this slice was a coin flip. The medium-high on D7 is
the only call where a future iteration's accumulation could push us
to migrate; the rest are stable.
