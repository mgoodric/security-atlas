# 203 — Dark-mode stylesheet wiring (theme selection actually themes the UI)

**Cluster:** Frontend
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** spillover surfaced 2026-05-22 from user report on a post-release deploy. Slice 170 explicitly deferred this work (comment in `web/app/(authed)/settings/page.tsx:710-714`: "The v1 build does not ship dark-mode stylesheet tokens... the persistence is the contract.").

## Narrative

When an operator picks "Dark" in `/settings`, two things happen today (post slices 170 + 176):

1. `localStorage["security-atlas.settings.theme"]` is written to `"dark"`.
2. `<html data-theme="dark">` attribute is set.
3. **Slice 176's `ThemeAwareLogo` component reads `data-theme` and swaps from `/logo-light.svg` to `/logo-dark.svg`** (light-ink variant, designed for dark backgrounds).

What does NOT happen:

4. `globals.css`'s dark-mode CSS variables (defined in `.dark { --background: ...; --foreground: ...; ... }` at line 86+) **never apply**, because nothing writes `class="dark"` to `<html>` (or otherwise activates the `.dark` selector).
5. Tailwind's `dark:` variant is `&:is(.dark *)` (configured at `globals.css:5` via `@custom-variant dark`). The selector never matches.

The result: selecting "Dark" swaps the logo (light-ink) but leaves the page styles in light mode (white background, dark text). The light-ink logo over the still-white page is **invisible** — matching the user-reported failure on the most recent release.

The same bug also explains "UI is almost entirely useless in dark mode": picking dark doesn't actually theme the UI; it only changes the logo and (now-invisibly) the logo's coloring.

This slice closes the gap: makes the operator's theme selection ACTUALLY THEME the UI.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                                                      | Mitigation                                                                                                                                                                                                                                           |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **T** Tampering       | None — purely cosmetic; no data path touched.                                                                                                                                                                               | n/a                                                                                                                                                                                                                                                  |
| **I** Info disclosure | If the dark theme is selected and any UI element (badge, modal, hover-card) is hardcoded to a light-mode color value (not a CSS variable), that element's text could become illegible — disclosure of state without intent. | AC-5: visual-regression check against all known top-level pages (`/`, `/controls`, `/risks`, `/policies`, `/audits`, `/settings`, `/admin`) in both modes. Document remaining hardcoded color leaks in decisions log as a follow-on slice candidate. |
| **D** DoS             | Adding the class on every selection adds a single DOM mutation. Negligible. The MutationObserver in `ThemeAwareLogo` will fire on the `class` change (already observed). No infinite loop possible.                         | n/a                                                                                                                                                                                                                                                  |
| **E** EoP             | None.                                                                                                                                                                                                                       | n/a                                                                                                                                                                                                                                                  |

## Acceptance criteria

- [ ] AC-1: When the operator selects "Dark" in `/settings`, `<html>` receives `class="dark"` (in addition to the existing `data-theme="dark"` attribute that slice 170 writes). When they select "Light", `class="dark"` is removed. When "System", the class is added iff `prefers-color-scheme: dark`.
- [ ] AC-2: The class change happens at the same `choose()` call site in `web/app/(authed)/settings/page.tsx` that already writes the attribute (around line 710), so the two stay in sync.
- [ ] AC-3: On page load (initial mount), the persisted theme is applied to `<html>` BEFORE first paint to avoid a flash-of-light-mode. Use either: (a) a small inline `<script>` in `web/app/layout.tsx` that reads `localStorage` and writes the class synchronously, or (b) the `next-themes`-style early-script pattern. Pick the approach in the decisions log (D1).
- [ ] AC-4: `prefers-color-scheme` reactivity — when an operator with `theme=system` toggles their OS dark/light setting, the page re-themes without reload. The existing `ThemeAwareLogo`'s `matchMedia` listener confirms this works; mirror its discipline for the html-class application (likely a single shared util module is the cleanest factoring).
- [ ] AC-5: Visual sanity-check: take screenshots of `/` (dashboard), `/controls`, `/risks`, `/policies`, `/audits`, `/settings`, `/admin/tenants` in BOTH light and dark modes. Verify each renders correctly. Document any obviously-broken surfaces (hardcoded light colors that don't switch) in the decisions log as future slice candidates — do not fix in this PR.
- [ ] AC-6: vitest regression covering: (a) `choose("dark")` adds `.dark` class and writes the attribute; (b) `choose("light")` removes the class; (c) `choose("system")` adds/removes based on a matchMedia mock; (d) page-load init script reads localStorage + applies class before paint.
- [ ] AC-7: Playwright e2e (or extend existing `settings.spec.ts`): pick dark, navigate to `/`, assert computed `<body>` `background-color` matches the dark-mode token value (or alternative: assert a specific element's class includes a `bg-` token that resolves dark).
- [ ] AC-8: CHANGELOG entry under "Fixed" describing the visible-to-operator behavior change.

## Constitutional invariants honored

This slice is frontend-only (`web/`) — does not touch any backend invariant (RLS, tenancy, evidence ledger, OSCAL, OPA, audit-log). It corrects a UX defect introduced by slices 170 + 176 not agreeing on a contract.

## Canvas references

None — purely tactical wiring; not a canvas decision.

## Dependencies

- **#170** (theme picker hydration) — merged. This slice extends `choose()` with the class write.
- **#176** (logo theme coupling) — merged. The logo already reads `data-theme`; this slice does NOT touch the logo path. The page-class wiring is the new layer.

## Anti-criteria (P0)

- **P0-A1**: DOES NOT change the source-of-truth theme attribute from `data-theme` to `class="dark"`. Both stay written; the logo + future consumers can read whichever they prefer.
- **P0-A2**: DOES NOT touch the backend or any API. Frontend-only fix.
- **P0-A3**: DOES NOT introduce `next-themes` or other dependency unless the decisions log (D1) explicitly justifies it; prefer the smallest possible inline-script approach.
- **P0-A4**: DOES NOT modify `globals.css` token values (the dark-mode CSS variables already exist; this slice only activates them).
- **P0-A5**: DOES NOT fix hardcoded-color violations surfaced during AC-5 spot-check. Those file as future slices.
- **P0-A6**: DOES NOT remove the FOUC-prevention inline script if added (P0-A3 may add one). Reasonable bundle-size impact only.

## Skill mix

- web/app/layout.tsx editor (inline early-paint script)
- web/app/(authed)/settings/page.tsx editor (extend `choose()`)
- vitest test author
- Playwright spec author (or extend existing settings.spec.ts)

## Notes for the implementing agent

The trap to avoid: writing the class swap in a React `useEffect` only. That gives a one-frame flash of light mode on every page load for users whose persisted preference is dark. The early inline script in `web/app/layout.tsx` runs before React hydrates and prevents the FOUC. Slice 170 explicitly accepted a one-frame flicker for the picker UI (below the fold); this slice should NOT accept that for the whole page (the entire above-the-fold UI flashes).

Pattern reference: `next-themes` does this with a `<script dangerouslySetInnerHTML>` block emitted into `<head>`. You don't need the library — the 8-line inline script is enough. Document the exact script in the decisions log so future maintainers know not to "tidy it up" into a hook.

Provenance: filed 2026-05-22 via continuous-batch spillover after user reported the visible failure on a deployed release. Slice 170 explicitly chose to defer this work (commented in source); this slice closes that deferral.
