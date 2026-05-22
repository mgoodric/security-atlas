# 201 — Migrate Playwright e2e fixtures from slice 034 bearer to JWT-via-tokensign

**Cluster:** Quality / Backend / Auth
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `ready` (spillover from slice 197)

## Provenance

Surfaced during slice 197 (PR #471) CI. The Go integration suite was migrated end-to-end from `credstore.Issue()` bearer to JWT-via-tokensign, but the Playwright e2e test fixtures at `web/e2e/seed.ts` still use the slice 034 bearer mechanism. After slice 197's `httpAuthMiddlewareWithExemptions` removal, the `TEST_BEARER = "test-bearer-e2e"` value no longer authenticates — most authenticated Playwright specs fail with element-not-found timeouts at 5s.

The slice 197 spec narrative said "~60 integration test fixtures" but only enumerated Go integration tests; Playwright e2e was scope-implicit but not actually included in slice 197's work. Spillover-as-slice pattern (Amendment 2): file as separate slice rather than scope-creeping 197.

Slice 197 merges UNSTABLE because:

- `Frontend · Playwright e2e` is NOT in `.github/branch-protection.json` required contexts (slice 127 explicitly removed it pending the 4-spec cluster; main has been merging with Playwright-not-required for weeks).
- The Go integration suite — which IS required — passes green.
- Self-host bundle (both modes) — also load-bearing — passes green.

Failing specs (chromium, after slice 197 merge):

- dashboard.spec.ts: AC-2 framework posture, AC-3 top risks, AC-4 recent drift, AC-5 upcoming, AC-6 activity, evidence freshness
- settings.spec.ts: AC-3 notification persist, AC-4+P0-A2 token issuance, AC-5 active sessions, AC-6 admin cross-link, AC-7 notifications, AC-8 timezone, AC-9 API tokens, AC-10 roles badge, AC-11 rotate-twice

All failures share the same root cause: the Playwright fixture at `web/e2e/fixtures.ts:78` reads `TEST_BEARER` from env and `web/e2e/seed.ts:53` defines it as `"test-bearer-e2e"`. That string was a slice 034 credstore-issued bearer that the now-removed `httpAuthMiddlewareWithExemptions` middleware accepted via the `atlas_test_` carve-out (which slice 197 also removed).

## Narrative

The fix shape mirrors slice 197's approach for Go: replace the static bearer with a JWT minted at test-setup time via the OAuth keystore. The new helper API was added in slice 197 as `Server.IssueTestJWT(t, claims)` for Go — Playwright needs an analogous mechanism.

Two candidate approaches:

1. **Out-of-process JWT mint via `atlas-cli`.** `web/e2e/seed.ts` shells out to `atlas-cli oauth issue-test-token --tenant <id> --user <email> --roles admin,grc_engineer` (NEW subcommand) and captures the resulting JWT. The CLI talks to the running atlas server's `/oauth/token` endpoint with `grant_type=test_jwt` (NEW grant — gated behind a `ATLAS_TEST_MODE=1` env on the atlas service).
2. **In-process via a Playwright global-setup helper.** `web/e2e/global-setup.ts` makes a fetch to a NEW `/v1/test/issue-jwt` endpoint (gated behind `ATLAS_TEST_MODE=1`) and stores the resulting JWT into `process.env.TEST_BEARER` for downstream specs.

Path #2 is simpler and matches the existing `TEST_BEARER` consumption model — drop-in replacement at the seed layer, no downstream spec changes.

## Acceptance criteria

- **AC-1.** New env-gated endpoint `POST /v1/test/issue-jwt` returns a short-lived (1h) JWT signed via the OAuth keystore for an arbitrary test user + tenant + roles. Refuses with 404 unless `ATLAS_TEST_MODE=1`.
- **AC-2.** `web/e2e/global-setup.ts` (new file) calls the endpoint at Playwright startup and writes the JWT to `process.env.TEST_BEARER`. Existing specs continue to read `TEST_BEARER` unchanged.
- **AC-3.** `web/e2e/seed.ts` no longer defines `TEST_BEARER` as a static string. The seed continues to provision the test user/tenant/roles but does not own the credential issuance.
- **AC-4.** `web/playwright.config.ts` registers the global-setup module.
- **AC-5.** The docker-compose self-host bundle sets `ATLAS_TEST_MODE=1` on the atlas service in BOTH bundled + external modes (production deployments do NOT set this).
- **AC-6.** `cd web && npm run test:e2e` PASSES locally end-to-end (all currently-failing specs flip to green).
- **AC-7.** CI `Frontend · Playwright e2e` PASSES on the slice 201 PR.
- **AC-8.** Decisions log at `docs/audit-log/201-playwright-jwt-decisions.md` captures: candidate approach picked (1 vs 2), why; the JWT claim shape used; the env-gating mechanism for production safety.

## Anti-criteria (P0 — block merge)

- **P0-201-1.** Does NOT re-introduce slice 034 bearer middleware (`httpAuthMiddlewareWithExemptions`) — slice 197's retirement is permanent.
- **P0-201-2.** Does NOT expose the test-JWT-issue endpoint in production. Refuse-to-issue unless explicit env-gate set; refuse-to-boot if `ATLAS_TEST_MODE=1` is set on a build that wasn't compiled with a test-mode flag (or equivalent guard).
- **P0-201-3.** Does NOT bake the test JWT into image layers or commit it to git. Issued at runtime by the test harness; lives only in the Playwright process env for the duration of the test run.
- **P0-201-4.** Does NOT broaden the JWT signing-key surface. Reuses the OAuth keystore from slice 187; does not introduce a separate "test keystore" with weaker constraints.

## Dependencies

- **#197** (in flight / merged UNSTABLE) — the failing specs surfaced from this slice's removal of the bearer middleware.
- **#187 + #188** (merged) — the OAuth keystore + signing infrastructure that the new endpoint reuses.

## Skill mix (3-4)

- `tdd` (red — confirm specs fail on main; green — fix; refactor — minimize)
- `simplify`
- `ship-gate` (Playwright CI is the gate)

## Notes for the implementing agent

The two-candidate-shapes decision is the load-bearing JUDGMENT. Recommend path #2 (in-process global-setup) for simplicity unless there's a deal-breaker discovered during build.

Slice 197 engineer's `Server.IssueTestJWT(t, claims)` Go helper (in `internal/api/testjwt/`) is the reference implementation for the JWT-claims shape. The new test endpoint should accept analogous claims (sub, tenant_id, roles).

The slice 127 branch-protection drift narrative is captured in `.github/branch-protection.json` itself — leave that alone (its restoration to required-status is slice 123 territory).

## Provenance

Filed 2026-05-21 as orchestrator-driven spillover from slice 197 (PR #471) CI. Slice 197's engineer migrated Go integration end-to-end but the Playwright fixtures were scope-implicit-not-actual in the slice 197 spec. Not engineer claim-inflation — a spec scope gap that surfaced post-merge because Playwright is not in branch-protection required-checks.
