# 170 — Settings theme picker doesn't restore from localStorage after reload (slice 168 spillover)

**Cluster:** Quality
**Estimate:** 0.25d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

**WHY.** During slice 168's AC-2 diagnosis (settings spec "theme picker persists choice across reload"), the engineer found a production hydration bug in `web/app/(authed)/settings/page.tsx`'s `AppearanceSelector` (lines 475-535).

The selector uses `useState` with a lazy initializer to read the persisted theme from `window.localStorage`:

```tsx
const [theme, setTheme] = useState<Theme>(() => {
  if (typeof window === "undefined") return DEFAULT_THEME;
  return readTheme(window.localStorage);
});
```

That initializer is SSR-guarded — and that's the bug. Next.js renders client components on the server during the initial prerender pass, where `typeof window === "undefined"`. The initializer returns `DEFAULT_THEME` ("system"). The server-rendered HTML ships with `data-selected="true"` on the `system` button. When the page hydrates on the client, React reuses the server-rendered state — **the lazy initializer is NOT re-run on the client**. So `window.localStorage` is never consulted on a fresh page load.

The user's selection IS persisted (`writeTheme(window.localStorage, "dark")` runs on click), but on the NEXT visit/reload the picker resets to "system" because that's what the SSR pass froze into the hydration state.

**Evidence (from slice 168's CI artifact `quality/166-allowed-kinds-null-safe-deref` run 26106619396, job 76772362534):**

- After AC-2 clicks `settings-theme-option-dark` and reloads, the assertion `toHaveAttribute("data-selected", "true")` on the dark button times out with received value `"false"`.
- The DOM snapshot from the AC-3 trace (which starts with a fresh `page.goto("/settings")`) shows `radio "System Follow OS preference" [checked]` — confirming the page always boots with "system" regardless of localStorage.
- The CardDescription on the Appearance section literally says "Theme preference is stored in your browser" — that contract is broken for any user who reloads.

**WHAT.** Add a `useEffect` (or a `dynamic({ ssr: false })` wrapper, OR the `useSyncExternalStore` pattern — engineer chooses) to sync the theme state from `localStorage` on client mount, so the picker reflects the persisted value on every visit, not just the first one within a single navigation.

**SCOPE DISCIPLINE — what's out:**

- Does NOT add server-side theme persistence — that's a separate v2 slice (cross-device sync via PATCH /v1/me).
- Does NOT touch the localStorage write path — `writeTheme` is correct.
- Does NOT touch the testid scheme on the theme buttons — `data-selected`, `data-testid="settings-theme-option-*"`, and the radiogroup ARIA are all correct.
- Does NOT change `theme.ts`'s `readTheme` / `writeTheme` helpers — pure persistence module, not the bug site.
- Does NOT add dark-mode CSS tokens — the existing "Dark-mode stylesheet pending" banner says so explicitly.

## Threat model

Pure client-side rendering bug. No new attack surface introduced or addressed.

**S — Spoofing.** None.

**T — Tampering.** The bug means user preferences silently revert on reload. Not security-relevant, but UX-corroding. Fix restores the contract.

**R — Repudiation.** None.

**I — Information disclosure.** None.

**D — Denial of service.** None.

**E — Elevation of privilege.** None.

**Verdict.** none.

## Acceptance criteria

- **AC-1.** `AppearanceSelector` reads localStorage on client mount (not just on SSR prerender). After `page.goto("/settings")` → click dark → `page.reload()`, the dark button MUST have `data-selected="true"`.
- **AC-2.** The fix preserves the SSR-render contract: no hydration mismatch warning in the browser console. (The most common safe pattern: `useState(DEFAULT_THEME)` + `useEffect(() => setTheme(readTheme(window.localStorage)), [])` — initial render matches server, then a single post-mount state flip.)
- **AC-3.** Settings spec AC-2 (`web/e2e/settings.spec.ts:52`) flips from red to green in CI Playwright.
- **AC-4.** A vitest regression covers the persistence contract: render the selector with a Storage shim containing `"dark"`, simulate mount, assert `data-selected="true"` on dark within one tick.
- **AC-5.** No regression in the other 10 settings ACs (esp. AC-1 / AC-3 / AC-4 — slice 168's surface).

## Constitutional invariants honored

- **Slice 168's contract:** AC-2 closes once this slice ships (slice 168 explicitly punts AC-2 to this spillover per the per-AC classification policy).
- **Slice 103's contract:** localStorage remains the v1 persistence — no server endpoint added.
- **shadcn / Next.js App Router idioms:** the fix uses standard React hook patterns; no custom render shims.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — Next.js App Router + React 19; SSR-prerender + client hydration is the default model.

## Dependencies

- **#168** (settings spec remaining 4 ACs) — slice 168 lands the AC-1 / AC-3 / AC-4 fixes; AC-2 routes here.

## Anti-criteria (P0 — block merge)

- **P0-A1.** Does NOT add server-side persistence (PATCH /v1/me with `theme` field) — that's a separate v2 slice.
- **P0-A2.** Does NOT change `theme.ts`'s pure persistence helpers (`readTheme` / `writeTheme` / `parseTheme`). They are correct; the bug is in the consumer.
- **P0-A3.** Does NOT modify the testid scheme on the theme buttons. Slice 168's AC-2 spec body MUST pass against the current selectors after this fix.
- **P0-A4.** Does NOT change the `Theme` type or `DEFAULT_THEME` value.
- **P0-A5.** Does NOT introduce a flash-of-unstyled-content > 1 frame. If `useEffect` causes a visible flicker between "system" (SSR) and "dark" (client), use a CSS-driven approach (`data-theme` on `<html>` from a `<script>` in `<head>`, or a server-cookie-based pre-paint) instead.
- **P0-A6.** Does NOT promote `Frontend · Playwright e2e` to required-checks — slice 116 owns that.

## Skill mix (3-5)

1. **Engineer** — primary; React hook pattern selection + implementation
2. **QATester** — reproduce + verify AC-3 in CI
3. **Designer** (not needed) — no UI work, behaviour fix only

## Notes for the implementing agent

**Three viable fix patterns (engineer picks):**

1. **`useEffect` post-mount sync.** Simplest. One state flip on mount. Risk: a single-frame flash from "system" to "dark" on slow networks. Mitigation: render the entire `AppearanceSelector` as `null` until mounted (loses SSR'd content, but acceptable for a tiny UI). Or accept the flicker — the section is below the fold.
2. **`useSyncExternalStore`.** React-canonical for external (non-React) state sources. The subscribe function is a no-op (localStorage doesn't fire events on same-tab writes; cross-tab would need a `storage` event listener but isn't required). Cleanest if you want the picker to react to mid-session changes.
3. **`dynamic({ ssr: false })` wrap.** Defers `AppearanceSelector` to client-only rendering. Removes the SSR'd radio group from the initial HTML. Simplest by code volume; trade-off is a brief skeleton-shaped hole on first paint.

Recommendation: pattern (1) with `useEffect`. Smallest diff, most readable, no Suspense boundary needed. The 1-frame flicker is below-the-fold acceptable.

**Test plan:** the regression is a single vitest spec — pass a Storage shim with `dark`, render via `@testing-library/react`, `act()` to flush effects, assert `data-selected="true"` on the dark button. Pair with the CI Playwright run as the live-binding gate.

**Provenance.** Surfaced 2026-05-19 during slice 168's AC-2 diagnosis. The slice 168 engineer triaged AC-2 to "production bug" per P0-A1, filed this spillover per Amendment 2 of the slice-development workflow, and stopped pursuing AC-2 inside slice 168's surface (P0-A4 forbids production code changes beyond a single testid addition).
