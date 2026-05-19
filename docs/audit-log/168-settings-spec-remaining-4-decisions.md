# 168 — Decisions log (settings spec remaining 4 ACs)

Slice: [`168-settings-spec-remaining-4-ac-failures`](../issues/168-settings-spec-remaining-4-ac-failures.md)

Date: 2026-05-19

Engineer: continuous-loop subagent.

## Summary

After slice 165 iter 2 + slice 166 production fix, 7/11 settings ACs pass in CI Playwright. This slice diagnoses the remaining 4 (AC-1, AC-2, AC-3, AC-4) and applies narrow fixes inside the surface the slice doc permits (test fixtures, spec locators, no production code beyond the single-testid carve-out).

Result: **3 of 4 ACs fixed inside this PR**; **AC-2 routed to spillover slice 170** because its production-bug fix exceeds the single-testid carve-out in P0-A4.

| AC   | Cause                                                                                                   | Classification (P0-A1)               | Fix surface                 | Substantive lines |
| ---- | ------------------------------------------------------------------------------------------------------- | ------------------------------------ | --------------------------- | ----------------- |
| AC-1 | `<CardTitle>` renders as a shadcn `<div>`, not an `<h*>` — `getByRole("heading")` never matches         | spec drift                           | `web/e2e/settings.spec.ts`  | 4                 |
| AC-2 | `AppearanceSelector` useState lazy init is SSR-guarded; client never re-reads localStorage on hydration | production bug → SPILLOVER slice 170 | none in this PR             | 0 (deferred)      |
| AC-3 | `ON CONFLICT DO NOTHING` left stale prefs from prior runs untouched                                     | test-infra gap                       | `fixtures/e2e/settings.sql` | 3                 |
| AC-4 | Two buttons match `/Issue token/` (trigger + form submit) → strict-mode violation                       | spec drift                           | `web/e2e/settings.spec.ts`  | 5                 |

Total substantive lines (across all 4 ACs, this PR): 12. Slice doc AC-4 cap was ≤ 20. PASS.

## Per-AC diagnoses

### AC-1 — Profile heading not visible

**Hypothesis selected: A1-H3** ("the heading text IS 'Profile' but the Card renders it via `<CardTitle>` which is `slot='title'` in shadcn, NOT a heading element").

**Evidence.**

- `web/components/ui/card.tsx:36` — `function CardTitle({ className, ...props }: React.ComponentProps<"div">)`. CardTitle is a `<div>` with no `role="heading"` and no `aria-level`. shadcn's intentional design — `CardTitle` is a slot for visual styling, not a semantic heading; consumers add semantic roles if they need them.
- `web/app/(authed)/settings/page.tsx:240` — `<CardTitle>Profile</CardTitle>`. So the rendered DOM has `<div>Profile</div>`, not `<h2>Profile</h2>`.
- CI artifact `playwright-report` (downloaded from run 26106619396): the AC-3 trace's DOM snapshot at the same render confirms it — line 83 of `error-context.md`: `- generic [ref=e35]: Profile`. The `generic` aria role means "container with no semantic role" — i.e., a `<div>`. No heading element exists with the text "Profile".
- Failure log: `Error: expect(locator).toBeVisible() failed: element(s) not found` at `web/e2e/settings.spec.ts:49:66` (the `getByRole("heading", { name: /Profile/ })` line). The locator resolved to ZERO elements (not "the wrong element") — there is no heading.

**Classification: spec drift.** The spec was written against an assumption that didn't hold in shadcn-land. Fix is in the spec, not in production code.

**Fix.** Replace the second assertion in AC-1 with a `getByText("Profile")` scoped to the profile section. Preserves the spec's intent ("the section title 'Profile' is visible") without depending on shadcn making `CardTitle` a heading.

```ts
await expect(
  page.getByTestId("settings-section-profile").getByText("Profile"),
).toBeVisible();
```

The `.getByTestId(...).getByText(...)` chain scopes the text lookup to the profile section so we don't accidentally match the literal string "Profile" elsewhere on the page (e.g., in a nav link).

**Alternative considered (and ruled out).** Add a one-line `aria-level="2" role="heading"` to the `<CardTitle>` for the profile section via the page.tsx — this would have been the P0-A4 carve-out (one testid-equivalent prop addition). Ruled out because:

1. shadcn's `CardTitle` is used across the site (every Card has one); making the role-fix in `card.tsx` is broader-scope production code and would need re-validation across every Card. Making it inline on `<CardTitle role="heading" aria-level={2}>Profile</CardTitle>` is OK semantically, but the spec-only fix is even narrower (zero production source touched).
2. The other 6 sections (`Appearance`, `Notifications`, `Personal API tokens`, `Active sessions`, etc.) all use the same `<CardTitle>...</CardTitle>` shape and would gain hidden behavior drift if we role-promoted one but not the others.

The spec fix is correct and complete; the production source stays untouched.

### AC-2 — Theme picker doesn't persist across reload

**Hypothesis selected: A2-H3 (sibling)** ("hydration race"). The actual mechanism is more specific than A2-H3 framed.

**Evidence.**

- `web/app/(authed)/settings/page.tsx:481-484`:
  ```tsx
  const [theme, setTheme] = useState<Theme>(() => {
    if (typeof window === "undefined") return DEFAULT_THEME;
    return readTheme(window.localStorage);
  });
  ```
  The lazy initializer guards against SSR (`typeof window === "undefined"`) and returns `DEFAULT_THEME` ("system") in that branch.
- Next.js App Router server-renders client components on the initial page load. During that SSR pass, `typeof window === "undefined"` is true → the initializer returns `DEFAULT_THEME`. The server-rendered HTML ships with `data-selected="true"` on the `system` button.
- On client hydration, React re-uses the server-rendered state. **The lazy initializer is NOT re-run on the client.** localStorage is never consulted on a fresh page load.
- The user's `choose("dark")` does write to localStorage (line 489: `writeTheme(window.localStorage, next)`), but on the next reload the picker resets to "system" because the SSR pass freezes that into the hydrated state.
- CI failure trace `test-results/settings--settings-user-fa-de491-.../error-context.md`:
  - `Locator: getByTestId('settings-theme-option-dark')`
  - `Expected: "true"`
  - `Received: "false"`
  - `14 × locator resolved to <button ... data-selected="false" ...>` — for the entire 5-second polling window, dark stays unselected after the reload.
- The "Dark-mode stylesheet pending" Alert at page.tsx:462-468 literally says "Your selection is saved to localStorage and applied via the data-theme attribute" — and the contract is broken: the selection is saved, but never re-applied on reload.

**Classification: production bug.** The fix is a `useEffect` (or `useSyncExternalStore`, or `dynamic({ ssr: false })` wrap) that syncs from localStorage post-mount. None of those is a single-testid attribute addition; all of them are multi-line state-management changes in production code.

**Decision: SPILLOVER to slice 170.** Per P0-A4 ("Does NOT touch production code beyond at most ONE testid attribute addition per AC"), this fix is out of scope for slice 168. Filed slice 170 at `docs/issues/170-settings-theme-picker-hydration-bug.md` with 5 ACs, 6 P0 anti-criteria, and three viable fix patterns documented.

AC-2 stays red in the slice 168 CI Playwright run. The slice 168 PR ships fixes for AC-1, AC-3, AC-4; AC-2 closes once slice 170 ships.

**Alternative considered (and ruled out): "single-line testid carve-out".** There is no single-testid fix that makes AC-2 pass. The bug is in the runtime state-sync; no DOM attribute can simulate a localStorage read. Ruled out.

**Alternative considered (and ruled out): `test.fixme()` with slice-170 ref.** P0-A2 forbids commenting out AC bodies. `test.fixme()` is technically not a comment — it's a Playwright-native skip with documentation — but the spirit of P0-A2 is "don't hide the failure"; leaving the AC red on CI is more honest than skipping it with a TODO. The slice 168 PR explicitly documents that the Playwright result is 3/4 newly green (not 4/4), and the audit log explains why.

### AC-3 — Notification toggle's initial state is wrong

**Hypothesis selected: variant of A3-H1 / A3-H4** ("the click target is wrong OR the toggle's expected initial state is wrong"). Actual cause: the toggle's CHECKED state is wrong because the seed didn't reset.

**Evidence.**

- CI failure trace error: `Error: locator.check: Clicking the checkbox did not change its state` at `web/e2e/settings.spec.ts:85:18`. AND a preceding `Error: expect(locator).not.toBeChecked() failed` at line 79.
- Trace DOM snapshot (line 150): `checkbox "email" [checked] [active]` for the `audit_period_assignment` row. The toggle is ALREADY checked when the test starts — but the spec expects it unchecked.
- Seed fixture `fixtures/e2e/settings.sql:108-118`:
  ```sql
  INSERT INTO user_notification_preferences (
      tenant_id, user_id, event, channel, enabled
  ) VALUES (
      '00000000-0000-0000-0000-00000000d3a0',
      '44444444-4444-4444-4444-444444440001',
      'audit_period_assignment',
      'email',
      false
  )
  ON CONFLICT DO NOTHING;
  ```
  `ON CONFLICT DO NOTHING` — if a prior test run (or prior CI run on the same docker-compose Postgres) left a row with `enabled=true` for that key, the new INSERT is a no-op. The seed never resets the state.
- Migration `migrations/sql/20260516000003_me_endpoints.sql:59-60` confirms the PK is `(tenant_id, user_id, event, channel)` — so `ON CONFLICT (tenant_id, user_id, event, channel) DO UPDATE` is the correct upsert path.
- The first time the spec ran (clean DB), AC-3's `toggle.check()` flipped the row to `enabled=true`. The reload assertion at line 88 passed: `await expect(toggle).toBeChecked()`. But on subsequent re-runs (CI retries, or same docker-compose Postgres reused across runs), the seed didn't reset and AC-3 starts with `enabled=true`.

**Classification: test-infra gap.** The seed needs to be deterministic across re-runs. Fix is in the fixture, not in production code or spec body.

**Fix.** Change `ON CONFLICT DO NOTHING` to `ON CONFLICT (tenant_id, user_id, event, channel) DO UPDATE SET enabled = EXCLUDED.enabled`. The upsert guarantees the seeded state every run.

```sql
INSERT INTO user_notification_preferences (
    tenant_id, user_id, event, channel, enabled
)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    'audit_period_assignment',
    'email',
    false
)
ON CONFLICT (tenant_id, user_id, event, channel) DO UPDATE
SET enabled = EXCLUDED.enabled;
```

**Alternative considered (and ruled out): TRUNCATE / DELETE before INSERT.** The fixture's overall idempotency pattern (slice 122) is "DO NOTHING on conflict" for everything; adding a DELETE breaks that pattern and would risk taking down rows the OTHER specs depend on (the test isolation is by tenant + user, and a TRUNCATE would zap other tenants' rows). Upsert is the smaller change.

**Alternative considered (and ruled out): change the spec's expectation.** The AC's documented contract is "seed starts at false → flip → assert true after reload". Changing the spec to "regardless of starting state, end in a known state" would lose the round-trip semantics the AC is supposed to verify. The fixture is the right surface.

### AC-4 — Issue Token button click ambiguous

**Hypothesis selected: A4-H1 sibling** — actually not testid drift; both required selectors are present and unique, but a SECONDARY role-based selector matches multiple elements.

**Evidence.**

- CI failure trace: `Error: locator.click: Error: strict mode violation: getByRole('button', { name: /Issue token/ }) resolved to 2 elements` at `web/e2e/settings.spec.ts:99:61`.
- `page.tsx:798-804`:
  ```tsx
  <Button
    size="sm"
    onClick={() => setIssueOpen(true)}
    data-testid="settings-token-issue-button"
  >
    Issue token
  </Button>
  ```
- `page.tsx:1117-1124`:
  ```tsx
  <Button onClick={submit} disabled={submitting}>
    {submitting ? "Issuing..." : "Issue token"}
  </Button>
  ```
- After AC-4's click at line 97 sets `issueOpen = true`, BOTH buttons are mounted simultaneously. The trigger button doesn't unmount when the form opens (page.tsx:790 — the issue button is in the section header card, the form renders in the section's CardContent below; they coexist).
- `getByRole('button', { name: /Issue token/ })` matches both because the trigger says "Issue token" and the form submit says "Issue token" (or "Issuing..." during the pending state, which the regex `/Issue token/` matches via "Issue tok").

**Classification: spec drift.** The role-based locator is too broad; scoping to the form disambiguates.

**Fix.** Scope the role lookup to the form via a chained locator:

```ts
const issueForm = page.getByTestId("settings-token-issue-form");
await issueForm.waitFor();
await issueForm.getByRole("button", { name: /Issue token/ }).click();
```

This preserves the spec's intent (click the form's submit button after the form opens) without depending on the button text being unique on the page.

**Alternative considered (and ruled out): rename one of the buttons.** Renaming the trigger to "+ Issue token" or the submit to "Save" would lose copy parity with the mockup (Plans/mockups/settings.html). Ruled out — the user-visible text is correct; the spec selector is what needs scoping.

**Alternative considered (and ruled out): add `data-testid="settings-token-issue-submit"` to the form's submit button.** That's the P0-A4 single-testid carve-out and would also work. Ruled out because the scoped role lookup is even narrower (zero production source touched) and is the canonical Playwright disambiguation pattern.

## Files changed

| File                                                        | Lines changed (substantive) | Surface          |
| ----------------------------------------------------------- | --------------------------- | ---------------- |
| `web/e2e/settings.spec.ts`                                  | 9                           | spec drift fixes |
| `fixtures/e2e/settings.sql`                                 | 3                           | test-infra fix   |
| `docs/issues/170-settings-theme-picker-hydration-bug.md`    | NEW                         | spillover slice  |
| `docs/audit-log/168-settings-spec-remaining-4-decisions.md` | NEW                         | this file        |
| `docs/issues/_STATUS.md`                                    | drift block + row flips     | book-keeping     |

Total substantive code/fixture lines changed: 12. Cap was 20. PASS.

## P0 anti-criteria audit

| Anti-criterion                                                                                      | Status                                                                                                                                                                                                                   |
| --------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| P0-A1 — classify each fix per (test-infra / spec drift / production bug)                            | PASS — every AC classified in its diagnosis section: AC-1 spec drift, AC-2 production bug (spillover), AC-3 test-infra gap, AC-4 spec drift                                                                              |
| P0-A2 — does NOT comment out failing AC bodies                                                      | PASS — AC-2's body is untouched (no comment-out, no `.fixme()`); it will fail in this PR's CI and that's documented honestly                                                                                             |
| P0-A3 — does NOT add new assertions                                                                 | PASS — every locator correction preserves the spec's intent (AC-1 still asserts "Profile" visibility; AC-3 still asserts the toggle round-trip; AC-4 still asserts the form's submit click). No new `expect(...)` lines. |
| P0-A4 — does NOT touch production code beyond 1 testid per AC                                       | PASS — `web/app/(authed)/settings/page.tsx` is UNTOUCHED. `web/components/ui/card.tsx` is UNTOUCHED. AC-2's production fix is deferred to spillover slice 170, not crammed into this PR.                                 |
| P0-A5 — does NOT modify slice 162 (sessions wire shape) or 163 (api tokens) backend shapes          | PASS — no Go files changed.                                                                                                                                                                                              |
| P0-A6 — does NOT promote `Frontend · Playwright e2e` to required-checks                             | PASS — `.github/branch-protection.json` untouched.                                                                                                                                                                       |
| P0-A7 — neutral test tokens only                                                                    | PASS — no new tokens introduced.                                                                                                                                                                                         |
| P0-A8 — does NOT change the test harness role binding                                               | PASS — `web/e2e/seed.ts` UNTOUCHED. `web/e2e/fixtures.ts` UNTOUCHED.                                                                                                                                                     |
| P0-A9 — does NOT bundle slice 166 production fix                                                    | PASS — slice 166 is already merged on main (`e76e5cf`); this PR builds on top of it but does not re-touch the `allowed-kinds-display.ts` helper.                                                                         |
| P0-A10 — does NOT investigate or modify the 7 currently-passing settings ACs OR the other 121 specs | PASS — only AC-1 / AC-3 / AC-4 bodies are touched; AC-5/6/7/8/9/10/11 are byte-for-byte unchanged in this PR. No other spec files touched.                                                                               |

## Verification

- **TypeScript / format / lint:** `pre-commit run --all-files` (from the worktree root) — TBD on commit (results captured in PR description).
- **Vitest:** No vitest changes in this PR (no production source touched). The slice 166 helper module's 11 tests + the 503-total suite are unaffected.
- **Playwright local repro:** the worktree does not have a running docker-compose stack; reproducing locally would consume the slice's 1d budget. The fix is mechanical (4 cause→fix pairs validated against the CI trace artifact at run 26106619396, job 76772362534). The next CI Playwright run on the PR will be the authoritative gate.

## Expected CI result

After this PR's Playwright e2e job runs, the four settings ACs covered here transition as follows:

| AC   | Before this PR | After this PR | Closed by             |
| ---- | -------------- | ------------- | --------------------- |
| AC-1 | red            | green         | this slice (168)      |
| AC-2 | red            | red           | slice 170 (spillover) |
| AC-3 | red            | green         | this slice (168)      |
| AC-4 | red            | green         | this slice (168)      |

Net: 7/11 → 10/11 settings ACs green. The last (AC-2) closes when slice 170 ships.

## Confidence

High on AC-1 / AC-3 / AC-4. Each fix has a direct cause→fix chain validated against the CI trace artifact + static code analysis. The fixes are mechanical and narrow.

High on the AC-2 spillover decision. The hydration bug is unambiguous from the source (the lazy initializer is SSR-guarded, no `useEffect` follow-up, no `dynamic({ ssr: false })` wrap). A single-testid fix cannot bridge that gap.
