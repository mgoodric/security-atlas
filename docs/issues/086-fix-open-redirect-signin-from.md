# 086 — Fix open redirect on signIn `from` parameter

**Cluster:** Auth
**Estimate:** 0.25d
**Type:** AFK

## Narrative

Surfaced by the 2026-Q2 security audit (slice 085). **HIGH severity finding.**

`web/app/login/actions.ts:23,55` accepts the `from` form field (originating from `/login?from=...` search param) and passes it directly to Next.js's `redirect(target || "/dashboard")` without validation:

```ts
const target = String(formData.get("from") ?? "/dashboard");
// ...
redirect(target || "/dashboard");
```

**Attack:** `https://atlas.example.com/login?from=https://evil.example.com/phish`. A user clicking this link and signing in successfully is redirected to attacker-controlled origin. The session cookie is `HttpOnly` so it doesn't leak via JS, but the trust signal of "I just signed into my GRC tool, this destination must be safe" enables credential-phishing pivots (attacker mounts a clone screen asking for additional credentials).

**Variants of the attack to block:**

- `?from=https://evil.com/phish` — fully-qualified URL to external host
- `?from=//evil.com/phish` — protocol-relative URL (browser interprets as `https://evil.com/phish`)
- `?from=javascript:alert(1)` — javascript-URL scheme (some browsers + Next.js's redirect handler may permit; defense-in-depth reject)
- `?from=/login?from=https://evil.com` — recursive open redirect (target IS a valid relative path but its content re-triggers the bug)
- `?from=\\evil.com\path` — backslash-prefixed (some path-parsers normalize to forward-slash)

**Fix:** validate `target` is a safe relative path. Specifically: must match `^/[^/]` (starts with `/`, second character not `/`). Reject `?from=` values that don't match; fall back to `/dashboard`. The validation is a 3-line server-side function.

```ts
function safeRedirectTarget(target: string): string {
  if (!target.startsWith("/")) return "/dashboard";
  if (target.startsWith("//")) return "/dashboard"; // protocol-relative
  if (target.startsWith("/\\")) return "/dashboard"; // backslash injection
  return target;
}
```

## Acceptance criteria

- [ ] AC-1: `web/app/login/actions.ts` `signIn` action validates the `target` value via a new `safeRedirectTarget` helper (or inline equivalent) before passing to `redirect()`. The helper rejects fully-qualified URLs, protocol-relative URLs (`//evil.com`), javascript-URL schemes, and backslash-prefixed paths. Falls back to `/dashboard`.
- [ ] AC-2: `web/app/login/actions.ts` ALSO validates `target` in the empty-token error branch (line 26-29) — same helper, same fallback — so the error redirect doesn't itself become an open redirect.
- [ ] AC-3: A unit test at `web/app/login/actions.test.ts` (or `web/lib/safe-redirect.test.ts` if the helper is extracted) covers: (a) `/dashboard` → `/dashboard`; (b) `/risks/123` → `/risks/123`; (c) `https://evil.com/phish` → `/dashboard`; (d) `//evil.com/phish` → `/dashboard`; (e) `javascript:alert(1)` → `/dashboard`; (f) `\\evil.com\path` → `/dashboard`; (g) empty string → `/dashboard`; (h) `/` → `/dashboard` (root with just a slash); (i) `/login?from=https://evil.com` → `/login?from=https://evil.com` (relative path passes; the SECOND-order redirect at `/login` is itself fixed by AC-1).
- [ ] AC-4: Playwright spec (under slice 069's runner, post-079 quarantined) added at `web/e2e/auth-open-redirect.spec.ts` asserts the fix end-to-end: navigate to `/login?from=https://evil.com/phish`, sign in with `TEST_BEARER`, assert the post-sign-in URL is `/dashboard`, not the attacker URL. Skipped if `TEST_BEARER` not set.
- [ ] AC-5: A `docs/audit-log/086-fix-open-redirect-signin-from-decisions.md` records: (1) why `/` alone is rejected (the root path lands on the marketing/dashboard route — should not bypass `/dashboard` default), (2) whether the validation is server-action-only or also middleware (decision: server-action-only is sufficient; middleware on every request is overkill for a single-page validation).
- [ ] AC-6: `docs/audits/2026-Q2-security-audit.md` is appended with a "Remediation status" line under the HIGH finding pointing at this slice's merge commit.
- [ ] AC-7: CONTRIBUTING.md (or the README "Security" section authored by slice 085) gains a brief "Open-redirect prevention" paragraph documenting the helper + the rule "all redirect targets MUST be validated via `safeRedirectTarget` before passing to `redirect()` / `NextResponse.redirect()` / equivalent." Reviewer-discipline note for future contributors.
- [ ] AC-8: Pre-commit clean. CI green. Vitest passes including the new test.

## Constitutional invariants honored

- **Working norms — Surgical fixes**: smallest viable change. A 3-line helper + a 2-line invocation change at 2 call sites + tests. No broader auth refactor.
- **AI-assist boundary**: nothing AI-generated.
- **CLAUDE.md "Never assert without verification"**: AC-4's Playwright spec is the live verification that the fix works end-to-end against the actual login flow, not just unit-tested.

## Canvas references

- `Plans/canvas/01-vision.md` — the v1 persona is a solo security leader; their trust signal that the GRC tool is safe to interact with is load-bearing for the product's value-prop. An open redirect undermines that.

## Dependencies

- **034** (OIDC RP + local users + api_keys admin, merged) — the auth flow this slice hardens
- **069** (verification suite — Playwright + vitest, merged) — the test runner AC-4 uses

## Anti-criteria (P0 — block merge)

- **P0-A1**: Does NOT add a redirect-allowlist of "permitted external domains." The fix is "relative paths only." Allowlisting external domains is a different design with different review requirements.
- **P0-A2**: Does NOT change the redirect default away from `/dashboard`. That's the established post-login destination.
- **P0-A3**: Does NOT mask the validation failure to the user (no silent "we blocked your redirect for security"). A normal user with a normal bookmark hits the `/dashboard` fallback transparently; the only users who notice are attackers who control the `from` value.
- **P0-A4**: Does NOT extend this fix to other redirect call sites in the app without explicit scope expansion. CONTRIBUTING.md (AC-7) makes the pattern available; uses elsewhere should be filed as follow-on slices if needed.

## Skill mix (3–5)

- Next.js server-action + URL-validation idioms
- `security-review` (the threat model + variant catalog)
- vitest + Playwright (test surfaces)
- `simplify` (the helper is 3 lines; the test is 10 lines; the docs paragraph is 5 lines)

## Notes for the implementing agent

- **The helper is short on purpose.** It's a security primitive — a 30-line "URL sanitizer" with edge cases would be the wrong shape. Three checks, fall back to `/dashboard`, done. The test enumerates the attack variants so the helper STAYS short.
- **Don't extend the validation to `apiBaseURL` or other URL inputs.** Different threat model, different code path. If those need hardening, file as separate slices.
- **The Playwright spec (AC-4) needs the post-079 `continue-on-error` setup to not flake the PR.** Verify slice 079 has merged before opening the PR (it has at slice-085-batch-time, but re-check).
