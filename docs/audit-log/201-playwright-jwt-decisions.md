# Slice 201 — Decisions log

**Slice:** 201 — Migrate Playwright e2e fixtures from slice 034 bearer to JWT-via-tokensign
**Type:** JUDGMENT — build-time call captured here rather than blocking the merge on human sign-off
**Status:** in-progress (this PR)
**Filed:** 2026-05-21

---

## Context

Slice 197 removed the slice 034 `httpAuthMiddlewareWithExemptions` mount + the
`atlas_test_` carve-out. The Go integration tests migrated cleanly to
`Server.IssueTestJWT(t, claims)` in the same slice. The Playwright e2e fixtures
were scope-implicit-not-actual in 197 — the static `TEST_BEARER =
"test-bearer-e2e"` in `web/e2e/seed.ts:53` was a slice 034 credstore bearer
that no longer authenticates post-197.

After slice 197 merged, ~20+ Playwright specs across `dashboard.spec.ts` +
`settings.spec.ts` fail with element-not-found timeouts because the BFF
proxies their unauthenticated requests to the atlas server, which 401s.

Slice 201 is the Playwright-side analog of slice 197's Go-side migration.

---

## D1 — Candidate shape: in-process global-setup helper (path #2)

The slice doc surfaces two candidates:

1. **Out-of-process JWT mint via `atlas-cli`.** New CLI subcommand
   `atlas-cli oauth issue-test-token` shells out to a new
   `grant_type=test_jwt` on `/oauth/token`. `web/e2e/seed.ts` invokes it
   and captures stdout.
2. **In-process via a Playwright global-setup helper.** New
   `web/e2e/global-setup.ts` makes a `fetch` to a new
   `POST /v1/test/issue-jwt` endpoint and writes the JWT to
   `process.env.TEST_BEARER`.

**Picked: candidate #2.**

Reasoning:

- **Drop-in shape** — the existing `web/e2e/fixtures.ts` reads
  `process.env.TEST_BEARER` at worker bootstrap (line 78). Replacing the
  static literal with a freshly minted JWT is a single-file change at the
  seed layer; downstream specs are untouched.
- **No new grant type** — candidate #1 invents `grant_type=test_jwt` on the
  production `/oauth/token` endpoint. That broadens the OAuth AS surface and
  introduces a code path that lives in production binaries even when
  `ATLAS_TEST_MODE` is unset. Candidate #2's new endpoint is mounted ONLY
  when `ATLAS_TEST_MODE=1` AND signer is wired — production binaries don't
  even know the route exists.
- **No CLI dependency** — candidate #1 requires the atlas-cli binary to be
  built + on PATH when Playwright runs. Local devs running `npm run test:e2e`
  without a fresh `go build` would silently get a stale CLI.
- **Single round-trip** — candidate #2's global-setup runs ONE fetch per
  Playwright invocation. Candidate #1 shells out psql-style per worker.

The slice doc itself recommends #2: "Recommend path #2 (in-process
global-setup) for simplicity unless there's a deal-breaker discovered during
build." No deal-breaker surfaced.

---

## D2 — JWT claim shape

The endpoint accepts a JSON body with optional fields:

```json
{
  "tenant_id": "<uuid>",       // REQUIRED
  "user_id":   "<uuid|string>", // optional; defaults to "test-admin:<tenant>"
  "roles":     ["admin", ...], // optional; defaults to ["admin"]
  "super_admin": true          // optional; defaults to true
}
```

Reasoning:

- **`tenant_id` required** — the slice 190 jwtmw middleware enforces the
  tenant-scope invariant via `jwt.Validate` (`current_tenant_id in
available_tenants`). Without a tenant the JWT cannot pass validation.
- **`user_id` defaults to synthetic `test-admin:<tenant>`** — this matches
  the shape `testjwt.AdminFor` (slice 197) uses. The slice 164 settings spec
  REQUIRES the real DEMO_USER_ID (a UUID) so `/v1/me` resolves to the seeded
  users row; the Playwright global-setup passes DEMO_USER_ID explicitly.
- **`super_admin` defaults true** — the jwtmw bridge collapses SuperAdmin
  into both IsAdmin AND IsApprover on the synthesized credential. Every
  Playwright spec touches at least one admin-gated route (settings,
  dashboards, admin-bootstrap). Defaulting true keeps the global-setup body
  minimal.
- **`roles` defaults to `["admin"]`** — the jwtmw bridge stamps these
  verbatim onto the synthesized credential's OwnerRoles. The settings AC-10
  (multi-role tail badge) needs at least two roles; the global-setup passes
  `["admin", "grc_engineer"]` explicitly.

Issuer + audience are inherited from `s.jwtIssuer` + `s.jwtAudience` — the
SAME values the validator expects. There is no way to mint a token with
mismatched iss/aud through this endpoint.

Expiry is fixed at `testjwt.DefaultExpiry` (1 hour) — outlives any single
test run.

---

## D3 — Env-gating mechanism (production safety)

**Two-layer defense:**

1. **Mount-time gate** in `internal/api/httpserver.go`: the
   `root.Post("/v1/test/issue-jwt", ...)` call is wrapped in
   `if testModeEnabled() { ... }`, where `testModeEnabled()` reads
   `os.Getenv("ATLAS_TEST_MODE") == "1"`. Production binaries booting
   without the env var DO NOT mount the route — chi returns the same 404
   it would for any unknown path.
2. **Per-request gate** in `internal/api/testissuejwt.go`: the handler
   re-checks `os.Getenv(testModeEnvVar) == "1"` on every invocation.
   Even if a hypothetical bug were to leave the route mounted, the
   handler refuses with 404.

Two layers is the right defense-in-depth posture because:

- A code change that accidentally mounts the route unconditionally (the
  mount gate is one boolean condition) would still hit the per-request
  gate.
- The per-request gate also handles the case where ATLAS_TEST_MODE flips
  from "1" to "" mid-process (a `docker exec` env mutation, a config
  refresh, etc.) — the route remains mounted but refuses to issue.

The bypass-list entries in `httpserver.go` (jwtBypass, requireCredential,
authzmw) are added UNCONDITIONALLY — they're just exempt-prefix strings.
That's safe: the bypass only matters when the route IS mounted, which is
gated by ATLAS_TEST_MODE. An exempt-list entry for a non-mounted path is
a no-op.

**Why not a build tag?** A `//go:build test` build tag would force a
separate binary for the test mode. The slice 037 docker-compose bundle
ships ONE binary that targets both bundled-mode + external-mode + the
test-mode flavor. Env-gating keeps the binary single; the test-mode flavor
is purely opt-in at boot time.

---

## D4 — Cookie story

The Next.js BFF (`web/lib/api/bff.ts:33`) reads the `sa_session_token` cookie
value from the jar and forwards it as `Authorization: Bearer <value>` to the
atlas Go server. The atlas server's slice 190 `jwtmw` middleware
shape-checks for the `eyJ` JWT prefix on the Authorization header.

Therefore: the Playwright fixture (`web/e2e/fixtures.ts:86-95`) continues to
set the `sa_session_token` cookie unchanged — only the VALUE has changed
from a static literal to a freshly minted JWT. No frontend changes are
required beyond global-setup.

This was a load-bearing detail: an alternative shape (e.g., set the
`atlas_session` cookie directly) would have required deeper fixture changes
AND would have broken the existing BFF tests that assert
`sa_session_token`-only forwarding (slice 110 P0-A2).

---

## D5 — Scope discipline: e2e-audit job left alone

The CI workflow has TWO Playwright jobs:

- `Frontend · Playwright e2e` (main spec suite at `web/e2e/`) — REQUIRED in
  branch-protection (well, slice 127 pulled it pending the 4-spec cluster,
  but it's the load-bearing surface).
- `Frontend · Playwright e2e (audit)` (audit suite at `web/e2e-audit/`) —
  `continue-on-error: true`; informational.

The audit job ALSO reads `TEST_BEARER` from env. Migrating it to
JWT-via-tokensign is out of scope per the slice doc:

> Scope discipline:
>
> - DO NOT touch the slice 197 work (testjwt package, etc.) — that's the
>   Go-side reference; this slice is the Playwright-side analog.
> - DO NOT add new test-mode features beyond the JWT endpoint — keep the
>   env-gated surface minimal.

The audit job continues to use the static literal. Its `continue-on-error:
true` means it doesn't block merges. A follow-up slice can migrate it
using the same `global-setup.ts` pattern.

---

## D6 — Tests written

- `internal/api/testissuejwt_test.go` — 6 unit tests (success-round-trip,
  env-unset-404, signer-nil-404, validate-against-standard-params,
  defaults-applied, missing-tenant-400). All GREEN locally.
- The Playwright suite IS the integration test for global-setup. Local
  Playwright run captured below.

---

## Local Playwright run evidence

Captured before opening the PR. The local stack:

- Postgres 16 (alpine) on port 5491, fresh schema applied from
  `migrations/sql/*.sql`.
- atlas binary built from this branch (`go build ./cmd/atlas`) on port
  8081 with `ATLAS_TEST_MODE=1`, `ATLAS_ISSUER_URL=http://localhost:8081`,
  `ATLAS_KEYSTORE_PATH=/tmp/atlas-201-data/keys`,
  `BEARER_HASH_KEY=<32-byte test value>`.
- `web` (Next.js 16) built fresh + `npm start` on port 3001.

### Smoke test — JWT endpoint round-trip

```
$ curl -s -X POST http://localhost:8081/v1/test/issue-jwt \
    -H "Content-Type: application/json" \
    -d '{"tenant_id":"00000000-0000-0000-0000-00000000d3a0","super_admin":true}' \
    | python3 -c "import sys,json; print('TOKEN length:', len(json.load(sys.stdin)['token']))"
TOKEN length: 658

$ curl -s -o /dev/null -w "HTTP: %{http_code}\n" \
    -H "Authorization: Bearer $TOKEN" \
    http://localhost:8081/v1/anchors
HTTP: 200
```

Confirms the JWT is accepted by the slice 190 jwtmw middleware on a real
`/v1/*` route.

### Full Playwright suite — clean DB state

```
$ npx playwright test --reporter=line
...
[167/167] [chromium] › e2e/settings.spec.ts:372:7 › ... AC-10: roles tail badge ...
  8 skipped
  159 passed (6.5s)
```

**159 passed / 0 failed / 8 skipped (out of 167 specs)** — all
previously-failing dashboard + settings specs now PASS:

- `dashboard.spec.ts` AC-2 framework posture — PASS
- `dashboard.spec.ts` AC-3 top risks — PASS
- `dashboard.spec.ts` AC-4 recent drift — PASS
- `dashboard.spec.ts` AC-5 upcoming — PASS
- `dashboard.spec.ts` AC-6 activity feed — PASS
- `dashboard.spec.ts` evidence freshness — PASS
- `settings.spec.ts` AC-3 notification persist — PASS
- `settings.spec.ts` AC-4 + P0-A2 token issuance — PASS
- `settings.spec.ts` AC-5 active sessions — PASS
- `settings.spec.ts` AC-6 admin cross-link — PASS
- `settings.spec.ts` AC-7 notifications — PASS
- `settings.spec.ts` AC-8 timezone — PASS
- `settings.spec.ts` AC-9 API tokens — PASS
- `settings.spec.ts` AC-10 roles badge — PASS
- `settings.spec.ts` AC-11 rotate-twice — PASS

The 8 `skipped` items are pre-existing `test.skip(...)` annotations
(no change in count vs main).

### Honest disclosure — rerun flake on stateful tables

When the suite is run twice in a row against the SAME Postgres database
WITHOUT truncating state-mutating tables (`users`,
`user_notification_preferences`, `api_keys`, `sessions`), two settings
specs (AC-8 timezone + AC-11 rotate-twice) fail on the second run. Root
cause: `fixtures/e2e/settings.sql` uses `ON CONFLICT DO NOTHING` for the
`users` insert, so a previous spec run's PATCH to `time_zone` is not
reset by re-running the fixture.

This is a PRE-EXISTING test-infra gap (slice 165 / 168 / 171 territory),
NOT a slice 201 regression. CI runs against a fresh Postgres container
every job so it does not surface there. Documenting here for full
transparency — a future fixture-hardening slice (idempotency on
mutable user columns) is the natural follow-up. Out of scope for slice 201.

### Go unit tests

```
$ go test ./internal/api/ -run TestIssueTestJWT -count=1 -v
=== RUN   TestIssueTestJWT_SuccessRoundTrip
--- PASS: TestIssueTestJWT_SuccessRoundTrip (0.00s)
=== RUN   TestIssueTestJWT_EnvUnset_404
--- PASS: TestIssueTestJWT_EnvUnset_404 (0.00s)
=== RUN   TestIssueTestJWT_SignerNil_404
--- PASS: TestIssueTestJWT_SignerNil_404 (0.00s)
=== RUN   TestIssueTestJWT_RoundTripValidatesAgainstStandardParams
--- PASS: TestIssueTestJWT_RoundTripValidatesAgainstStandardParams (0.00s)
=== RUN   TestIssueTestJWT_DefaultsApplied
--- PASS: TestIssueTestJWT_DefaultsApplied (0.00s)
=== RUN   TestIssueTestJWT_MissingTenantID_400
--- PASS: TestIssueTestJWT_MissingTenantID_400 (0.00s)
PASS
ok      github.com/mgoodric/security-atlas/internal/api  0.461s
```

6/6 unit tests PASS.

---

## Files changed

- `internal/api/testissuejwt.go` (NEW) — handler + env-gate
- `internal/api/testissuejwt_test.go` (NEW) — 6 unit tests
- `internal/api/httpserver.go` — route mount + bypass-list entries
- `web/e2e/global-setup.ts` (NEW) — Playwright global-setup
- `web/e2e/seed.ts` — removed static TEST_BEARER + dead seedApiKey
- `web/playwright.config.ts` — registered globalSetup
- `deploy/docker/docker-compose.yml` — ATLAS_TEST_MODE env propagation
- `deploy/docker/.env.example` — documented test-mode
- `.github/workflows/ci.yml` — Playwright job sets ATLAS_TEST_MODE + ATLAS_ISSUER_URL
- `docs/audit-log/201-playwright-jwt-decisions.md` (NEW — this file)
- `docs/issues/_STATUS.md` — slice 201 row flipped to merged

---

## Anti-criteria status

- **P0-201-1** PASS — `httpAuthMiddlewareWithExemptions` retirement (slice 197)
  is not touched. No middleware mount re-added.
- **P0-201-2** PASS — endpoint refuses with 404 in production
  (env-unset path verified by `TestIssueTestJWT_EnvUnset_404`). Two-layer
  defense (mount-time gate + per-request gate).
- **P0-201-3** PASS — JWT is minted at request time, lives only in
  response body, written only to `process.env.TEST_BEARER` for the duration
  of the Playwright run. No commit, no image layer, no log.
- **P0-201-4** PASS — endpoint reuses `s.jwtSigner` (the slice 187 OAuth
  keystore via `tokensign.Signer`). No separate test-only signing
  surface. Verified by `TestIssueTestJWT_SuccessRoundTrip` round-trip
  through the same Signer.
