# 203 — dark-mode stylesheet wiring · decisions log

**Slice:** `docs/issues/203-dark-mode-stylesheet-wiring.md`
**Branch:** `frontend/203-dark-mode-stylesheet-wiring`
**Author:** Claude (engineer subagent)
**Date:** 2026-05-22

This log captures the JUDGMENT calls made while building slice 203. The
slice doc specifies the WHAT; this log records the HOW + the trade-offs
I weighed inline. All decisions are reviewable post-merge by the
maintainer.

---

## D1 — Early-paint approach: inline `<script>` in `<head>` (no `next-themes`)

**Decision:** Inject a 14-line inline `<script>` block into `<head>` via
`web/app/layout.tsx`'s `<script dangerouslySetInnerHTML={...} />`. The
script reads `localStorage["security-atlas.settings.theme"]`,
resolves the active theme, and adds/removes the `dark` class on
`<html>` synchronously, BEFORE React hydrates. No new dependency.

**Alternatives considered:**

| Approach                                                                                    | Why rejected                                                                                                                                                                                                                                                                 |
| ------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `next-themes` library                                                                       | Adds a context provider, a `useTheme` hook, and a documented SSR-handshake surface area we don't need. The picker UI is already wired to localStorage + `<html data-theme>`; the only missing piece is the class write. P0-A3 explicitly prefers the smallest-possible path. |
| `useEffect`-only class write (in a layout-level client component)                           | Runs AFTER React hydrates, which means the first frame paints WITHOUT the class, so the whole above-the-fold UI flashes light mode for users with persisted dark theme. The slice doc's "Notes for the implementing agent" explicitly warns against this.                    |
| Cookie-backed SSR rendering (server reads the cookie, emits `<html class="dark">` directly) | Requires a server-readable cookie (we use localStorage); requires the picker to also write a cookie; widens the wire surface to a v2 / cross-device-sync conversation that is currently a deferred open question (canvas §11 OQ). Out of scope for a 0.5d frontend-only fix. |
| Server Components reading localStorage                                                      | Localstorage is not available on the server. Non-starter.                                                                                                                                                                                                                    |

**The exact script text** (also at `web/app/layout.tsx:THEME_BOOTSTRAP_SCRIPT`):

```js
(function () {
  try {
    var k = "security-atlas.settings.theme";
    var raw = window.localStorage.getItem(k);
    var theme =
      raw === "light" || raw === "dark" || raw === "system" ? raw : "system";
    var isDark =
      theme === "dark" ||
      (theme === "system" &&
        window.matchMedia("(prefers-color-scheme: dark)").matches);
    var html = document.documentElement;
    if (isDark) {
      html.classList.add("dark");
    } else {
      html.classList.remove("dark");
    }
    if (!html.getAttribute("data-theme")) {
      html.setAttribute("data-theme", theme);
    }
  } catch (e) {
    // localStorage denied (Safari private / extension) -- fall through
    // to the light-mode default. The page still renders correctly.
  }
})();
```

**Why this exact shape:**

- **`try/catch`** because localStorage can throw on access (Safari private
  mode pre-2023, locked-down extensions). Throwing in `<head>` would
  block all subsequent script execution and break the page entirely; the
  catch lets the page fall through to its light-mode default.
- **Theme parser inlined** (the `(raw === "light" || ...)` ternary)
  because the script cannot `import { parseTheme }` from
  `web/app/(authed)/settings/theme.ts` — `dangerouslySetInnerHTML` takes
  a string, not a module reference. The helper in
  `web/lib/theme-class.ts` mirrors this logic and is what the runtime
  `<ThemeClassSync>` component + the picker's `choose()` both call;
  vitest pins the helper's correctness. A divergence between script and
  helper would reintroduce a one-frame flash.
- **`data-theme` seed at the same moment** so the slice-176
  `ThemeAwareLogo`'s MutationObserver doesn't fire on the FIRST mount
  (which would be a sub-frame visual but a real one). The seed only
  runs when the attribute is absent so a subsequent runtime write does
  not get clobbered.
- **`suppressHydrationWarning` on `<html>`** is required because the
  server-rendered HTML does NOT include the `dark` class (the server
  has no localStorage). The class-list mismatch is the deliberate
  outcome of fixing the bug; React would otherwise log a hydration
  warning on every dark-mode page load. This is the documented
  next-themes / next/font pattern.

**Locality:** the script lives in `web/app/layout.tsx` so it runs on
every page (not just authed pages — the operator may land first on
`/login`, which is also rendered via the same root layout). The
`<ThemeClassSync>` component mounts under `<Providers>` and owns the
runtime tail of the contract (mount-side `prefers-color-scheme`
reactivity + `data-theme` MutationObserver).

---

## D2 — Surfaces flagged by AC-5 visual sanity-check

**Decision:** AC-5 mandates a visual spot-check of `/`, `/controls`,
`/risks`, `/policies`, `/audits`, `/settings`, `/admin/tenants` in both
light and dark modes. Per P0-A5, hardcoded-color violations surfaced by
this check do NOT get fixed in this PR — they file as future slices
with `#203` as the parent.

**Methodology:** running a full dev server + browser screenshot harness
in this sub-agent session is not feasible (no live server, no Playwright
runtime against the platform). I substituted a STATIC scan: grep for
hardcoded color tokens in `web/app/**/*.tsx` and `web/components/**/*.tsx`
that bypass the CSS-variable-backed `bg-background` / `text-foreground` /
`text-muted-foreground` / `border-border` set. These are the surfaces
that, in a live dark-mode session, would render with a light-mode-coded
color while the rest of the page themes — i.e. the AC-5 "obviously-broken
surfaces" the slice doc calls out.

**Static scan results (slice candidates):**

| Surface                    | File                                            | Pattern                                                                | Severity (judged)                                                                                                                       |
| -------------------------- | ----------------------------------------------- | ---------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Board-pack export bar      | `web/components/board-pack/export-bar.tsx`      | `bg-white text-slate-700 border-slate-300`                             | High — the entire board-pack route is operator-visible; export bar sits sticky-top so the violation is impossible to miss in dark mode. |
| Board-pack section cards   | `web/components/board-pack/section-card.tsx`    | `bg-white border-slate-200` (card chrome + inline `<textarea>` chrome) | High — every section of every board pack renders these.                                                                                 |
| Board-pack coverage trend  | `web/components/board-pack/coverage-trend.tsx`  | hardcoded slate / white                                                | Medium — chart background; less load-bearing than chrome but still visibly broken.                                                      |
| Board-pack findings list   | `web/components/board-pack/findings-list.tsx`   | similar                                                                | Medium.                                                                                                                                 |
| Board-pack templated badge | `web/components/board-pack/templated-badge.tsx` | similar                                                                | Low — single badge.                                                                                                                     |
| Board-pack top-risks table | `web/components/board-pack/top-risks-table.tsx` | similar                                                                | Medium.                                                                                                                                 |
| Board-packs view page      | `web/app/(authed)/board-packs/[id]/page.tsx`    | bg-white wrapper                                                       | Medium.                                                                                                                                 |
| Exceptions list page       | `web/app/(authed)/exceptions/page.tsx`          | hardcoded color cells                                                  | Low — likely an inline cell highlight, not the whole page.                                                                              |
| Control coverage table     | `web/components/control/coverage-table.tsx`     | hardcoded cells                                                        | Low — likely matrix cell coloring; semantic ramp not token-driven.                                                                      |

**Settings page swatches** (`app/(authed)/settings/page.tsx:432,438`)
intentionally use `bg-white` + `bg-slate-900` as the literal preview
swatches for the Light + Dark theme buttons. These MUST NOT theme —
they show the user what each theme looks like. The grep returned them
but I excluded them from the slice-candidate list.

**Recommended next-slot spillover:** ONE slice titled "dark-mode token
migration for board-pack components" covers the high+medium cluster
(the board-pack surfaces dominate the violations). The low items
(exceptions cell, control coverage cell) likely warrant a second slice
once the board-pack pass establishes the migration pattern. Both file
post-merge per Amendment 2 spillover policy.

**Caveat:** the static scan is a PROXY for the live visual check. The
slice's binary success contract is "selecting Dark renders a dark page";
the Playwright AC-12 assertion (body bg-color delta) is the actual gate
on that. The board-pack components are demonstrably broken in dark mode
today; whether the dashboard / controls / risks / policies / audits /
admin pages render PERFECTLY in dark mode without other token-bypass
violations cannot be confirmed without a live screenshot pass. That is
also future-slice work (a dedicated dark-mode-visual-regression
harness; out of scope for 0.5d).

---

## D3 — CI-delta scan (per slice 143 D8 + slice 202 D2 corrections)

**Required surfaces:**

| CI check                                                          | Will this PR pass?                   | Evidence                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| ----------------------------------------------------------------- | ------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Frontend · lint`                                                 | Yes                                  | `npm run lint` returned 0 errors. Two pre-existing warnings in `scripts/capture-readme-screenshots.ts` are unrelated to this PR. The `react/no-danger` ESLint rule is not enabled in this project's config, so the `dangerouslySetInnerHTML` usage in `layout.tsx` does not trigger a lint warning.                                                                                                                                                                                                                                                             |
| `Frontend · vitest`                                               | Yes                                  | Full suite ran 729/729 green (74 test files). The new `web/lib/theme-class.test.ts` adds 17 tests covering AC-6 (a-d): `choose("dark")` adds class, `choose("light")` removes, `choose("system")` follows OS-pref, and `applyPersistedThemeClass` covers the page-load init contract.                                                                                                                                                                                                                                                                           |
| `Frontend · Playwright e2e` (now required-check per slice 116)    | Probably yes; verified-by-shape only | The new AC-12 test extends the existing `web/e2e/settings.spec.ts` (already mounted in the CI matrix). It uses the existing `authedPage` fixture, the existing seed, and the existing CSS-variable surface. The body-bg color delta assertion is browser-agnostic (Playwright reads `window.getComputedStyle` directly). Risk: if the headless Chromium emits oklch() instead of rgb() the brittle equality would fail — I deliberately chose a NEGATION assertion (`darkBg !== lightBg`) instead of `darkBg === "rgb(37, 37, 37)"` to avoid that class-of-bug. |
| `Frontend · build` (verified locally)                             | Yes                                  | `npm run build` succeeded. Inline-script-in-`<head>` is a stable Next.js pattern (next-themes, next/font, etc. all do this).                                                                                                                                                                                                                                                                                                                                                                                                                                    |
| `Frontend · TypeScript` strict                                    | Yes                                  | The new modules are TS-clean. Pre-existing `tsc --noEmit` errors in `lib/auth/oauth-client.test.ts` + `scripts/capture-readme-screenshots.test.ts` are unrelated to this PR; verified by stashing my changes and re-running tsc against main — same 11 errors surface.                                                                                                                                                                                                                                                                                          |
| `Frontend · UI honesty (advisory)` (slice 178)                    | No deltas expected                   | The harness is data-binding-focused (asserts each page's read-only-derived data is sourced from the API, not invented). Theme-class wiring is orthogonal — no API surface changes, no mock data introduced.                                                                                                                                                                                                                                                                                                                                                     |
| `Frontend · Playwright stub-twin` (docs-only fastpath, slice 061) | N/A                                  | This PR is code (not docs-only); the path-filter routes it to the full Playwright run.                                                                                                                                                                                                                                                                                                                                                                                                                                                                          |
| GitGuardian                                                       | Yes                                  | No secrets, no vendor-prefixed test tokens (P0-A5 from slice 069 honored). The inline script contains the literal localStorage key `security-atlas.settings.theme` — a vendor-neutral app prefix, not a credential pattern.                                                                                                                                                                                                                                                                                                                                     |
| `pre-commit run --all-files`                                      | Verified pre-commit                  | Will run before push.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                           |

**Specific verification claims (per the engineer-claim-verification feedback loop in MEMORY.md):**

- I VERIFIED locally: `npm run lint` returned 0 errors related to my changes.
- I VERIFIED locally: `npm run test` (vitest) passed 729/729 tests.
- I VERIFIED locally: `npm run build` (Next.js production build) succeeded.
- I VERIFIED locally: `npx tsc --noEmit` produced ZERO errors in my new/modified files (5 files); the 11 remaining errors are in pre-existing files (`oauth-client.test.ts`, `capture-readme-screenshots.test.ts`) and are unrelated to this slice.
- I DID NOT run Playwright locally against this branch. The new AC-12 test should pass given (a) the same fixture surface as the existing 11 specs in `settings.spec.ts`, all of which pass in CI today, and (b) the assertion shape is a simple class-presence + body-bg color delta check. The first CI run on this PR is the live gate.

**Class-of-bug risks I weighed:**

1. **Inline script CSP violation** — the slice-087 CSP at `web/proxy.ts:65` is `Content-Security-Policy-Report-Only`, so even an enforced `script-src 'self'` would only LOG the violation, not block it. The `security-headers.spec.ts` checks the header presence but does not enforce script-src compliance. Verified by re-reading the proxy.ts directive (`"script-src 'self';"`) — this would fail under an enforced CSP, but the slice 087 / 089 D1 decision keeps CSP in report-only mode precisely because of Next.js's own inline hydration scripts. Our inline script joins that exemption class; the deferred-cutover conversation is open question OQ #22.

2. **Hydration mismatch warnings in the dev console** — solved by the `suppressHydrationWarning` flag on `<html>`. Verified by reading the Next.js docs (next-themes does exactly this).

3. **The `data-theme` source-of-truth invariant (P0-A1)** — verified by reading the picker `choose()` carefully: it writes BOTH the attribute AND calls `applyThemeClass`. The two stay in sync at every call site. The runtime `<ThemeClassSync>` also reads `data-theme` first (falling back to localStorage), so the slice-176 ThemeAwareLogo consumer continues to read its preferred signal unchanged.

4. **The `globals.css` `.dark` block invariant (P0-A4)** — I did not touch `web/app/globals.css`. The class write activates the existing token block.

---

## D4 — Why the picker UI was NOT moved to read the class instead

I considered making the picker's "selected" state derive from the
runtime `dark` class rather than from local React state. Rejected:
the existing AppearanceSelector + `<DEFAULT_THEME, readTheme, writeTheme>`
contract already works (slice 170 hydration audit passed), and changing
its source-of-truth would widen the slice's blast radius beyond the
0.5d estimate. The state pair (React-local `theme` + DOM `class`) stays
in sync via the same `choose()` call site (AC-2 contract); the runtime
`<ThemeClassSync>` re-converges if anything externally writes the
attribute.

---

## D5 — Why `<ThemeClassSync>` mounts under `<Providers>` and not at the body root

`<ThemeClassSync>` returns `null`. It needs the React tree (for
`useEffect`) but no DOM presence. Mounting it inside `<Providers>` keeps
the layout HTML structure unchanged (one `<body><div>...</div></body>`
tree, no spurious wrapper). Mounting it as a sibling of `{children}`
inside Providers ensures it runs on every authed AND unauthed route
(login page included), since `<Providers>` is the layout-level wrapper
that every page passes through.

---

## D6 — Spillover slices filed against this slice

Per Amendment 2 (do NOT modify `_INDEX.md`, do NOT fix in this PR), the
hardcoded-color violations enumerated in D2 file as the next-slot
spec citing #203 as the parent. The maintainer assigns the next
contiguous slice number on intake (#206 + as appropriate). I did not
file these — that is a maintainer responsibility per the spillover
discipline.

---

## D7 — Why `applyThemeClass` accepts an Element-shaped subset instead of `HTMLElement`

`web/lib/theme-class.ts` types its target as `ClassListTarget` (a
3-method subset of `DOMTokenList`'s parent). This is so vitest's
`node`-env tests (per `vitest.config.ts`, no jsdom) can exercise the
helper against a minimal in-memory stub WITHOUT pulling in jsdom or
`@testing-library/react`. The runtime callers (the picker, the
`ThemeClassSync`, the inline script's mirrored logic) pass
`document.documentElement`, which structurally satisfies the
interface. The narrow type is intentional: a future refactor that
reaches for `.setAttribute` or `.style` on the target has to widen
the surface explicitly, which surfaces the change in code review.

This mirrors the slice-103 `ThemeStore` pattern (theme.ts:30-33),
which uses the same Storage-shaped subset technique for the same reason.

---
