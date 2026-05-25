# Slice 277 — Mobile-responsive baseline — decisions log

**Slice spec:** [`docs/issues/277-mobile-responsive-baseline.md`](../issues/277-mobile-responsive-baseline.md)
**Branch:** `frontend/277-mobile-responsive-baseline`
**Status:** in-review

This log captures the JUDGMENT calls the implementing agent made while
building the slice. Slice 277 is `Type: AFK`, but the spec carries three
decision slots (D1-D3) to be settled by the engineer; this file is the
record.

---

## D1 — Drawer primitive: **roll our own `<Sheet>` over `@base-ui/react/dialog`**

The spec AC-4 names "shadcn `<Sheet>` component" as the recommended
drawer primitive. The canonical shadcn implementation sources from
`@radix-ui/react-dialog` — but security-atlas's existing shadcn-themed
primitives wrap `@base-ui/react` instead (slice 097 introduced the
`Dialog` at `web/components/ui/dialog.tsx` on top of
`@base-ui/react/dialog`). Adding `@radix-ui/react-dialog` would be a
new top-level dependency, which P0-277-8 explicitly forbids ("Drawer
composes from existing shadcn `<Sheet>` (already installed)" — but it
ISN'T already installed).

**Options considered:**

- (a) `npx shadcn add sheet` and accept the new Radix dep. Violates
  P0-277-8.
- (b) Build a `<Sheet>` primitive at `web/components/ui/sheet.tsx`
  using the same `@base-ui/react/dialog` foundation as slice 097's
  `Dialog`, with a side-anchored popup instead of centered. No new
  dep; matches the project's existing primitive shape.
- (c) Hand-roll a non-shadcn drawer (custom `<dialog>` element + manual
  focus trap + manual Escape handler). Worst of both worlds: more
  code than (b) AND less battle-tested than (a).

**Decision: (b).** Built `web/components/ui/sheet.tsx` parallel to
`dialog.tsx`. The API surface (Sheet / SheetTrigger / SheetContent /
SheetClose / SheetHeader / SheetTitle / SheetDescription) mirrors the
shadcn `<Sheet>` so future contributors who reference the shadcn docs
find familiar names. The `side` prop on `SheetContent` (default
`"left"`) controls the anchor edge.

**Trade-off accepted:** the codebase now carries TWO sources of shadcn-
themed primitives (dialog + sheet) instead of one. Slice 097's dialog
established the foundation; slice 277 extends it. Future overlays (a
popover, a tooltip) should follow the same shape rather than oscillating
between `@base-ui/react` and `@radix-ui/react-*`.

**Confidence: high.** The Sheet renders the same WAI-ARIA dialog pattern
(focus trap, Escape close, outside-click close, scroll lock) the
existing Dialog already exercises in three admin pages.

---

## D2 — Viewport breakpoint: **`md = 768px`** (Tailwind default)

The spec AC-3 / AC-5 names `md` (Tailwind 768px) as the desktop/mobile
fork. Considered briefly: would `lg` (1024px) be a better fork? Tablets
(iPad portrait = 768px, iPad landscape = 1024px) sit between the two.

**Decision: `md` (768px).** Rationale:

1. **Aligns with the Tailwind default.** Every other responsive
   precedent in the codebase (slice 056 hierarchical risk, slice 040
   dashboard, the `md:grid-cols-*` family throughout `(authed)/**`)
   keys off `md`. A drawer that switches at `lg` would diverge from the
   established discipline and would render double chrome (drawer +
   inline sidebar) at the 768-1023px window.
2. **Tablet portrait is "small desktop", not "big phone."** The persistent
   sidebar at 768px portrait still fits comfortably (56-unit / 224px
   wide sidebar on a 768px viewport leaves 544px for content — usable).
   The collapsed drawer is the right shape at < 768px (phone widths).
3. **Empirical: every modern web framework's mobile-drawer pattern keys
   off ~768px.** Linear, Vercel, Stripe Dashboard, Notion — all key the
   sidebar collapse at roughly this breakpoint.

**Confidence: high.** This is a settled industry pattern; the only
plausible alternative (`lg`) has known UX cost (double chrome at tablet
portrait).

---

## D3 — Audit-doc shape: **per-row table with three columns + verdict legend** (matches spec proposal exactly)

The spec AC-9 proposed: `route | mobile-ready | notes`. The shipped
audit doc at [`docs/responsive-audit.md`](../responsive-audit.md) uses
exactly that shape, plus:

1. A verdict legend (yes / partial / no / n/a) at the top — explicit
   semantics so a future reader doesn't guess.
2. A "Methodology" section recording how each verdict was reached
   (static code inspection at slice 277 time, not a real-device pass —
   the conservative cut: when in doubt, `partial`, not `yes`).
3. The shell (cross-cutting chrome) audited separately from authed
   routes — the sidebar / topbar / footer surface is shared and gets
   its own row group.
4. A separate "Spillover slices filed" section linking to slice 281
   (the priority-three list-table card-collapse) and naming the future
   per-page slices that will land out of the `partial` rows.

**Decision: ship as proposed in the spec, augmented with the four
notes above.** The augmentation is additive — the load-bearing
table shape is identical.

**Trade-off accepted:** the audit doc is intentionally **conservative**.
Many rows landed `partial` based on static inspection that a real-device
walkthrough might re-verdict to `yes`. A future maintainer who walks
the routes at 375px on a real phone can re-verdict (or file the
follow-on slice if the verdict holds).

**Confidence: medium.** The verdicts are reasoned from code inspection,
not from device testing. The "conservative cut" mitigation reduces the
risk that a `yes` verdict is wrong (under-promising is fine; the
follow-on slice fan-out is the slice 277 deliberate shape). But several
rows could be `yes` after testing — the audit doc explicitly invites
re-verdiction.

---

## D4 (engineer-side) — Mobile drawer nav-data shape: **pass {href,label} array from server to client; re-mount badges by href match**

The spec AC-4 calls out: "the drawer composes with existing chrome
(slice 213 audit pill + slice 214 count badges + slice 223 ⌘K + slice
250 avatar)." The desktop `<Sidebar>` consumes a `NavItem[]` where each
item carries an optional `slot: ReactNode` for the count badges.

**Problem:** the `slot` prop is a React node — server-side it carries a
JSX element of the badge client component. Passing React elements
across the server-component → client-component boundary is supported in
Next.js 16, but it makes the drawer recompute the same badge JSX every
render (badges are client components and would be re-instantiated on
each open/close cycle).

**Decision:** the authed layout (`web/app/(authed)/layout.tsx`) calls
`getAuthedNav()` and maps to `{href, label}[]` (drops the slot) before
passing into the client `<MobileSidebar>`. The drawer renders the same
badges by href match (a small switch in `mobile-sidebar.tsx`'s
`badgeForHref(href)` helper).

**Trade-off accepted:** the badge-to-href mapping is duplicated between
the desktop sidebar (where the badge is a static JSX prop on the NAV
array entry) and the mobile drawer (where the badge is selected by
href). If a future slice adds a third badge, both sites need updating.
A linter rule or a shared constant would catch the drift; for slice
277's minimal-touch scope, the duplication is acceptable and called
out here.

**Confidence: medium.** The duplication is small (two cases —
controls and risks) and the drift cost is "a new badge takes one
extra line."

---

## D5 (engineer-side) — Topbar items at narrow widths: **leave the slice 213/223/250 chrome in the topbar; do NOT relocate into the drawer**

The spec AC-4 names four chrome surfaces that the drawer must "compose
with": audit pill (213), count badges (214), ⌘K search (223), avatar
(250). The plain reading suggests all four end up "in the drawer." But
that conflates two different chrome locations:

- The **sidebar** previously hosted: nav-items + the slice 214 count
  badges. These belong in the drawer.
- The **topbar** previously hosted: logo, breadcrumb, GlobalSearch,
  InProgressAuditPill, TenantSwitcher, UserAvatar, Sign-out. These
  stay in the topbar.

**Decision:** the drawer hosts ONLY what the sidebar previously hosted
(nav-items + count badges). The topbar items stay in the topbar at all
widths. At 375px the topbar gets dense — the slice 277 audit doc
records this as a `partial` verdict for the topbar with a spillover
note ("hide-or-collapse some items at < sm").

**Rationale:**

1. Per AC-4 the avatar is "preserved in the topbar even at small widths"
   — the spec is explicit about the avatar at least staying in topbar.
   The other three (pill / ⌘K / TenantSwitcher) compose to the same
   "right-side topbar chrome" cluster; splitting some into the drawer
   and not others would be inconsistent.
2. The slice 277 scope ("foundational baseline; per-page work spillovers")
   says do NOT rework chrome content layout in this slice. Relocating
   topbar items would be content rework.
3. The topbar density issue is real but minor — at 375px most users
   will reach the breadcrumb / logo from the left and the avatar from
   the right; the in-between (search box, audit pill, TenantSwitcher
   when active) compresses. A future spillover slice can collapse the
   non-load-bearing items at `< sm`.

**Confidence: high.** The minimal-touch interpretation aligns with the
slice's "foundational baseline" scope and the user's "desktop browser
users should continue to use the existing UI" hard ask.

---

## D6 (engineer-side) — Hamburger glyph: **inline SVG, no icon library** (P0-277-8)

The hamburger trigger renders a glyph. The two paths:

- (a) Add `lucide-react` or `@heroicons/react`. Each is a new top-level
  dep. Violates P0-277-8.
- (b) Inline SVG, three stacked `<line>` strokes. Matches the existing
  slice 213 / slice 075 pattern of "no icon library; inline SVG
  where needed."

**Decision: (b).** The hamburger glyph is six lines of SVG; an icon
library is overkill for a single use site. If a future slice surfaces
the need for ten more icons, that's the time to weigh adding a library
(separate slice + threat-model review + bundle-size analysis).

**Confidence: high.** Inline SVG is the codebase convention.

---

## Verification

- **AC-1.** `web/app/layout.tsx` exports `viewport: Viewport = { width:
"device-width", initialScale: 1 }`. Confirmed in the file.
- **AC-2.** `web/e2e/mobile-baseline.spec.ts` asserts the meta tag
  resolves on `/dashboard`.
- **AC-3..AC-7.** The same spec exercises the drawer toggle at 375px +
  the desktop-no-trigger at 1280px + Escape close + nav-click close.
- **AC-8.** `web/docs/responsive-discipline.md` ships with the
  breakpoint table, three-checkpoint test rule, default layout
  pattern, ban list, slice precedents, and the new-page checklist.
- **AC-9.** `docs/responsive-audit.md` ships with the per-route
  verdict table covering authed + admin routes + the shell, plus a
  spillover-slice section linking to slice 281.
- **AC-10..AC-13.** The new spec is additive; existing Playwright +
  vitest suites are not modified.
- **AC-14.** CHANGELOG entry added under `## [Unreleased]` → `### Added`.
- **AC-15.** This file.

## Revisit once in use

The conservative-cut verdicts in `docs/responsive-audit.md` are the top
of the revisit list. Specifically:

1. **Re-walk each `partial` row at 375px on a real device.** Several
   may re-verdict to `yes`. The honest record will drive the
   spillover-slice fan-out.
2. **Re-verdict the shell topbar density.** The audit row marks it
   `partial` for the right-side chrome (search + pill + switcher +
   avatar + Sign out). If the operator-feedback says it's actually
   usable, the row becomes `yes` and no spillover needed. If not, file
   the spillover for `< sm` chrome collapse.
3. **Verify Next.js 16 viewport export emits the expected meta tag at
   build time.** The spec assumes the App Router `viewport` export
   compiles to `<meta name="viewport">` in `<head>`. The slice 277 e2e
   spec asserts this at runtime, but a static build-output check would
   add belt-and-suspenders coverage. Optional follow-on.
4. **The slice 281 follow-on (list-table card-stack collapse) is the
   load-bearing per-page work.** Land that before any other table-heavy
   per-page mobile slice.
5. **Sheet primitive maturity.** The `<Sheet>` at
   `web/components/ui/sheet.tsx` ships only used by one consumer
   (mobile drawer). If a second consumer needs a side-anchored drawer
   (e.g., a mobile filter panel on `/controls`), validate that the
   primitive's API surface is right; iterate on the API in the same
   slice as the second consumer.
