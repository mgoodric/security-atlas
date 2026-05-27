# Slice 315 — decisions log

Coverage lift — auth-substrate-v2 small packages (`oauthclient` + `oauthcode` + `revocation` + `userprefs`) round-3 spillover from slice 312. Each D-decision is a JUDGMENT-slice trade-off recorded inline per `Plans/prompts/04-per-slice-template.md` JUDGMENT-slice discipline.

## Context

Spillover from slice 312's round-3 audit (`docs/coverage-audit-2026-05-round-3.md`). The audit identified 4 small auth-substrate-v2 packages below 70% merged coverage, all under 100 statements each:

| Package                     | Pre-slice merged % | Statements | Origin slice                      |
| --------------------------- | ------------------ | ---------- | --------------------------------- |
| `internal/auth/oauthclient` | 5.7                | 53         | 188 (OAuth client registry)       |
| `internal/auth/oauthcode`   | 0.0                | 89         | 189 (PKCE auth-code store)        |
| `internal/auth/revocation`  | 18.2               | 44         | 190 (RFC 7009 revocation list)    |
| `internal/auth/userprefs`   | 29.6               | 54         | 108 (per-user notification prefs) |

All 4 were untracked in `coverage-thresholds.json`. The disposition the audit assigned was `unit-add + integration-enrollment` — the existing test files (where present) were not enrolled in CI's `tests-integration` job, so their integration coverage didn't reach the merged profile.

## D1 — Grouping rationale

**Question.** Why bundle 4 packages into one slice rather than ship 4 separate ones?

**Decision.** Single slice covering all 4. **Reasons:**

- Each is ≤ 100 statements — individually each is too small to warrant a standalone slice (the PR review overhead would exceed the substantive change).
- 3 of 4 are siblings under `internal/auth/` and share the OAuth AS substrate origin (slices 187-192). The test pattern repeats (store CRUD + sentinel-error branches + RLS or RLS-exempt tenancy).
- `userprefs` is the odd one out (slice 108 origin, RLS-enabled), but the test shape still maps to the same template (store CRUD + RLS isolation), so the cognitive load of bundling stays low.
- Precedent: slice 312's narrative explicitly listed these 4 as a single grouped spillover, naming the slice 315.

**Alternative considered + rejected.** Ship `userprefs` separately because it's tenant-scoped + slice 108 (not 188+). Rejected because `userprefs` is the smallest gap (29.6% → ~85% post-enrollment); singleton-PR overhead would outweigh test write effort.

## D2 — Test-file shape per package

**Question.** Should every package get a new `integration_test.go`, or only those without an existing one?

**Decision.** Add `integration_test.go` to `oauthclient`, `oauthcode`, `userprefs`. Leave `revocation` untouched — it already shipped a 190-line `integration_test.go` from slice 190 that covers Revoke / IsRevoked / Sweep / audit-log append. Enrolment alone takes `revocation` from 18.2% to 79.5% merged.

**Rationale.** The slice 290 precedent: "enrolment is the load-bearing move." When a package already has a comprehensive integration suite that simply isn't run in CI, the fix is the workflow change, not new code. Writing additional tests on top of slice 190's would be vanity coverage (the new tests would cover the same branches the existing tests already cover).

**Sanity check.** Local merged-profile measurement (Postgres-backed) on this branch:

| Package                     | Pre-slice merged % | Post-slice merged % | Floor (set in this PR) |
| --------------------------- | ------------------ | ------------------- | ---------------------- |
| `internal/auth/oauthclient` | 5.7                | 86.8                | 84                     |
| `internal/auth/oauthcode`   | 0.0                | 87.6                | 85                     |
| `internal/auth/revocation`  | 18.2               | 79.5                | 77                     |
| `internal/auth/userprefs`   | 29.6               | 85.2                | 83                     |

Every package exceeds the AC-2 70% target. Floors are set at `max(0, floor(measured - 2pp))` per slice 069 P0-A4 (monotonic ratchet, 2pp noise band).

## D3 — Tenancy plumbing in `userprefs` tests

**Question.** `user_notification_preferences` is RLS-enabled (canonical four-policy pattern). How do the integration tests apply the tenant GUC?

**Decision.** Tests open two pools — `DATABASE_URL_APP` (atlas_app, RLS-enforced) and `DATABASE_URL` (admin, BYPASSRLS) — mirroring the slice 108 + slice 162 `internal/api/me/profile_integration_test.go` pattern. Tests:

- Seed users via the admin pool (BYPASSRLS) so seed data is deterministic and not gated by an empty tenant GUC.
- Read / write via the app pool through the production `Store.Get` and `Store.Upsert` methods, which call `tenancy.ApplyTenant` internally — the test injects the tenant via `tenancy.WithTenant(ctx, tenantID.String())`.
- Verify RLS isolation with a `TestRLSIsolationBetweenTenants` test that confirms Tenant A's write is invisible to Tenant B's read.

**Rationale.** Reuses the existing tenancy plumbing rather than inventing a per-test bypass. The test exercises the EXACT code path production runs.

## D4 — Tenancy NOT applied for `oauthclient` / `oauthcode` / `revocation` tests

**Question.** These three tables are explicitly RLS-exempt (see migration headers for `oauth_clients`, `oauth_auth_codes`, `oauth_revoked_tokens`). Do the integration tests still use the app pool?

**Decision.** Use `DATABASE_URL_APP` (atlas_app pool). No tenant GUC needed.

**Rationale.** The production code path (`Store.New` callers in `cmd/atlas` + `internal/api/oauth`) uses the atlas_app pool without `tenancy.ApplyTenant` for these specific tables. Mirroring the production path makes the integration test a true end-to-end check rather than a synthetic "tests pass under a different pool" arrangement.

The RLS-exempt status is documented in each migration header (with rationale: platform-global identities, short-lived codes, jti-keyed revocation list). The test suites quote those rationales in their package-doc headers.

## D5 — Token-prefix discipline (GitGuardian)

**Question.** GitGuardian flags token-prefix patterns even in tests. How are the new tests' fixture values structured?

**Decision.** Use `uuid.New().String()` for unique values + `"test-"` / `"ut-"` prefixes for client_ids / redirect_uris / code values. NO base64-shaped JWT-prefix literals (the kind starting `eyJ`), NO vendor token prefixes (the Okta / GitHub-personal / Slack-bot / Stripe-live / Stripe-test families) anywhere in the test files.

**Rationale.** Cumulative feedback from slices 196-205 batch (`feedback_ci_secret_scanning.md`): GitGuardian's heuristic is path-agnostic — vendor prefixes trip it even in `*_test.go`. Cheapest mitigation is discipline at write time.

## D6 — Unit vs integration test split

**Question.** The slice 290 split rule says "integration covers DB-touching paths; unit covers pre-DB helpers." How is that applied per package?

**Decision.**

- **`oauthclient`:** unit tests (existing + the `SecretByteLen` guard added inline) cover `generateSecret` entropy + `password.Hash`/`Verify` round-trip. Integration tests cover `Issue` / `Verify` / `Lookup` DB-touching branches.
- **`oauthcode`:** new `oauthcode_test.go` covers `WithClock` mutability + `PKCEMethodS256` + `DefaultTTL` constants + `New` constructor. Integration covers everything else (Insert, ConsumeOnce, SweepExpired, RegisterRedirectURI, IsRedirectURIRegistered, LookupRedirectURI).
- **`revocation`:** unit tests already cover the nil-pool sentinel + empty-jti defensive guards. No new unit tests. Integration tests (slice 190) cover Revoke / IsRevoked / Sweep / audit-log append.
- **`userprefs`:** unit tests already cover `DefaultMatrix` + `isAllowedEvent` + `isAllowedChannel`. New integration tests cover `Get` + `Upsert` DB-touching paths plus RLS isolation.

## D7 — Coverage profile measurement

**Question.** Per-package coverage was measured locally via `-coverpkg=./internal/auth/<pkg>/...` against each package's own test run, not against a unified profile. Is that the same number CI will produce?

**Decision.** Yes, equivalent. The CI run uses `-coverpkg=./...` across the unified test invocation in `tests-integration`, but the union of per-package `-coverpkg=./internal/auth/<pkg>/...` runs produces the same per-package statement counts when fed to `gocovmerge` and re-aggregated.

**Verification.** Ran `go run ./cmd/scripts/coverage-gate -profile=/tmp/merged-315.txt` (where `/tmp/merged-315.txt` is the gocovmerge'd output of the 4 per-package runs). Output: `coverage-gate: checked 4 packages, 0 failed, 90 warnings (no profile data)`. All 4 new floors pass. The 90 warnings are for packages that weren't in this local run's `-coverpkg` scope — the gate's policy is warn-but-don't-fail for missing profile data.

## D8 — Threshold update header comment

**Question.** Should the `$comment` field in `coverage-thresholds.json` be updated for slice 315?

**Decision.** Yes — append a slice 315 summary to the existing slice 069 / 279 / 312 trail. Mirrors the slice 312 example. The `$comment` is the AI-navigable changelog for the thresholds file; readers tracing "why is `internal/auth/oauthclient` floored at 84?" should find the answer in this file's history.

## Constitutional invariants honored

- **Slice 069 P0-A4** (monotonic ratchet) — every new threshold is at `max(0, floor(measured - 2pp))`. No existing floor lowered.
- **AC-2** — every package ≥ 70% merged coverage post-enrolment (oauthclient 86.8 / oauthcode 87.6 / revocation 79.5 / userprefs 85.2).
- **AC-3** — every new test file's first comment block names the load-bearing functions + branches covered.
- **AC-4** — 4 new floors added to `coverage-thresholds.json` at `max(0, floor(measured - 2pp))`.
- **P0-315-1** — no floor raised without writing the tests to hit the new bar.
- **P0-315-2** — no existing floor lowered.
- **P0-315-3** — `_STATUS.md` row 315 flipped to `in-review` in a SEPARATE commit on the same branch (per template Step 9), NOT bundled with the test changes.
- **P0-315-4** — slice 314 (`internal/api/oauth`) NOT bundled here — those 921 statements remain a separate spillover.
- **AI-assist boundary** — every test body written deliberately with explicit reference to the slice's anti-criteria (P0-188-3 plaintext-secret leak, P0-189-3 one-shot codes, P0-190-4 revocation idempotency). No LLM-generated boilerplate.
- **Token-prefix bans** — no `eyJ*` / `okta_*` / `ghp_*` / `sk_*` / `xoxp-*` literals in any test fixture.

## Surprises surfaced

None. The slice is a textbook "enrolment + unit-add" lift, mirroring slices 290 / 297 / 310 / 311. The audit's prediction (4 packages, < 100 stmts each, group-cohesive) held: every assumption baked into the slice doc was confirmed in execution. No spillover slices filed.

## Files touched

- `internal/auth/oauthcode/integration_test.go` — NEW (350 lines; 14 test functions)
- `internal/auth/oauthcode/oauthcode_test.go` — NEW (60 lines; 4 unit tests for constants + WithClock + New)
- `internal/auth/oauthclient/integration_test.go` — NEW (280 lines; 12 test functions)
- `internal/auth/userprefs/integration_test.go` — NEW (260 lines; 8 test functions)
- `.github/workflows/ci.yml` — extended `tests-integration` package list with 4 new entries
- `cmd/scripts/coverage-thresholds.json` — 4 new floor entries + updated `$comment`
- `docs/audit-log/315-coverage-auth-substrate-small-decisions.md` — this file
- `docs/issues/_STATUS.md` — row 315 `ready` → `in-review` (separate commit per template Step 9)
