# 206 — BFF auth cookie migration from `sa_session_token` to `atlas_jwt`

**Cluster:** Frontend (auth)
**Estimate:** 1d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover surfaced 2026-05-22 from a deployment-blocking production bug report. Slice 197 retired the slice-034 opaque-bearer middleware on the Go backend but never migrated the BFF layer to read the new `atlas_jwt` cookie that slice 189's OAuth callback writes. The deployed v1.14.0 build leaves every authenticated user in an infinite login loop.

## Narrative

Three cookies are in play in the post-slice-197 codebase:

1. `sa_session_token` — declared as `SESSION_COOKIE` in `web/lib/auth.ts:5`. Read by 30+ BFF files (proxy.ts, layouts, route handlers). Retired by slice 197 on the backend, but the BFF migration never shipped.
2. `atlas_session` — declared as `OIDC_SESSION_COOKIE` in `web/lib/auth.ts:15`. Vestigial; slice 190's retirement target. NOT touched in this slice.
3. **`atlas_jwt`** — declared as `ATLAS_JWT_COOKIE` in `web/app/oauth/callback/route.ts:44`. Set by the OIDC callback's POST finalize handler. **This is the canonical post-slice-198 session cookie.**

Break chain (live-reproduced via `curl http://192.168.1.246:3015/v1/me`):

1. Operator completes OIDC login → callback writes `atlas_jwt`.
2. Browser navigates to `/dashboard` → `web/proxy.ts:99` checks `SESSION_COOKIE` (= `sa_session_token`) → cookie NOT present.
3. `proxy.ts` 307s to `/login`.
4. `/login` mostly just renders; nothing sets `sa_session_token`. → infinite loop.
5. Any `/v1/*` curl through the Next.js server also 307s because the proxy matcher catches every path except `_next/*`.

Fix shape: rename the `SESSION_COOKIE` constant's value from `"sa_session_token"` to `"atlas_jwt"`. All 30+ import sites (`SESSION_COOKIE` constant name) compile unchanged — only the bytes the cookie jar looks up change. The OAuth callback's `ATLAS_JWT_COOKIE` constant stays as a distinct symbol so the callback file's setter does not depend on `lib/auth.ts`.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                                                                                                                                                                                                                                   | Mitigation                                                                                                                                                                                                                                      |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | A renamed cookie does not change auth semantics — the cookie value is still a JWT, validated by the backend's slice-190 `jwtmw` middleware via shape-check (`eyJ` prefix) + signature verify + claim validation + revocation check.                                                                                                                                                                                                      | P0-A1: no new auth mechanism — the BFF continues to pass the cookie value as `Authorization: Bearer <jwt>` and the backend continues to validate.                                                                                               |
| **T** Tampering       | The cookie is still `httpOnly + SameSite=Lax + Secure` (set by OAuth callback already). Migration does not weaken any attribute.                                                                                                                                                                                                                                                                                                         | P0-A4: keep the cookie-attribute discipline that slice 146 + slice 189 established.                                                                                                                                                             |
| **R** Repudiation     | None — every backend hit still carries the verified JWT claim, audit log entries unchanged.                                                                                                                                                                                                                                                                                                                                              | n/a                                                                                                                                                                                                                                             |
| **I** Info disclosure | A leftover `sa_session_token` cookie in a deployed browser would silently fail to authenticate, but the value is opaque — no PII / token exfiltration risk.                                                                                                                                                                                                                                                                              | Documented in the changelog operator note (AC-10): users must log in once after deploy.                                                                                                                                                         |
| **D** DoS             | None.                                                                                                                                                                                                                                                                                                                                                                                                                                    | n/a                                                                                                                                                                                                                                             |
| **E** EoP             | The single critical bug: if the proxy ever exempts `/v1/*` without backend JWT validation, that would bypass auth. AC-3 + AC-7 explicitly verify the backend remains the authority — proxy exemption ONLY removes the Next.js redirect-to-login; the backend's `jwtmw` middleware still gates every `/v1/*` request and returns 401 on missing/invalid JWT. The exemption is necessary because `/v1/*` proxies through the Next.js host. | P0-A2: the proxy exemption MUST NOT bypass backend JWT validation. AC-7 explicitly demonstrates a curl with no auth against `/v1/me` returns 401. The proxy exemption only removes a Next.js-layer redirect; the backend is the auth authority. |

## Acceptance criteria

- [ ] AC-1: `web/lib/auth.ts` — `SESSION_COOKIE` value changed from `"sa_session_token"` to `"atlas_jwt"`. 30+ import sites (which import the named constant) compile unchanged.
- [ ] AC-2: `web/proxy.ts:99` reads the renamed cookie via the same `SESSION_COOKIE` import — no source change at this line if AC-1's constant rename is done in `lib/auth.ts`.
- [ ] AC-3: `web/proxy.ts` exempts `pathname.startsWith("/v1/")` AND `pathname === "/metrics"` so curl-style direct backend traffic that traverses the Next.js host is not 307'd to `/login`. Exact exemption set documented in D2.
- [ ] AC-4: BFF→backend call-site shape decided (D1) — Cookie pass-through vs `Authorization: Bearer`. Verdict applied uniformly. Reading the codebase confirms all BFF routes already use `Authorization: Bearer ${bearer}`; D1 is **Authorization Bearer**, no call-site changes required beyond the constant rename.
- [ ] AC-5: `web/proxy.test.ts` fixtures at lines 170/239 (and any others) updated — `sa_session_token` → `atlas_jwt`. All proxy tests pass.
- [ ] AC-6: Other vitest specs updated. `web/scripts/capture-readme-screenshots.ts:113` hard-codes the cookie name — refresh.
- [ ] AC-7: Backend regression check documented: `curl -i http://localhost:<port>/v1/me` with no auth returns 401 JSON. Confirmation pasted in the decisions log.
- [ ] AC-8: Playwright e2e — extend an auth-flow spec to assert OIDC callback → `/dashboard` does NOT infinitely loop. Inject the JWT directly (the existing fixture does this) and assert `/dashboard` renders without a redirect to `/login`. The existing `bff-cookie-production-build.spec.ts` already exercises this path; verify it still passes after the constant rename.
- [ ] AC-9: CHANGELOG entry under "Fixed".
- [ ] AC-10: CHANGELOG operator note — existing users must log in once after deploy (any leftover `sa_session_token` cookie in their browser is now ignored; the OAuth callback will set `atlas_jwt` on next login).
- [ ] AC-11: Decisions log `docs/audit-log/206-bff-cookie-migration-decisions.md` with D1 (cookie-vs-Authorization shape), D2 (proxy exemption set), D3 (honest CI-delta scan results).

## Anti-criteria (P0)

- **P0-A1**: NO new auth mechanism. The cookie value is still the OAuth-AS-issued JWT; the backend's slice-190 `jwtmw` middleware is unchanged.
- **P0-A2**: NO bypass of backend JWT validation on `/v1/*`. The proxy exemption ONLY removes the Next.js-layer redirect-to-login; the backend continues to enforce auth via `jwtmw`. AC-7 documents the verification.
- **P0-A3**: NO `atlas_session` cookie behavior changes. That cookie is slice 190's retirement target; this slice is in `atlas_jwt` territory only.
- **P0-A4**: NO removal of the `SESSION_COOKIE` constant name. The 30+ import sites depend on the NAME being stable; only the VALUE changes. Keep the constant exported.
- **P0-A5**: No vendor-prefixed test fixture tokens. Use neutral test strings (`"test-bearer-fixture"` etc. preserved).
- **P0-A6**: No new dependencies.

## Constitutional invariants honored

This slice is frontend-only (`web/`) — does not touch any backend invariant (RLS, tenancy, evidence ledger, OSCAL, OPA, audit-log). It corrects a wire-shape mismatch introduced by slice 197 retiring the backend opaque-bearer path without migrating the BFF cookie reader.

## Canvas references

- [`Plans/canvas/09-tech-stack.md`](../../Plans/canvas/09-tech-stack.md) — OAuth Authorization Server table row references slices 187-192 + ADR-0003.

## Dependencies

- **#189** (OAuth callback writes `atlas_jwt`) — merged.
- **#190** (`jwtmw` middleware accepts `Bearer eyJ...` shape) — merged.
- **#197** (legacy bearer middleware retired) — merged. THIS is the slice that left the BFF stranded.
- **#198** (OIDC first-install bootstrap) — merged.

## Skill mix

- `web/lib/auth.ts` constant editor.
- `web/proxy.ts` exemption editor.
- Vitest fixture refactor (proxy.test.ts).
- Playwright spec verification.
- CHANGELOG author.

## Notes for the implementing agent

Trap to avoid: do NOT rename the `SESSION_COOKIE` constant itself (only the value). 30+ files import the named symbol; renaming the symbol fans out across the entire BFF and adds review burden for zero behavior change. The minimal-blast-radius fix is a one-line value change in `web/lib/auth.ts`, then walk every test file that hard-codes the literal `"sa_session_token"` string and replace it with `"atlas_jwt"`.

Trap two: do NOT broaden the proxy matcher exemption to `pathname.startsWith("/api/v")` — that would silently expose `/api/vendors` etc. (slice 092 P0-A1 discipline). Use a startsWith on `/v1/` (which is the platform's exact URL prefix) and exact-equality for `/metrics`.

Provenance: filed 2026-05-22 via the deployment-blocking bug report on v1.14.0. PR #502 is the claim-stake; this spec is the engineering contract.
