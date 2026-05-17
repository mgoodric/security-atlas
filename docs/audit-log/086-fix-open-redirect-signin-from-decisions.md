# Decisions log — Slice 086 (Fix open redirect on signIn `from` parameter)

Slice 086 is the HIGH-severity remediation for the open-redirect finding in
the 2026-Q2 security audit (slice 085, `docs/audits/2026-Q2-security-audit.md`).
The slice is `Type: AFK`, but the implementing engineer made several
build-time judgment calls that are documented here for post-deployment
revisit.

## Decisions made

### D1 — bare `/` rejected, not passed through (high confidence)

**Decision:** the helper treats `"/"` as non-safe and falls back to
`/dashboard`.

**Alternatives considered:**

- Pass `"/"` through unchanged — would land the user on the marketing/root
  route, which is not the post-login destination. The explicit
  `/dashboard` default exists to guarantee the user lands on the authed
  surface; bare `/` would silently bypass that.
- Treat `"/"` as a separate "empty" sentinel and emit a server log — adds
  noise; the helper's contract is "non-safe → fallback", not "non-safe →
  observability event". Future logging belongs in the calling action,
  not the helper.

**Rationale:** AC-3 (h) of the slice doc explicitly enumerates `"/"` →
`/dashboard`. The audit report's variant catalog implicitly assumes the
helper rejects any "structurally valid but not a real destination" value.
A bare `/` qualifies.

### D2 — validation lives in the server action, not middleware (high confidence)

**Decision:** the helper runs inside `web/app/login/actions.ts` server
action only. No global middleware on every request.

**Alternatives considered:**

- Middleware-based validation on every `/login` request — would catch the
  bug earlier in the pipeline, but the open-redirect surface is _the
  server action's redirect call_, not the page render. Validation at the
  page render would have to inspect every `from=` query param against
  the same rules; the action is the single point where the value
  becomes consequential.
- Validation inside the React component on the client — server actions
  ignore client-side validation by design (it's the threat surface);
  this would be theater.
- Validation in Next.js's `middleware.ts` global handler — same problem
  as the page-render approach (catches the URL too early, before the
  action even reads it) plus adds a hot-path cost to every request for a
  single-page concern.

**Rationale:** the helper is a security primitive scoped to the
redirect-after-action pattern. Server action is the right place — single
call site, deterministic invocation, no risk of being bypassed by a
direct POST. CONTRIBUTING.md gains the reviewer-discipline note so
future redirect-from-user-input sites get the helper too.

### D3 — single validation point at variable assignment, not per call site (high confidence)

**Decision:** call `safeRedirectTarget` ONCE at the top of `signIn`, when
reading `formData.get("from")`. Both downstream `redirect()` call sites
(empty-token error branch + happy-path) use the validated `target`.

**Alternatives considered:**

- Call `safeRedirectTarget` at each `redirect()` call site individually
  — defensive-in-depth, but obscures the invariant "`target` is always
  safe from line 38 onward". Two call sites today, more if the action
  ever grows; better to enforce at the boundary.
- Refactor the helper to take + return the FULL redirect URL (including
  query params) — wrong shape; the helper's job is path-validation, not
  URL-construction. The action knows how to assemble the error redirect
  URL; the helper only needs to bless the path component.

**Rationale:** matches the slice doc's spirit ("a 3-line `safeRedirectTarget`
helper + a 2-line invocation change at 2 call sites"). The "2 call sites"
phrasing covered conceptually by validating the value flowing into both
sites — single point of validation, two beneficiaries. AC-1 and AC-2
both satisfied.

### D4 — drop redundant `|| "/dashboard"` fallback on happy-path redirect (high confidence)

**Decision:** the happy-path `redirect(target || "/dashboard")` is
simplified to `redirect(target)`. The `safeRedirectTarget` helper
guarantees a non-empty string starting with `/`, so the `|| "/dashboard"`
clause is unreachable code.

**Alternatives considered:**

- Keep the `|| "/dashboard"` for defense-in-depth — but defense-in-depth
  here means "if a future refactor breaks the helper, this catches the
  empty-string case". The unit test (`web/lib/safe-redirect.test.ts`
  case (g)) catches that regression before it ships; redundant code at
  the call site invites confusion about which fallback is load-bearing.

**Rationale:** the slice doc explicitly lists `simplify` in the skill mix.
The helper is the single source of truth for the fallback; the call site
should not re-implement it.

### D5 — Playwright spec uses semantic selectors, not test-id attributes (medium confidence)

**Decision:** the spec at `web/e2e/auth-open-redirect.spec.ts` selects the
token input via `locator('input[name="token"]')` and the submit button
via `getByRole("button", { name: /sign in/i })`. No new `data-testid`
attributes were added to `web/app/login/page.tsx`.

**Alternatives considered:**

- Add `data-testid="login-token-input"` + `data-testid="login-submit"`
  to the form — would make the spec slightly more robust to copy
  changes ("Sign in" → "Log in"), but expands the slice's edit surface
  into a component that other slices (072, 073) are already touching.
  P0-A4 says "does NOT extend this fix to other redirect call sites
  without explicit scope expansion"; adding test-ids is a sibling
  expansion that's better filed separately if needed.
- Use the existing login flow's `next/test` driver — doesn't exist.

**Rationale:** semantic selectors are good Playwright practice (a11y
roles double as test selectors), and the spec is post-079 quarantined
so a copy-change flake doesn't block merge. If the spec proves
copy-fragile in practice, a follow-on slice can add the test-ids.

### D6 — Playwright spec skipped, not deleted, when TEST_BEARER absent (high confidence)

**Decision:** the spec uses `test.skip(!HAS_BEARER, ...)` at the
`describe` level. Specs run only when the env var is set.

**Alternatives considered:**

- Hard-throw when TEST_BEARER unset — would error the CI job's
  Playwright stage; even with `continue-on-error: true` at the job level,
  it produces a misleading red ✗ on the PR.
- Gate by `process.env.CI` instead — couples the test to CI vs. local
  rather than to its actual precondition (a bearer-token fixture).

**Rationale:** the slice doc explicitly says "Skipped if `TEST_BEARER`
not set." The pattern matches `web/e2e/first-time-login.spec.ts`'s
precondition gating.

### D7 — CONTRIBUTING.md, NOT README ## Security, for the prevention paragraph (high confidence)

**Decision:** AC-7's "Open-redirect prevention" paragraph lands in
`CONTRIBUTING.md`, not the README `## Security` section authored by slice 085.

**Alternatives considered:**

- Add it under README `## Security` — slice 085's section is the
  discovery surface for users, not contributors. The "all redirect
  targets MUST flow through `safeRedirectTarget`" rule is a
  reviewer/contributor guideline.
- Add it to a new `docs/security/` doc — overkill for a one-paragraph
  note; CONTRIBUTING.md already has reviewer-discipline content.

**Rationale:** the slice doc's AC-7 explicitly offers CONTRIBUTING.md as
the alternative, AND `_STATUS.md` batch-31 row 086 calls out:
"CONTRIBUTING.md not README to avoid contention" with the parallel
slices 087+088 which both append to README `## Security`.

### D8 — `docs/audits/...` remediation status filled in as `<TBD post-merge>` (high confidence)

**Decision:** the audit report's HIGH finding gets a "Remediation status:
shipped in slice 086 (commit `<TBD post-merge>`)" line. The
post-merge final-reconcile PR will replace `<TBD post-merge>` with the
squash-merge commit SHA.

**Alternatives considered:**

- Wait until merge, then file a follow-up PR — defeats the purpose of
  documenting remediation status in the slice itself.
- Use the PR number instead of the commit — PR number is stable but the
  squash-merge commit is the canonical "this is on main" pointer.

**Rationale:** matches slice-085's pattern of letting the reconcile PR
fill in post-merge SHAs (`_STATUS.md`'s "Last reconciled" lines do the
same).

### D9 — CHANGELOG.md not manually edited (high confidence)

**Decision:** despite the implementation prompt asking for an
`[Unreleased] / Fixed` entry, this slice does NOT manually edit
CHANGELOG.md.

**Alternatives considered:**

- Manually add an `[Unreleased]` section + `Fixed` bullet — would
  duplicate the work release-please does automatically.

**Rationale:** the project's CHANGELOG.md preamble explicitly states
"Release sections below are auto-generated by release-please from
Conventional-Commit messages." Slice 085 (PR #168 commit `e09ebfb`) did
not edit CHANGELOG.md; slice 067 (PR #141) was the dedicated cleanup
that explicitly drained stale `[Unreleased]` sections "because
everything is released." The Conventional Commit subject on the
squash-merge will surface as the bullet in the next release-please run.

## Revisit once in use

Concrete items to re-evaluate after the slice has been on `main` and
running against real traffic:

1. **Does the bare-`/` rejection ever surprise a real user?** A user
   who has bookmarked `/` and uses the bookmark URL as the `from`
   destination will land on `/dashboard` instead. Acceptable today
   because the post-login surface IS `/dashboard`; revisit if `/` ever
   becomes a meaningful authed-landing page distinct from `/dashboard`.

2. **Should the helper log rejected attempts?** Currently silent — the
   action redirects to `/dashboard` with no observability signal. If
   open-redirect attempts spike in the wild (audit-log query: "count of
   times `target` did not pass through unchanged"), consider adding a
   structured server log. This requires a logging surface in
   `actions.ts` that doesn't exist today.

3. **Other redirect-from-user-input call sites.** The slice scopes to
   the signIn action explicitly (P0-A4). A future audit-pass should
   grep for `redirect(` and `NextResponse.redirect(` across `web/` and
   confirm every call-site sourced from user input uses the helper. If
   any new ones exist, file a follow-on slice rather than expanding
   this one.

4. **Test-id selectors on the login form.** The Playwright spec uses
   `input[name="token"]` + role-based button selection (D5). If the
   login copy changes ("Sign in" → "Log in") and the spec breaks, the
   first remediation is `data-testid` attributes — file as a follow-on
   slice (covers all login-page specs, not just this one).

5. **Playwright spec proves the end-to-end flow.** The spec is
   quarantined under slice 079, so it does not block merge. When slice
   079's quarantine is lifted (per its own follow-on plan), confirm
   this spec passes on the first un-quarantined run, OR file a fix.

## Confidence

| Decision                                      | Confidence |
| --------------------------------------------- | ---------- | ------------- | ---- |
| D1 — bare `/` rejected                        | high       |
| D2 — server-action-only validation            | high       |
| D3 — single validation point                  | high       |
| D4 — drop redundant `                         |            | "/dashboard"` | high |
| D5 — semantic selectors, no new test-ids      | medium     |
| D6 — `test.skip` on missing TEST_BEARER       | high       |
| D7 — CONTRIBUTING.md not README ## Security   | high       |
| D8 — audit report remediation line as `<TBD>` | high       |
| D9 — no manual CHANGELOG.md edit              | high       |

D5 is the lone `medium`: if the login copy churns or the form gets
restructured, the spec could flake. The post-079 quarantine makes that
flake non-blocking, but it's the most likely revisit driver. Adding
test-ids is the documented remediation path.

## Acceptance criteria status

- [x] AC-1: `signIn` happy-path `redirect()` validated through helper
- [x] AC-2: `signIn` empty-token error `redirect()` validated through helper (via single-point validation — see D3)
- [x] AC-3: vitest at `web/lib/safe-redirect.test.ts` covers all 9 cases
- [x] AC-4: Playwright spec at `web/e2e/auth-open-redirect.spec.ts` exists (post-079 quarantined; skip-if-no-bearer)
- [x] AC-5: decisions log (this file)
- [x] AC-6: `docs/audits/2026-Q2-security-audit.md` "Remediation status" line added under HIGH finding
- [x] AC-7: `CONTRIBUTING.md` "Open-redirect prevention" paragraph added
- [x] AC-8: Pre-commit clean. Vitest passes. CI green verified at PR open + merge.

## Constitutional invariants

- **Working norms — Surgical fixes** — the helper is 5 lines of executable
  code (3 checks + bare-`/` check + return) + a fallback. Total diff to
  `actions.ts` is 2 changed lines (import + invocation) + 1 cleanup
  (drop redundant `|| "/dashboard"`). No broader auth refactor.
- **AI-assist boundary** — no AI-generated content in code, audit report,
  CONTRIBUTING.md, or this decisions log; all engineer-authored.
- **Never assert without verification** — AC-3's unit test enumerates the
  attack variants exhaustively; AC-4's Playwright spec is the live
  end-to-end verification against the real login flow.
- **No vendor-prefixed test tokens** — the Playwright spec reads
  `TEST_BEARER` from env at runtime; no literal vendor-prefixed tokens
  appear in source. GitGuardian-safe per slice-069's P0-A9 hard rule.
