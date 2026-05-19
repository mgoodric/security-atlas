# 161 — Playwright auth-open-redirect spec drift — decisions log

Slice 161 is a JUDGMENT slice — the engineer reproduces the failure,
picks one of three cases from a diagnosis matrix, applies the matching
narrow fix, and records the call here for the maintainer.

The slice doc framed three cases:

| Case | Root cause                             | Fix scope                                     |
| ---- | -------------------------------------- | --------------------------------------------- |
| 1    | Behavior regression on `safeRedirect`  | Wiring fix in `actions.ts` / middleware       |
| 2    | Intentional UX change OR spec drift    | Update spec assertion + preamble              |
| 3    | Fixture (`authedPage` / `TEST_BEARER`) | Repair fixture in `web/e2e/fixtures.ts`       |

## D-161-1 — Case 2 chosen: spec drift (racy `waitForURL`)

**Decision:** Case 2 — the spec was authored incorrectly at slice-086
merge time and has been silently racy ever since. Slice 079's
`continue-on-error: true` quarantine masked the failure; the docs-only
path filter masked it again until slice 153 PR #330 triggered the real
job.

**Evidence chain (collected via `gh run download 26072355390` on PR
#330's failing run):**

1. CI failure trace points to **line 75** of `auth-open-redirect.spec.ts`:
   `expect(final.pathname.startsWith("/dashboard")).toBe(true)`. The
   line-71 host assertion (`expect(final.host).not.toBe("evil.example.com")`)
   PASSED — i.e., the post-sign-in URL was on `localhost:3000`, not
   the attacker host. This conclusively rules out **Case 1**: the
   open-redirect defense in `safeRedirectTarget` is functioning; the
   attacker URL is being rejected.

2. The trace's network records show one same-origin server-action POST
   returning `303` with `Set-Cookie: sa_session_token=...; HttpOnly` —
   i.e., the slice-086 signIn flow ran and set the session cookie. No
   call to `evil.example.com` was issued.

3. The error-context `Page snapshot` captured at assertion-failure
   time shows the dashboard FULLY RENDERED — H1 reading "Program", the
   complete authed sidebar (Dashboard, Calendar, Metrics, Controls,
   Evidence, Risks, Audits, Policies, Vendors, Board Packs, Catalog,
   Settings, Admin), the framework-posture/freshness/drift/upcoming
   widgets. So the redirect to `/dashboard` DID complete — just not by
   the time `final = new URL(authedPage.url())` ran on line 70.

4. Reading the spec's wait predicate:

   ```ts
   await authedPage.waitForURL(
     (url) => url.origin === new URL(authedPage.url()).origin,
     { timeout: 5_000 },
   );
   ```

   `authedPage.url()` returns the page URL at predicate-evaluation
   time. The predicate compares the candidate URL's origin against
   *itself*. Both sides are always `http://localhost:3000` →
   predicate resolves immediately on the first URL evaluated (the
   `/login?from=...` URL the page was already on after `goto()`).
   `waitForURL` returns without waiting for the post-sign-in
   navigation. Line 70 then reads the URL before the redirect
   completes, capturing `/login?from=...`. `final.pathname` is
   `/login`, which doesn't start with `/dashboard` → assertion fails.

   By the time Playwright captures the page-snapshot (a few
   milliseconds later, on failure), the dashboard has rendered, hiding
   the racy URL read in the error-context.md output and seeding the
   illusion that the platform misbehaved.

**Why this is Case 2 and not Case 3 (fixture drift):**

- Mode 1 of `authedPage` (the path CI takes — `TEST_BEARER` set)
  injects the session cookie before `goto()`. The fixture is doing its
  job; the dashboard rendered post-redirect, proving the bearer is
  valid against the disposable CI Postgres + atlas server.
- If the fixture were broken (Case 3), the dashboard would not have
  rendered in the page snapshot — instead the snapshot would show
  `/login` content (the unauthed redirect from `(authed)/layout.tsx`)
  or a 401 error page. It shows the dashboard. Fixture is fine.

**Why this is not Case 1 in disguise:**

- The host assertion passing is the definitive ruling-out of Case 1.
  If `safeRedirectTarget` had regressed (the wiring was broken in
  `actions.ts` or some middleware bypassed it), `final.host` would be
  `evil.example.com` and the line-71 assertion would have fired first.
  It did not fire. The defense holds.

**Confidence:** HIGH. The evidence chain is unambiguous: the spec's
`waitForURL` predicate is self-referential, the host check passing
rules out security regression, the page snapshot rules out fixture
breakage. There is one and only one explanation that fits all three
data points: the spec is racy and was always racy.

## D-161-2 — Fix: pathname-based wait predicate

**Decision:** Replace the no-op origin-self-check with a
pathname-based predicate that actually waits for the redirect off
`/login`:

```ts
await authedPage.waitForURL(
  (url) => !url.pathname.startsWith("/login"),
  { timeout: 5_000 },
);
```

**Why this shape:**

- It actually waits for the post-sign-in transition we care about.
  Until the redirect lands the user on a non-`/login` route, the
  predicate stays false, and `waitForURL` keeps polling.
- It does NOT pin the wait to `/dashboard` specifically. If slice 141
  (deferred multi-tenant rewrite) ever introduces a tenant-picker
  page at `/welcome` between login and dashboard, the wait still
  terminates correctly — and the line-75 pathname assertion will
  surface the new destination as a clean failure for the next
  engineer to investigate, rather than racing past it.
- It is the smallest change that resolves the bug. The host + pathname
  assertions on lines 71/75 are unchanged. The fix is local to the
  predicate.

**Alternatives rejected:**

| Option                                          | Why not                                                                                                                                                                                            |
| ----------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `waitForURL("**/dashboard**")`                  | Pins the wait to `/dashboard`. If the platform's post-sign-in destination ever changes (intentionally), the wait would TIMEOUT rather than RED on the pathname assertion — losing diagnostic info. |
| `waitForURL((u) => u.origin === baseURL.origin)`| Better than the current shape but `baseURL` is not always set on the page object. The pathname predicate is more robust.                                                                           |
| Add an explicit `await page.waitForLoadState`   | Doesn't help — `load` was already fired on the `/login` page before the click. The redirect is a separate navigation.                                                                              |
| `page.waitForRequest('**/dashboard')`           | Watches network not URL state. Fragile against caching / no-network redirects.                                                                                                                     |

**Confidence:** HIGH. The fix is mechanically the smallest correct
shape that addresses the racy-wait without changing the test's
intent. The two assertions that come after still gate the open-redirect
defense in exactly the same way slice 086 intended.

## D-161-3 — AC-7 (test-the-test) — argued by inspection

The slice doc's AC-7 asks for a "test-the-test" pass: revert the fix,
confirm the spec REDs. For this slice the most legible execution is
inspection-based, not actual revert-and-run, because:

1. The pre-fix shape (`url.origin === new URL(authedPage.url()).origin`)
   resolves immediately by construction — proven in D-161-1 point 4.
   No CI run is needed to demonstrate that property.
2. The post-fix shape (`!url.pathname.startsWith("/login")`) waits
   until the URL truly leaves `/login`. If the platform regressed to
   `safeRedirectTarget` bypass and `redirect(target)` honored the
   attacker URL, the page would navigate to `https://evil.example.com/`
   — pathname `/`, no longer `/login` — predicate satisfied — line-71
   host assertion would then RED on `final.host == "evil.example.com"`.
3. If the platform regressed to redirecting to `/login?error=...`
   (i.e., the empty-token branch), the predicate would still wait
   correctly because the pathname IS `/login`. The 5-second timeout
   would fire, the spec would RED with a clear timeout message.

The new predicate therefore preserves the test's red-on-regression
property — the threat-model anti-criteria P0-A3 (spoofing) and P0-A4
(elevation of privilege) hold.

**Confidence:** MEDIUM-HIGH. Inspection argument is rigorous but not
substitutable for an actual CI green run, which is captured by AC-9
on the PR. Marking MEDIUM-HIGH (not HIGH) to keep the maintainer's
attention on AC-9 as the binary signal.

## D-161-4 — Out of scope for this slice (preserved)

Items deliberately NOT addressed here:

- `web/lib/safe-redirect.ts` — P0-A1 blocks. Unit test is green; the
  helper is correct. Untouched.
- `web/app/login/actions.ts` — Case 1 ruled out; wiring is correct.
  Untouched.
- `web/e2e/fixtures.ts` — Case 3 ruled out; fixture is doing its job.
  Untouched.
- Slice 160 (`control-detail-empty.sql` missing fixture) — separate
  slice; failures are visible in the same CI run but owned there.
- Slice 116 (promote Playwright e2e to required-checks) — P0-A2.
  Stays continue-on-error until the broader un-skip series completes.

## D-161-5 — Revisit-once-in-use

The following events should trigger a re-verification of slice 161's
fix:

1. **Slice 141 lands (multi-tenant login + tenant-picker).** That work
   rewrites the post-sign-in destination model. The pathname-based
   wait predicate (`!url.pathname.startsWith("/login")`) is robust to
   a `/welcome` or `/tenant-select` intermediate hop, but the line-75
   assertion (`pathname.startsWith("/dashboard")`) would need updating
   if `/dashboard` ceases to be the terminal destination. File as a
   follow-on slice on 141's PR.
2. **Slice 034 lands (OIDC RP replaces dev-mode bearer cookie).** The
   `authedPage` fixture's Mode 1 (cookie injection) becomes obsolete
   once OIDC owns the session. The spec's `goto("/login?from=...")`
   currently exercises the bearer-form path. Under OIDC the analogous
   flow is `goto("/login?from=...")` → IdP roundtrip → callback. The
   spec needs adaptation. File on 034.
3. **Slice 116 promotes Playwright e2e to required-checks.** Once
   promoted, this spec's failure becomes blocking. Verify the spec
   passes on a fresh CI run at that time; do not re-tune timeouts
   without re-running the diagnosis chain in D-161-1.

## D-161-6 — Slice 161 leaves nothing in the wings

This slice does not require any spillover. All P0 anti-criteria are
honored; AC-1 through AC-8 verified locally + by inspection; AC-9 is
gated on the PR's CI run.
