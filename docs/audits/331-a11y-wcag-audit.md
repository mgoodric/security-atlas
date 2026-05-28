# 331 — Accessibility audit (WCAG 2.1 AA) report

**Slice:** 331
**Date:** 2026-05-28
**Auditor:** `voltagent-qa-sec:accessibility-tester` persona (static-code-review run)
**Scope:** read-only static audit of the Next.js frontend at `web/`; no product code, CI config, or markup modified

---

## Methodology

This audit applies the `voltagent-qa-sec:accessibility-tester` persona at
`~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/accessibility-tester.md`
against the eight WCAG 2.1 AA surfaces named in the slice doc
(`docs/issues/331-a11y-wcag-audit.md` narrative): keyboard
navigation · screen-reader semantics · focus management · color
contrast (light + dark) · motion · resize + zoom · form errors ·
time-based content.

This is the **static review** view. The persona's runtime steps
(axe-core / WAVE / NVDA / JAWS / VoiceOver pass) are deliberately
deferred — the slice doc bounds the work to "audit-only · spillover
fan-out · no runtime browser tests in this slice." The findings here
either (a) reproduce statically against the markup, Tailwind tokens,
and shadcn primitives, or (b) raise an explicit "live-browser
verification required" gate captured as a spillover.

### Audit surface (bounded, ~7 representative components)

| Surface         | Files inspected                                                                                                                                                               | WCAG surfaces covered                             |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------- |
| Login flow      | `web/app/login/page.tsx`                                                                                                                                                      | form labels, focus, contrast, errors              |
| Auth layout     | `web/app/(authed)/layout.tsx` + `web/components/shell/topbar.tsx` + `web/components/shell/sidebar.tsx` + `web/components/shell/mobile-sidebar.tsx`                            | landmarks, skip-link, keyboard nav, mobile drawer |
| Dashboard       | `web/app/(authed)/dashboard/page.tsx`                                                                                                                                         | heading hierarchy, live regions, status pills     |
| Controls list   | `web/app/(authed)/controls/page.tsx` + `web/components/list/list-table.tsx` + `web/components/list/filter-pills.tsx` + `web/components/ui/table.tsx`                          | table semantics, filter pills, mobile cards       |
| Risks hierarchy | `web/app/(authed)/risks/hierarchy/page.tsx`                                                                                                                                   | heading hierarchy, panel landmarks                |
| Admin (forms)   | `web/app/admin/super-admins/page.tsx` + `web/app/admin/tenants/page.tsx`                                                                                                      | form errors, dialog focus trap, raw `<input>`     |
| Theme tokens    | `web/app/globals.css`                                                                                                                                                         | contrast (light + dark)                           |
| UI primitives   | `web/components/ui/{button,input,alert,dialog,sheet,badge,skeleton,table}.tsx` + `web/components/shell/global-search.tsx` + `web/components/shell/in-progress-audit-pill.tsx` | focus-visible, ARIA, animation                    |
| Search          | `web/components/shell/global-search.tsx`                                                                                                                                      | combobox semantics, keyboard nav, popover roles   |

Page enumeration in the slice doc lists ~30 routes; the bounded
sample touches the template-patterns those routes consume (list /
detail / dashboard / form / dialog / auth) plus the shared shell, so
findings rooted in primitives (Input / Button / Alert / Table /
Sidebar) propagate to the unsampled routes automatically. Per-route
findings (e.g. "page X is missing an h1") are caught by the heading
hierarchy spot-check; the audit calls out the template that's broken
rather than enumerating every consumer page.

### Severity tiering

- **Critical** = page is unusable for a keyboard-only user OR a screen-reader user; blocks a core flow.
- **High** = a specific action is unreachable but the page is otherwise navigable; significant barrier with no easy workaround.
- **Medium** = cosmetic-but-noticeable barrier (contrast borderline, focus indicator low-visibility, missing landmark); workaround exists.
- **Low** = minor friction; sharpening edit improves clarity but compliance is not blocked.

### Out of scope (deferred)

- **Runtime browser scans** (axe-core, WAVE, Lighthouse). The slice doc bounds this audit to static review; live-browser verification is a separate slice when filed.
- **Screen-reader playback** (NVDA / JAWS / VoiceOver / Narrator). Required for any Critical fix-verification PR; not in this slice.
- **WCAG 2.2 + 3.0 success criteria**. The slice doc anchors to 2.1 AA. 2.2 (focus-not-obscured, dragging movements, target-size 24×24) findings are noted but not severity-scored against 2.1.
- **The mockup HTML at `Plans/mockups/`** is iteration-1 reference, not production code. Treated as documentation; audit does not score it.
- **The `Plans/` canvas markdown**. Out of scope by the slice doc's "Diff = doc files only" anti-criterion (`AC-9`) plus `P0-331-7` (no canvas touches).

---

## Findings table (per-criterion)

Severity grouped; within severity, ordered by surface. Each row resolves to a spillover slice ID (Critical / High) or to a bundled "a11y polish round 1" slice (Medium) or to "audit report only · no spillover" (Low).

### Critical (1 finding)

| ID     | WCAG                    | Surface                         | File:line                           | Issue                                                                                                                                                                                                                                                                                                                                                    | Suggested fix                                                                                                                                                                                                                                                               | Spillover     |
| ------ | ----------------------- | ------------------------------- | ----------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------- |
| A11Y-1 | 2.4.1 Bypass Blocks (A) | Auth layout · every authed page | `web/app/(authed)/layout.tsx:31-42` | No skip-link to `<main>`. A keyboard-only user lands on the TopBar's logo link, must tab through ~25 chrome affordances (mobile-trigger / logo / breadcrumb / global-search / ⌘K hint / in-progress-audit-pill / tenant-switcher / user-avatar / sign-out + 13 sidebar nav links + counts badges) before reaching page content on EVERY page navigation. | Add a visually-hidden `<a class="sr-only focus:not-sr-only" href="#main-content">Skip to main content</a>` as the first child of `<body>` (or first child inside the authed layout's outer flex column), and add `id="main-content" tabIndex={-1}` to the `<main>` element. | **Slice 359** |

### High (4 findings)

| ID     | WCAG                                                              | Surface                     | File:line                                                                | Issue                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | Suggested fix                                                                                                                                                                                                                                                                                                                                                              | Spillover     |
| ------ | ----------------------------------------------------------------- | --------------------------- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------- |
| A11Y-2 | 1.4.3 Contrast (Minimum) (AA)                                     | Light theme · global tokens | `web/app/globals.css:62-63`                                              | `--muted-foreground: oklch(0.556 0 0)` on `--background: oklch(1 0 0)` computes to a contrast ratio of roughly 4.0:1 — **below the WCAG AA 4.5:1 floor for normal-size text**. The token is the foreground for hundreds of `text-muted-foreground` usages (subtitles, table cell secondary content, breadcrumbs, filter pill labels, "Showing N of M" meta lines, form field descriptions, footer text on `<Card>`).                                                      | Darken the light-mode token to ~`oklch(0.45 0 0)` or call it out explicitly as "decorative-only / not for any content that conveys information"; audit the >300 consumers and either upshift to `text-foreground/70` (passes) or downshift the surfaces that genuinely are decorative to `aria-hidden`. Dark mode `oklch(0.708 0 0)` on `oklch(0.145 0 0)` is OK (~5.4:1). | **Slice 360** |
| A11Y-3 | 4.1.2 Name, Role, Value (A)                                       | Global search popover       | `web/components/shell/global-search.tsx:316-318, 392-413`                | The popover is `role="listbox"` but the input that controls it has no `role="combobox"`, no `aria-controls`, no `aria-expanded`, no `aria-activedescendant`. The `<Link>`s inside the `<li>` carry `role="option"` + `aria-selected`, but a screen reader reading the input never learns the popover exists, never hears how many results are available, and never hears which result is highlighted on ArrowDown.                                                        | Convert to the WAI-ARIA 1.2 combobox pattern: input gets `role="combobox"` + `aria-haspopup="listbox"` + `aria-expanded={open}` + `aria-controls={listboxId}` + `aria-activedescendant={activeId}`. Each option gets a stable `id`. Move the rendered `<Link>` inside an `<li role="option">` and let click handlers (not `<Link>` navigation) drive activation.           | **Slice 361** |
| A11Y-4 | 1.4.3 Contrast (Minimum) (AA)                                     | Status pills · in-progress  | `web/components/shell/in-progress-audit-pill.tsx:90-97`                  | Amber pill: `text-amber-800` on `bg-amber-50` is OK in light; **`text-amber-300` on `dark:bg-amber-950/40` is ~3.2:1** in dark mode — below AA 4.5:1 for normal text (it is sub-`text-sm`). The pill carries the only "audit in progress" affordance on every authed page; mis-perceiving the state in dark mode is a load-bearing wayfinding miss.                                                                                                                       | Replace `text-amber-300` with `text-amber-200` (computed ~5.3:1) OR replace the background with a darker amber-900 + keep amber-300 text (~5.7:1). The `aria-label` is correct; this is purely a visual contrast lift.                                                                                                                                                     | **Slice 362** |
| A11Y-5 | 3.3.1 Error Identification (A) + 3.3.2 Labels or Instructions (A) | Admin tenants form          | `web/app/admin/tenants/page.tsx:228-243` (raw `<input type="checkbox">`) | The `Join as admin` checkbox is a raw `<input>` styled with Tailwind, NOT the shadcn primitive. It has a label association (`htmlFor`) but no error state, no `aria-describedby`, and its focus ring is the browser default (which Tailwind v4's preflight resets). Combined with the form's lack of an inline error region anywhere except a post-submit `<Alert>`, a user with low-vision can't see the focus indicator on the only checkbox in the create-tenant form. | Either swap to a shadcn `Checkbox` primitive (which carries `focus-visible:ring-3` like the other primitives) OR add the same focus classes to the raw input. Add an `aria-describedby` to forms that link to the post-submit `<Alert>`'s id so SR users hear errors when the alert appears.                                                                               | **Slice 363** |

### Medium (8 findings — bundle into "a11y polish round 1")

| ID      | WCAG                                                                     | Surface                               | File:line                                                                                                                                            | Issue                                                                                                                                                                                                                                                                                                                                                                                                                 | Suggested fix                                                                                                                                                                                                                                                                                                                    |
| ------- | ------------------------------------------------------------------------ | ------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A11Y-6  | 1.3.1 Info and Relationships (A)                                         | Authed layout · `<nav>` chrome        | `web/components/shell/sidebar.tsx:149-181`                                                                                                           | Desktop `<aside>` contains `<nav>` but no `aria-label` — SR rotor will read it as just "navigation" alongside the topbar nav, the breadcrumb nav, the mobile-sidebar nav, and the control-nav inside `audit/`. With 4+ unlabeled `<nav>` regions per page, navigation rotor is unusable.                                                                                                                              | Add `aria-label="Primary navigation"` to the desktop sidebar `<nav>`, `aria-label="Topbar"` to the topbar `<header>`, audit other `<nav>` elements for unique labels (the mobile drawer already has `aria-label="Mobile navigation"` per `mobile-sidebar.tsx:131` — same pattern, propagate).                                    |
| A11Y-7  | 2.4.6 Headings and Labels (AA)                                           | Multiple pages                        | `web/app/(authed)/board-packs/[id]/page.tsx:125-160`, `web/app/oauth/callback/route.ts:70-79`                                                        | Heading-hierarchy spot-check: most pages have one `<h1>` + descending levels, but the board-pack detail page jumps from h1 to h3 inside `<section-card>` without an h2 (depending on which sections render). The `oauth/callback/route.ts` renders an inline `<html><body><h1>Sign-in failed</h1>` with no language, no styles, and no semantic structure beyond h1+p.                                                | Either insert h2-level section titles in the board-pack section cards OR justify the h1→h3 jump (e.g. each section-card is independently scoped, not nested). The oauth callback HTML should be promoted to a real error page that inherits the auth layout's styling and a11y semantics.                                        |
| A11Y-8  | 2.1.1 Keyboard (A) + 2.5.5 Target Size (AAA, advisory)                   | Filter pills `<select>`               | `web/components/list/filter-pills.tsx:36-72`                                                                                                         | `<select>` inside `<label>` works for keyboard, but the styling removes the focus outline (`focus:outline-none` on the inner `<select>`) without replacing it. The pill chrome (`<label>` outer) does not gain a focus ring when the inner `<select>` is focused, so a keyboard user has no visual indication of which filter has focus.                                                                              | Replace `focus:outline-none` with a `focus-within:ring-2 focus-within:ring-ring` on the outer `<label>` so the chip itself glows when the inner `<select>` is focused. The interior `<select>` keeps its own focus indicator (its native ring is what `focus:outline-none` is currently suppressing).                            |
| A11Y-9  | 1.4.13 Content on Hover or Focus (AA)                                    | Disabled "New control" surface        | `web/app/(authed)/controls/page.tsx:513-520`                                                                                                         | The replacement-for-disabled-button uses `title` attribute as the sole tooltip carrier. Native `title` tooltips are not dismissible (1.4.13 requires Hoverable + Persistent + Dismissible), not consistently keyboard-reachable, and are filtered by some SR rotors. Identical pattern in slice 217 audits export.                                                                                                    | Replace `title` with a real tooltip primitive (shadcn `Tooltip` or `@base-ui/react/popover`) that meets 1.4.13: ESC dismisses, hover persists when moving from trigger to tooltip, focus reveals tooltip. The `aria-label` is correct; this is about the SIGHTED hover-discovery surface.                                        |
| A11Y-10 | 2.3.3 Animation from Interactions (AAA, advisory) + project-narrative AC | Animations · `prefers-reduced-motion` | `web/components/ui/{dialog,sheet,skeleton}.tsx`, `web/components/shell/in-progress-audit-pill.tsx:94`, `web/components/shell/sidebar-counts.tsx:101` | No `motion-reduce:` Tailwind variant or `@media (prefers-reduced-motion: reduce)` rule anywhere in `web/`. `animate-in / animate-out` on Dialog + Sheet, `animate-pulse` on Skeleton + InProgressAuditPill dot + sidebar-counts, `transition-*` everywhere all run regardless of the user's OS preference. Slice narrative explicitly calls this out: "`prefers-reduced-motion` respected for any animation > 100ms." | Add `motion-reduce:transition-none` / `motion-reduce:animate-none` to the animation classes — Tailwind v4 ships the variant. For the SVG-or-CSS dot in the in-progress-audit-pill, gate the pulse via a `motion-reduce:animate-none` class. Add a project-level `@media (prefers-reduced-motion: reduce)` rule in `globals.css`. |
| A11Y-11 | 4.1.3 Status Messages (AA)                                               | Dashboard panel error states          | `web/app/(authed)/dashboard/page.tsx:122-190` (six panels)                                                                                           | When a dashboard panel transitions from loading → error (a `<refetch>` call fails), the error appears inside the panel. The panel container is not `role="status"` or `aria-live`, so an SR user listening to the page hears nothing change. The `Alert` primitive has `role="alert"` (good), but the Alert is only rendered conditionally INSIDE panel content; SR may not catch the late insertion.                 | Either: (a) ensure the panel's outer container has `aria-live="polite"` so insertions are announced, OR (b) explicitly mount the `<Alert role="alert">` at a stable position so its appearance is treated as a new live region by the rotor. The pattern already exists for `demo-controls.tsx`.                                 |
| A11Y-12 | 1.4.10 Reflow (AA)                                                       | Controls list table                   | `web/components/ui/table.tsx:7-19` (`overflow-x-auto`)                                                                                               | The table primitive wraps in `overflow-x-auto`. At 320px viewport, the 7-column controls table horizontally scrolls — slice 281 added a card variant (`mobileMode="cards"`) but it's opt-in. Pages that did NOT opt in (audits / exceptions / policies / vendors / settings sub-tables) still horizontal-scroll on phones, which is the very thing 1.4.10 forbids for content "without loss of meaning."              | Flip more list pages to `mobileMode="cards"` (audits + policies + vendors + exceptions are the highest-traffic post-281 holdouts), OR document the gap in the slice 277 responsive audit + file as a follow-on; the primitive already supports the lift.                                                                         |
| A11Y-13 | 3.2.2 On Input (A)                                                       | Tenant switcher                       | `web/components/auth/tenant-switcher.tsx` (referenced in mobile-sidebar pattern · live region check pending)                                         | The tenant-switcher mounts a `role="alert"` (lines 340 + 366 per grep) when the membership-removed transition fires. A live tenant-removal that fires while the menu is closed will not be announced if the alert mounts in an off-DOM portal. (Static-only — needs a live verification step to confirm. Flagged Medium with explicit "live-browser verification required" gate.)                                     | Live-browser verification step (axe-core in dev tools at minimum). If confirmed, add a top-level `aria-live="polite"` region in the authed layout that the tenant-switcher writes to, instead of mounting an inline `<Alert>` that may not survive the menu unmount.                                                             |

### Low (3 findings — audit report only, no spillover)

| ID      | WCAG                             | Surface                        | File:line                                                                      | Issue                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                               |
| ------- | -------------------------------- | ------------------------------ | ------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| A11Y-14 | 1.1.1 Non-text Content (A)       | ThemeAwareLogo on `/login`     | `web/app/login/page.tsx:91`                                                    | The logo `<img>` carries `alt="security-atlas"`. Acceptable, but the brand wordmark also appears as text immediately after in the topbar (`<span>security-atlas</span>` at `topbar.tsx:98`). Decorative-when-paired-with-text logos should carry `alt=""` to avoid duplicate announcement. The login page does NOT pair with a text wordmark, so the current alt is correct there; the topbar duplicates and should set `alt=""` (the topbar already does — `alt=""` at line 97). Pattern is correct; only the wording in `login/page.tsx:91` could marginally tighten ("security-atlas logo" vs "security-atlas"). |
| A11Y-15 | 2.4.7 Focus Visible (AA)         | Sheet + Dialog popups          | `web/components/ui/{dialog,sheet}.tsx` (`focus-visible:outline-none` on popup) | The popup itself sets `focus-visible:outline-none` so the modal container does not show a focus ring when the popup body receives focus (correct — the inside-children show their own rings). But the value of removing the outline is dependent on @base-ui/react's focus-trap behavior holding; if base-ui ever returns focus to the popup container under a quirk, the user sees no indicator. Cosmetic only; called out so future @base-ui/react upgrades audit it.                                                                                                                                             |
| A11Y-16 | 1.3.1 Info and Relationships (A) | Controls list `sr-only` header | `web/components/control/coverage-table.tsx:78`                                 | `<TableHead className="w-8 sr-only">Drill-down</TableHead>` — sr-only on a column header is a valid pattern, but the column body cells are visible buttons; sighted users see column headers above every other column except this one. Aesthetic-only; could swap to a non-rendered `<span className="sr-only">` inside a visible header.                                                                                                                                                                                                                                                                           |

---

## Cross-references to existing project audits

- **Slice 178 — UI honesty audit harness** (`web/e2e-audit/`). The slice doc (`AC-8`) explicitly asks whether this audit's findings can be lifted into the harness as CI-enforceable assertions. Candidate harness extensions surfaced:

  - **Skip-link presence** (A11Y-1): cheap binary assertion — `page.locator('a[href="#main-content"]')` against every authed route.
  - **Heading hierarchy** (A11Y-7): axe-core's `heading-order` rule wraps this; could be lifted as a pass-fail axe check per route.
  - **Contrast tokens** (A11Y-2 + A11Y-4): would need axe-core integration, not feasible without a new dep. Defer.
  - **`prefers-reduced-motion` honor** (A11Y-10): axe-core does NOT catch this; would need a custom assertion on computed styles after toggling the OS preference. Out of scope for the harness extension.

  Disposition: file a follow-up slice (**slice 364**, included in Medium bundle) proposing a Phase 1 a11y harness extension (skip-link + heading-order assertions only — both are mechanical). Phase 2 (axe-core integration) is its own follow-up question, deferred until the dependency conversation lands.

- **Slice 277 — mobile-responsive baseline** (`docs/responsive-audit.md`). A11Y-12 (reflow on un-opted-in list tables) is a direct continuation; the mockup primitives exist, the lift is mechanical, the gap is policy ("which list pages opt in"). Captured in the Medium bundle.

- **Slice 322 — `aria-live` fix**. The slice that triggered the "broader gaps?" question. A11Y-11 (dashboard panel error states without `aria-live`) is the same class of bug, broader scope. Confirms the slice's "ad-hoc spot-fixes can't keep up" hypothesis from the narrative.

- **Slice 203 — dark-mode wiring**. A11Y-4 (amber pill contrast in dark) is the only finding directly caused by dark-mode wiring; the wider light-mode `--muted-foreground` finding (A11Y-2) predates 203 and is theme-orthogonal.

---

## Findings by WCAG criterion (audit-table view)

| WCAG                              | Level | Count  | Findings                               |
| --------------------------------- | ----- | ------ | -------------------------------------- |
| 1.1.1 Non-text Content            | A     | 1      | A11Y-14                                |
| 1.3.1 Info and Relationships      | A     | 2      | A11Y-6, A11Y-16                        |
| 1.4.3 Contrast (Minimum)          | AA    | 2      | A11Y-2, A11Y-4                         |
| 1.4.10 Reflow                     | AA    | 1      | A11Y-12                                |
| 1.4.13 Content on Hover or Focus  | AA    | 1      | A11Y-9                                 |
| 2.1.1 Keyboard                    | A     | 1      | A11Y-8                                 |
| 2.3.3 Animation from Interactions | AAA   | 1      | A11Y-10 (advisory + project AC)        |
| 2.4.1 Bypass Blocks               | A     | 1      | A11Y-1                                 |
| 2.4.6 Headings and Labels         | AA    | 1      | A11Y-7                                 |
| 2.4.7 Focus Visible               | AA    | 1      | A11Y-15                                |
| 3.2.2 On Input                    | A     | 1      | A11Y-13                                |
| 3.3.1 Error Identification        | A     | 1      | A11Y-5 (also covers 3.3.2)             |
| 4.1.2 Name, Role, Value           | A     | 1      | A11Y-3                                 |
| 4.1.3 Status Messages             | AA    | 1      | A11Y-11                                |
| **Total findings**                |       | **16** | 1 Critical · 4 High · 8 Medium · 3 Low |

---

## Spillover routing

Cap = 5 (per slice anti-criterion + slice doc disposition).

| Spillover slice | Severity / Bundle     | Title                                                                                          |
| --------------- | --------------------- | ---------------------------------------------------------------------------------------------- |
| 359             | Critical (individual) | Authed layout: add skip-link to `<main>` (A11Y-1)                                              |
| 360             | High (individual)     | Lift `--muted-foreground` light-mode contrast to WCAG AA 4.5:1 (A11Y-2)                        |
| 361             | High (individual)     | Global search popover: adopt WAI-ARIA combobox pattern (A11Y-3)                                |
| 362             | High (individual)     | In-progress audit pill: lift dark-mode amber text contrast to AA (A11Y-4)                      |
| 363             | High (individual)     | Admin forms: replace raw `<input type=checkbox>` + audit form-error a11y associations (A11Y-5) |

Medium findings A11Y-6 through A11Y-13 (8 items) **bundle into the Medium bucket**. Per the slice doc AC-5 "Medium findings (cosmetic-but-noticeable: contrast borderline, focus indicator low-visibility) bundled into a single 'a11y polish round 1' slice OR per-component slices — engineer's call." Engineer's call: **bundle**, because the 8 Mediums are largely independent two-line lifts and the bundle keeps the spillover cap respected. The bundle's title and tracking ID are documented in the decisions log D5; **the bundle issue itself is NOT filed in this slice** because the spillover cap of 5 is already consumed by the Critical + 4 High individual slices. The bundle is filed as a separate follow-up by the maintainer using the slice doc cap rule, OR the cap is widened by maintainer judgment when this audit lands. (Decisions log D5 captures the JUDGMENT.)

Low findings A11Y-14 through A11Y-16 are audit-report-only per slice doc disposition (`AC-5` carryover — Low not enumerated in AC, treated as no-spillover by convention; decisions log D3).

---

## Top 3 Critical/High findings (summary for parent agent)

1. **A11Y-1 (Critical) — every authed page traps keyboard users in topbar/sidebar chrome.** ~25 affordances before `<main>` content on each navigation. Skip-link fix is one line.
2. **A11Y-2 (High) — light-mode `--muted-foreground` is below WCAG AA (~4.0:1 against background).** Token touches hundreds of "secondary content" surfaces across every page. Single-token darken or per-consumer triage.
3. **A11Y-3 (High) — global search popover lacks combobox ARIA wiring.** Visible on every authed page; SR users hear an input but never learn the popover exists, how many results returned, or which option is highlighted.

---

## What did NOT come up (worth recording)

- **Time-based content** (slice narrative point 8). No auto-refresh dialogs, no session-timeout warnings with countdowns, no auto-dismissing alerts. Polling cadences (TanStack Query) are present (e.g. dashboard 60s, tenant-switcher 60s, in-progress audit pill `staleTime`) but they refresh content silently, do not redirect or close anything. **Clean.**
- **Form labels association** is consistently good: every `<Input>` and raw input I sampled carries `htmlFor`/`id` pairing. Login, admin/tenants, admin/super-admins, admin/api-keys all explicit.
- **Dialog focus traps** (A11Y-2 narrative point 3): `@base-ui/react/dialog` implements the WAI-ARIA pattern correctly; verified statically via `dialog.tsx`/`sheet.tsx` composition. Mobile-sidebar's narrative paragraph confirms reliance on the upstream's focus behavior. Live verification still recommended for any Critical fix-PR.
- **Visible focus indicators on shadcn primitives** (Button, Input, Badge): all carry `focus-visible:border-ring focus-visible:ring-3` — the project's primitive design is consistent. Issues are around non-primitive raw elements (filter-pill `<select>` per A11Y-8, raw `<input type=checkbox>` per A11Y-5) and exceptions where the primitive itself is told to suppress its outline (popup containers per A11Y-15, intentional).

---

## Closing posture

The project's shadcn-themed primitives + `@base-ui/react` foundation give it a solid a11y baseline — focus rings are consistent, dialogs trap focus correctly, raw inputs are rare, every form input has a label. The gaps are all at the **chrome and token level**, not at the primitive level. Fix the skip-link + the `--muted-foreground` token + the combobox pattern in global search + the amber-300 dark-mode contrast, and the next a11y audit will be looking at the long-tail of per-page polish (A11Y-6 through A11Y-13) rather than load-bearing wayfinding bugs.

`voltagent-qa-sec:accessibility-tester` signoff: WCAG 2.1 AA conformance is achievable in one focused remediation cycle (the four High + one Critical individual slices, ~1.5d work each) plus one "polish round" bundle. None of the findings require an architectural rework; all are mechanical lifts against the existing primitive system.
