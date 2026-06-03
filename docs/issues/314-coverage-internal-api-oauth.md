# 314 — Coverage lift — `internal/api/oauth` to 70%+

**Cluster:** Quality
**Estimate:** 3-5d (921 statements; OAuth AS endpoint family; multi-RFC surface)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Surfaced during slice 312's round-3 coverage audit, captured per the continuous-batch policy. The audit at `docs/coverage-audit-2026-05-round-3.md` measured `internal/api/oauth` at **15.7% merged coverage** (unit-only: 15.7%), well below the 70% aspirational target.

`internal/api/oauth` is the largest single untracked surface in the audit at 921 statements — it's the slice 187 OAuth Authorization Server endpoint family (`/oauth/token`, `/oauth/authorize`, `/oauth/introspect`, `/oauth/revoke`, `/oauth/.well-known/openid-configuration`, `/oauth/.well-known/oauth-authorization-server`, etc.). It implements RFC 9068 JWT Profile, RFC 8693 Token Exchange, RFC 7636 PKCE, RFC 8628 Device Authorization Grant, RFC 7009 Revocation, RFC 7662 Introspection.

**Disposition:** `unit-add` + `integration-enrollment`

**Notes:** Standalone — too large to bundle with other auth-substrate-v2 packages (slice 315 handles the small auth-substrate companions). Likely needs both (a) integration enrollment in CI's `tests-integration` job (slice 187 / 188 / 189 added integration tests; if not already in the CI list, that's a 0-effort lift), AND (b) substantive unit tests for the pre-DB branches (client lookup, JWS verification, redirect URI matching, scope parsing, error response formatting).

## What ships in this slice

1. **Enroll `internal/api/oauth` in CI's `tests-integration` job** (if not already).
2. **New unit tests** under `internal/api/oauth/*_test.go` covering the OAuth AS endpoint families. Suggested test files (one per RFC area):
   - `token_test.go` — `/oauth/token` (authorization_code, client_credentials, refresh_token, urn:ietf:params:oauth:grant-type:token-exchange, urn:ietf:params:oauth:grant-type:device_code grants)
   - `authorize_test.go` — `/oauth/authorize` (PKCE challenge + verifier, response_type=code, redirect_uri matching, scope filtering, error responses)
   - `introspect_test.go` — `/oauth/introspect` (active/expired/revoked branches, client auth)
   - `revoke_test.go` — `/oauth/revoke` (token_type_hint, idempotent semantics)
   - `wellknown_test.go` — `.well-known/openid-configuration` + `.well-known/oauth-authorization-server`
   - `device_test.go` — `/oauth/device_authorization` + `/oauth/token` device_code grant (slice 8628)
3. **Floor lift in `cmd/scripts/coverage-thresholds.json`** — add the new entry at `floor(merged_measured - 2pp)`. (Currently untracked.)

## Acceptance criteria

- [ ] **AC-1.** `internal/api/oauth` reaches ≥ 70% merged coverage.
- [ ] **AC-2.** Each test exercises real RFC-compliance branches with real assertions (no vacuous tests).
- [ ] **AC-3.** Each new test file's first comment block names the RFC + section + load-bearing function it covers.
- [ ] **AC-4.** `coverage-thresholds.json` adds the `internal/api/oauth` floor at `max(0, floor(measured - 2pp))`.

## Constitutional invariants honored

- **Testing discipline (CLAUDE.md).** Ratchet contract: floor + tests in same PR.
- **Slice 069 methodology.** Floors at `max(0, floor(measured - 2pp))`. Monotonic ↑.
- **AI-assist boundary.** OAuth AS is critical security infrastructure — no LLM-generated test bodies; per-RFC-section human-written branch coverage.

## Dependencies

- **#312** (round-3 coverage audit + lift) — `ready`. This slice depends on #312 landing the audit doc.
- **#187** (OAuth AS foundation) — `merged`. The surface under test.
- **#192** (OAuth AS endpoint family completion) — should be `merged` before lift work begins.

## Anti-criteria (P0 — block merge)

- **P0-314-1.** Does NOT raise the `internal/api/oauth` floor without writing the unit tests that hit the new bar.
- **P0-314-2.** Does NOT lower any existing floor.
- **P0-314-3.** Does NOT modify `_STATUS.md` from inside this slice's own commits.
- **P0-314-4.** Does NOT write tests that bypass the AS's RFC-conformance assertions (i.e. tests must assert RFC error codes, expected claims, signed JWS validity — NOT just status codes).

## Notes for the implementing agent

OAuth AS testing is one of the higher-friction surfaces because RFC conformance requires asserting specific error codes (e.g. RFC 6749 §5.2: `invalid_request`, `invalid_client`, `invalid_grant`, `unauthorized_client`, `unsupported_grant_type`, `invalid_scope`). The slice 312 audit doc documents the surface; pair it with:

- `internal/auth/jwt` test patterns (already at 95.5% merged — read for JWS signing test patterns)
- `internal/auth/tokensign` test patterns (slice 187 — read for keystore wiring)
- `pkg/sdk-go/oauth` test patterns (slice 188 — client-side test shape; the AS surface mirrors)

Suggested phasing: enrollment commit (small, ~10 min), then per-RFC-area test commit (one per `.go` test file added). Bisectable.
