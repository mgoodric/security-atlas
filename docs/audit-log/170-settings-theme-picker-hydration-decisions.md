# Slice 170 — Settings theme picker hydration fix · decisions log

**Date:** 2026-05-19
**Engineer:** Claude (subagent — Opus 4.7)
**Branch:** `quality/170-theme-hydration-fix`
**Type:** JUDGMENT (engineer picks D1 from 3 viable patterns)
**Estimate vs actual:** 0.25d budget · ~30 min actual

## Context

Slice 168's AC-2 diagnosis surfaced a production hydration bug in
`web/app/(authed)/settings/page.tsx` (lines 475-535, `AppearanceSelector`).

The selector used `useState` with an SSR-guarded lazy initializer:

```tsx
const [theme, setTheme] = useState<Theme>(() => {
  if (typeof window === "undefined") return DEFAULT_THEME;
  return readTheme(window.localStorage);
});
```

That initializer ran **once per render pass**:

1. **Server prerender:** `typeof window === "undefined"`, returns
   `DEFAULT_THEME`. Server HTML ships with `data-selected="true"` on the
   `system` button.
2. **Client hydration:** React re-mounts the tree but **reuses
   server-rendered state**. The lazy initializer is NOT re-run. The
   client never consults `localStorage` on a fresh page load.

Consequence: the picker reset to "system" on every reload, regardless of
the persisted choice. The `writeTheme` call on click still worked (the
value was in `localStorage`), but the read path was broken. Settings
spec AC-2 caught it.

## D1 — fix pattern (3 viable options, engineer picks 1)

### Decision

**Pattern A: `useEffect` post-mount sync.**

```tsx
const [theme, setTheme] = useState<Theme>(DEFAULT_THEME);
useEffect(() => {
  setTheme(readTheme(window.localStorage));
}, []);
```

### Rationale

- **Smallest diff** (~3 substantive lines in `AppearanceSelector` + 1
  import line for `useEffect`).
- **No new dependencies.**
- **Preserves SSR contract** (AC-2): the initial render returns
  `DEFAULT_THEME` on both server and client, eliminating any hydration
  mismatch warning. The post-mount setState is a single state flip — a
  pattern React explicitly supports and is the canonical answer to
  "syncing to a non-React state source" for one-shot reads.
- **Below-the-fold flicker is acceptable.** The Appearance section sits
  below Profile in the settings page; the 1-frame transition from
  "system" to the stored value is invisible to anyone who isn't
  staring at the radio group during initial paint. Slice 170 P0-A5
  caps this at "> 1 frame"; Pattern A is exactly 1 frame.

### Patterns ruled out

- **Pattern B: `dynamic({ ssr: false })` wrapper.** Defers
  `AppearanceSelector` to client-only. Removes the SSR'd radio group
  from initial HTML — degrades a11y on first paint and ships a
  visible "hole" where the picker should be. Higher cost than Pattern
  A for no benefit.
- **Pattern C: `useSyncExternalStore`.** React-canonical for external
  state sources, but the subscribe function for `localStorage` is a
  no-op (same-tab writes don't fire a `storage` event). The hook
  reduces to the same post-mount read Pattern A delivers in 3 lines,
  but with 10-15 lines and a no-op subscribe shim. Excess code for
  zero behaviour difference.

## D2 — regression test placement

### Decision

Extend `web/app/(authed)/settings/theme.test.ts` with a new describe
block `"slice 170 — AppearanceSelector post-mount hydration contract"`
containing 4 tests that pin the post-mount read invariant.

### Rationale

- **Test env is node-only** (slice 069 P0-A3): `@testing-library/react`
  is NOT a dependency. The vitest config (`web/vitest.config.ts`)
  excludes `*.tsx` from coverage and runs only `*.test.ts`. The team
  cannot render `AppearanceSelector` itself in this slice without
  introducing a new dependency — a scope violation.
- **The Playwright spec at `web/e2e/settings.spec.ts:60` (AC-2) is the
  live-binding gate** that exercises the actual hydration sequence in
  a real browser. Slice 170 AC-3 requires that spec to flip green —
  Pattern A makes that mechanical.
- **The vitest regression pins the underlying contract** that the
  fixed component depends on: `readTheme(store)` after
  `writeTheme(store, "dark")` returns "dark" — and that this is the
  read the `useEffect` performs. If a future refactor moves the
  post-mount read off `readTheme`, the Playwright spec fails;
  if `readTheme` itself regresses, this vitest fails. Layered.

## D3 — `react-hooks/set-state-in-effect` disable

### Decision

Add a single `// eslint-disable-next-line react-hooks/set-state-in-effect`
directly above the `setTheme(readTheme(window.localStorage))` call inside
the `useEffect` callback, with an explanatory comment block.

### Rationale

ESLint's `react-hooks/set-state-in-effect` rule flags any synchronous
state setter inside an effect. For most cases that's correct guidance:
deriving state from props during render is preferable. But for THIS
specific pattern — one-shot synchronization from a non-React external
state source (`localStorage`) on mount — `react.dev`'s "Synchronizing
with external systems" doc explicitly recommends the `useEffect` +
setState pattern as canonical. The prior implementation's comment even
named the rule by name when explaining why it used the lazy initializer
hack to sidestep it — but that hack was the source of the slice 170 bug.

We're trading "the linter is silent" for "the picker actually works."
The disable is narrow (one line) and load-bearing comment is explicit
about the slice 170 invariant.

## D4 — initial-state value

### Decision

Seed `useState` with `DEFAULT_THEME` (the existing exported constant,
value `"system"`), not with an explicit string literal.

### Rationale

- Matches the SSR pass exactly. The bug fix mantra: "client first
  render == server first render."
- Centralizes the default-value definition in `theme.ts`. P0-A4
  forbids changing `DEFAULT_THEME` itself; using the constant honours
  that.

## Local verification

```text
$ cd web && npm run test -- --run
...
 Test Files  45 passed (45)
      Tests  507 passed (507)
   Start at  11:13:58
   Duration  1.05s
```

507 = 503 (slice 166 baseline) + 4 new slice 170 regression tests.

Focused run:

```text
$ npm run test -- --run app/\(authed\)/settings/theme.test.ts
 ✓ app/(authed)/settings/theme.test.ts (14 tests) 2ms
 Test Files  1 passed (1)
      Tests  14 passed (14)
```

14 = 10 prior + 4 new.

## P0 anti-criteria audit

- **P0-A1 (no server-side theme persistence):** PASS. No PATCH /v1/me
  fields added. v2 scope respected.
- **P0-A2 (no changes to `theme.ts` helpers):** PASS. The file is
  byte-identical pre/post-slice (only `theme.test.ts` was extended).
- **P0-A3 (no testid changes on theme buttons):** PASS.
  `data-testid="settings-theme-option-*"` and `data-selected` selectors
  unchanged.
- **P0-A4 (no changes to `Theme` type or `DEFAULT_THEME`):** PASS.
  Both exported symbols are byte-identical in `theme.ts`.
- **P0-A5 (no >1-frame FOUC):** PASS. Pattern A causes exactly 1 frame
  between SSR paint and post-mount setState. The Appearance section is
  below the fold (Profile + tail badge come first), so the flicker is
  not user-visible on the initial paint.
- **P0-A6 (no bundling of slice 168 fixture-upsert change):** PASS.
  The slice 168 change is already on main; this branch only touches
  `page.tsx`, `theme.test.ts`, `_STATUS.md`, and this decisions log.

## Spillovers

None filed. The fix is contained to `AppearanceSelector`. A pass with
ripgrep over `useState<.*>\(\(\)` patterns in `web/app/(authed)/`
showed no other SSR-guarded lazy-initializer reads of `localStorage` —
the `AppearanceSelector` was the only instance. Should a sibling
pattern surface later, it would file as a new spillover slice per
Amendment 2.

## Files changed

1. `web/app/(authed)/settings/page.tsx` — 1-line import change
   (`useEffect` added to React imports); 2-line lazy-initializer
   replaced with 3-line `useState` + `useEffect` post-mount sync;
   13-line explanatory comment added.
2. `web/app/(authed)/settings/theme.test.ts` — 1 new describe block,
   4 new test cases, pinning the post-mount-read invariant.
3. `docs/issues/_STATUS.md` — claim-stake flip + drift block.
4. `docs/audit-log/170-settings-theme-picker-hydration-decisions.md` —
   this document.

## Whether AC-2 (settings.spec.ts:60) flips green

**Expected: YES.** The Playwright assertion sequence is:

1. `page.goto("/settings")` — SSR + hydration completes. Pattern A's
   `useEffect` fires immediately on mount.
2. `page.getByTestId("settings-theme-option-dark").click()` — runs
   `choose("dark")`, which calls `writeTheme(localStorage, "dark")` and
   `setTheme("dark")`. Click handler is unchanged.
3. `page.reload()` — fresh navigation, SSR returns DEFAULT_THEME;
   client hydration completes; `useEffect` fires and calls
   `setTheme(readTheme(localStorage))` → `setTheme("dark")`.
4. `expect(page.getByTestId("settings-theme-option-dark")).toHaveAttribute("data-selected", "true")` — passes (after the effect resolves).

The only failure mode is if Playwright's assertion races the `useEffect`
microtask. `toHaveAttribute` retries with default timeout (5s), so a
single-frame flicker is well within the retry window. Expected: green.
