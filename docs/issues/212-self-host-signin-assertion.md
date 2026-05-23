# 212 — Self-host bundled e2e asserts bootstrap user can sign in + reach an admin-gated endpoint

**Cluster:** Quality / Testing
**Estimate:** ~0.25d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover from slice 211 retrospective. The full bootstrap → sign-in → authz-gated-endpoint path is the load-bearing contract the local-credential surface exists to serve, and slices 209 + 210 + 211 all shipped with this same class of bug — no test asserted that the path actually works end-to-end. This slice locks the regression with a CI guard.

## Narrative

`deploy/docker/test-self-host-bundle.sh` currently asserts six things in both `bundled` and `external` matrix modes:

1. `docker compose up` brings every service to running/healthy
2. atlas `/health` returns 200
3. atlas-bootstrap exits 0
4. `controls` has 50 seeded rows
5. `decision_audit_log` has ≥1 row (proves the authenticated upload path ran)
6. Re-running atlas-bootstrap is idempotent

**None of those exercise the email/password sign-in path slice 209 ships.** A future PR that removes the seed.sql role grants (slice 211) — or that breaks `/v1/install-state`'s `tenant_id` field (slice 210) — would pass green CI and ship the same broken UX the operator just dug us out of.

This slice extends the harness with two new assertions, splice-inserted before the re-run idempotency check (so the new assertions run against the freshly-bootstrapped state, not the post-rerun state):

- **Assertion 6 (new)**: POST `/auth/local/login` with the harness's `ATLAS_DEFAULT_USER_EMAIL` + `ATLAS_DEFAULT_USER_PASSWORD` → HTTP 200 + response JSON contains a non-empty `token` field. Proves slice 209's email/password sign-in path is wired end-to-end (handler reachable, password verifies, JWT signer produces a token).
- **Assertion 7 (new)**: decode the JWT's middle segment (base64url payload) and verify the slice 211 grants flowed into the claims:
  - `atlas:super_admin == true`
  - `atlas:roles[<bootstrap_tenant_id>]` contains `"admin"`

Both assertions run in both matrix modes (bundled + external). The existing assertion 6 (idempotency) is renumbered to 8.

### Why JWT-claim-level inspection vs. just probing /v1/me

A 200 from `/auth/local/login` alone is insufficient — slice 209's nil-signer fallback (D5 in the slice 209 decisions) returns a 200 with NO token field when atlas was started without the OAuth signer wired. The first assertion's "non-empty token" check catches that misconfig. The second assertion's claim-inspection catches the slice 211 class-of-bug: signed JWT but empty roles. Either failure mode is silent without these two specific checks.

We could also probe `/v1/me` with the new JWT to catch authz problems further down the stack. That's a fine future addition but the JWT-claim check is cheaper (no second HTTP call) and locks the specific regression we just survived.

## Threat model

| STRIDE                       | Threat                                                                                                              | Mitigation                                                                                                                                                                                                                                  |
| ---------------------------- | ------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | The harness's deterministic bootstrap password (`test-default-user-password`) leaks into a public CI log if echoed. | The new code calls `curl --data @file` (body in a tmpfile) instead of `--data 'literal'`; the password never appears in any rendered command line. Existing `set -x` discipline in the harness avoids leaking env values to logs.           |
| **T** Tampering              | n/a — read-only CI assertions.                                                                                      | n/a                                                                                                                                                                                                                                         |
| **R** Repudiation            | n/a — CI artifacts.                                                                                                 | n/a                                                                                                                                                                                                                                         |
| **I** Information disclosure | A failure-mode log dump exposes the JWT, which (in CI) is short-lived but still a credential.                       | The JWT is only logged on failure (via existing `fail()` log helper) and is bound to a transient docker-network ephemeral atlas issuer (`http://atlas-bootstrap-internal-something:8080`) — useless outside the CI runner's local Postgres. |
| **D** Denial of service      | n/a — single-request assertion path.                                                                                | n/a                                                                                                                                                                                                                                         |
| **E** Elevation of privilege | n/a — assertion only inspects state; does not write or modify.                                                      | n/a                                                                                                                                                                                                                                         |

## Acceptance criteria

- [ ] AC-1: `deploy/docker/test-self-host-bundle.sh` adds Assertion 6 — POST `/auth/local/login` against `http://127.0.0.1:${ATLAS_HOSTPORT}/auth/local/login` with the harness's `ATLAS_DEFAULT_USER_EMAIL` + `ATLAS_DEFAULT_USER_PASSWORD` + `ATLAS_BOOTSTRAP_TENANT` env values. JSON body assembled via heredoc to a tmpfile, `curl --data @<file>` to avoid leaking the password into a process arg list. Asserts HTTP 200.
- [ ] AC-2: Assertion 6 also asserts the response body contains a non-empty `token` field (catches the slice 209 D5 nil-signer fallback). Uses `python3 -c` (available on ubuntu-latest) to parse the JSON.
- [ ] AC-3: `deploy/docker/test-self-host-bundle.sh` adds Assertion 7 — decode the JWT payload (middle segment, base64url) via `python3 -c`. Asserts `atlas:super_admin == true`. Asserts the value at `atlas:roles[<bootstrap_tenant_uuid>]` is a list containing `"admin"`.
- [ ] AC-4: The existing Assertion 6 (re-run idempotency) is renumbered to Assertion 8. The header comment block's assertion list (lines 29-50) is updated to enumerate the new assertions accurately.
- [ ] AC-5: Both new assertions run in both matrix modes (`bundled` + `external`) — the assertions are unconditional, not mode-gated, so the matrix coverage is automatic.
- [ ] AC-6: Failure mode of either new assertion produces a clear `fail()` message — `fail "sign-in: HTTP ${code}"` for the HTTP-status check, `fail "JWT missing admin role"` for the claim check. The harness already prints the `set +e` exit code + body when `fail()` is invoked.
- [ ] AC-7: The harness still exits 0 against `main` with the slice 211 fix in place (positive control — proves the new assertions don't false-positive on the known-good state).
- [ ] AC-8: A red-team simulation — revert the slice 211 role-grant INSERTs from seed.sql locally, run the harness in bundled mode, and confirm Assertion 7 fires with the expected `fail` message. Document the test in the decisions log; revert the local seed.sql before committing.

## Decisions

- **D1: JWT-claim inspection in shell** — use `python3 -c` for base64url decode + JSON parse rather than introducing a `jq + jwt-decode` chain. Python is pre-installed on the ubuntu-latest runner; jq does not handle base64url padding cleanly. Adding a dedicated `jwt-cli` would be a new dependency for one test.
- **D2: Heredoc to tmpfile vs. `--data` inline** — the password is a low-entropy CI-only secret but still treated as sensitive by reflex. `--data @<file>` keeps it out of the rendered command-line in any CI log echo. Mirrors the slice 211 backfill verification pattern.
- **D3: Position between Assertions 5 and (old) 6** — the new sign-in assertions run AFTER the bootstrap completes but BEFORE the re-run idempotency check. This way the sign-in test exercises the same DB state the operator actually sees on first install, not the post-rerun state. If we put it after the re-run, a regression that only affects the first-run case would not show up.
- **D4: Bootstrap-tenant UUID hardcoded** — the harness already hardcodes `ATLAS_BOOTSTRAP_TENANT=00000000-0000-4000-8000-000000000001` at line 109; the claim-check assertion uses that same hardcoded UUID. If a future slice changes the bootstrap tenant UUID, both the harness's env AND the claim-check assertion update in lockstep — fine.
- **D5: No `/v1/me` follow-up probe in this slice** — focused regression guard only. Adding a `/v1/me` 200-check is a worthwhile future slice but adds a second HTTP call + JWT-bearer logic to the harness; this slice keeps the assertion surface lean. The two claim checks already prove the regression-of-concern is gone.

## Constitutional invariants honored

- **CLAUDE.md "Surgical fixes only"** — extends one file (`test-self-host-bundle.sh`) by ~40 lines. No new dependencies. No new CI jobs. Reuses the existing matrix.
- **Test-discipline ratchet (CLAUDE.md "Testing discipline")** — adds a regression guard at the integration-test layer. Does NOT lift any coverage floor; this is presence-of-test work, not coverage work.
- **No new RLS / authz / migration surface.** Read-only assertion against the existing bootstrap state.

## Anti-criteria (P0 — block merge)

- **P0-A1: Does NOT add new dependencies to the runner.** Uses `curl`, `python3`, and `docker exec` — all pre-installed on `ubuntu-latest`. No `npm install jwt-cli` or similar.
- **P0-A2: Does NOT echo the bootstrap password into any CI log line.** The JSON body is materialized via heredoc to a tmpfile; `curl --data @<file>` reads from the file. No `--data '{"password": "..."}'` literals.
- **P0-A3: Does NOT modify the existing assertions (1-5) or the idempotency check (renumbered to 8).** Purely additive. If the harness's behavior changes for the existing checks, that's a regression in this slice.
- **P0-A4: Does NOT depend on the Next.js layer / port 3000.** The assertions hit atlas directly at `http://127.0.0.1:${ATLAS_HOSTPORT}` — same surface the existing `/health` check uses. The Next.js layer's `/auth/*` rewrite (or lack thereof) is irrelevant.
- **P0-A5: Does NOT alter the harness's exit-code contract.** Exit 0 = every assertion passed; non-zero on first failure. New assertions follow the same `fail()` pattern.

## Dependencies

- **#209** — merged. Defines `/auth/local/login` + the JWT-mint path.
- **#211** — merged. Defines the seed.sql role grants the assertions verify.
- **#065** — merged. Defines the harness's overall shape.

## Notes for the implementing agent

- The harness uses `${ATLAS_HOSTPORT}` to address atlas from the runner host (set earlier in the script when the bundle comes up). Existing `/health` check at line ~378 is the reference pattern.
- `db_count()` is the existing in-Postgres assertion helper — NOT used here since we're asserting on JWT claims, not DB rows. The slice 211 DB-level state is already verified by the existing assertions (controls / decision_audit_log).
- The `python3 -c` invocations should pipe input on stdin (not inline `-c "...{token}..."`) so the JSON parsing is robust to control characters in the JWT segment.
- `fail()` and `log()` are helpers defined earlier in the harness. Use them; do not reinvent.
- After the JWT decode, the `atlas:roles` claim is `map[uuid → []string]`. The lookup pattern is `claims["atlas:roles"][BOOTSTRAP_TENANT_UUID]` — assert that result is a list AND contains `"admin"`. Doing both checks defends against future regressions where the roles map shape mutates (e.g., to a flat list).
- The bootstrap tenant UUID `00000000-0000-4000-8000-000000000001` is set via the harness's `ATLAS_BOOTSTRAP_TENANT` env (line 109). Pass that value into the assertion script so the hardcoding stays in one place.
