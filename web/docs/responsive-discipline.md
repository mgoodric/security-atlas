# Responsive discipline (security-atlas)

> The rubric a contributor follows to ship a page that works on every
> form factor security-atlas supports. Filed by slice 277 (the
> mobile-responsive baseline). Per-page audit lives at
> [`docs/responsive-audit.md`](../../docs/responsive-audit.md).

## Form-factor stance

security-atlas is **responsive CSS only** — same routes, same
components, different layouts at different widths. We do **not** ship:

- separate `/m/` mobile-only routes
- a viewport-detecting JS branch that renders `<MobileX/>` vs
  `<DesktopX/>` component trees
- a User-Agent sniff
- a native iOS / Android app

The form-factor switch is **Tailwind media-query class on existing
components** — nothing else. (Slice 277 P0-277-2, P0-277-3, P0-277-5.)

## Tailwind v4 breakpoints

Tailwind v4 default breakpoints (`web/app/globals.css` keys off these
via `@theme`):

| Prefix | Min width | Typical device                           |
| ------ | --------- | ---------------------------------------- |
| `sm`   | 640px     | small-phone landscape · phablet portrait |
| `md`   | 768px     | tablet portrait · small laptop           |
| `lg`   | 1024px    | tablet landscape · standard laptop       |
| `xl`   | 1280px    | large laptop · desktop                   |
| `2xl`  | 1536px    | large desktop · external monitor         |

**The load-bearing breakpoint for security-atlas is `md` (768px).** It
is the desktop / mobile fork: at `≥ md` the persistent sidebar renders
inline (the canonical desktop chrome); at `< md` the sidebar collapses
to a `<Sheet>` drawer behind a hamburger trigger. Every new chrome
surface that needs a form-factor switch keys off `md`.

## Test widths (the three-checkpoint rule)

Test every page at **three widths** before marking it done:

| Width  | Tier    | Why                                                        |
| ------ | ------- | ---------------------------------------------------------- |
| 375px  | mobile  | iPhone SE / 13 mini baseline; the narrowest realistic case |
| 768px  | tablet  | the `md` switch boundary; catches "almost desktop" bugs    |
| 1280px | desktop | the canonical authoring width; matches the mockups         |

Playwright's `page.setViewportSize({ width, height })` is the supported
harness; the slice 277 spec `web/e2e/mobile-baseline.spec.ts` pins the
foundational baseline for the chrome at 375px.

## Default layout pattern

Start every multi-column layout at `grid-cols-1` and grow with `md:`
or `lg:` modifiers. The canonical shape:

```tsx
<div className="grid grid-cols-1 gap-4 md:grid-cols-2 lg:grid-cols-3">
  {/* cards / panels / tiles */}
</div>
```

**Never** start at `grid-cols-2` and try to "collapse it back" — the
result always has horizontal-scroll bugs at 375px. The framework wants
mobile-first; honor that.

## Ban list

The following patterns are blocked on PR review:

- **Fixed pixel widths > 320px inside content containers.** A panel
  with `width: 600px` will horizontal-scroll at every mobile width. Use
  Tailwind's `max-w-*` scale (`max-w-md`, `max-w-2xl`, etc.); they
  resolve to `min(<value>, 100%)` semantics via `max-width`.
- **Hover-only affordances without `:focus-visible` parity.** Touch
  devices have no hover. Every `hover:` Tailwind class should be paired
  with a `focus-visible:` variant (or replaced with an always-visible
  affordance). The slice 075 logo link uses this pattern verbatim.
- **Horizontal-scroll containers without a caption or ARIA description.**
  If a table or chart genuinely cannot collapse below its content width,
  it MUST carry a `<caption>` (for tables) or an `aria-describedby`
  pointing at a "scroll horizontally to see all columns" hint. Screen
  readers cannot infer the scroll affordance otherwise.
- **Tables wider than 5 columns without an `md:` collapse pattern.**
  Slice 056's hierarchical risk dashboard is the canonical reference:
  at `< md` the table collapses to a card stack (one card per row, all
  columns stacked as label/value pairs inside the card).
- **`overflow-x: auto` on a top-level layout container.** It hides the
  bug. Fix the inner element that's too wide; never paper over.

## Sidebar drawer composition (slice 277)

Pages render inside the shared authed shell at
`web/app/(authed)/layout.tsx`. The layout owns:

- The desktop `<Sidebar>` (rendered with `hidden md:block`).
- The mobile `<MobileSidebar>` drawer (rendered with `md:hidden` on the
  trigger).
- The `<TopBar>`, which receives the mobile sidebar trigger as a
  `mobileSidebar` slot prop.

**A page does NOT mount its own sidebar.** The shell is shared. If a
page needs additional in-page navigation (a tab strip, a left rail of
filter chips, etc.) it ships those as part of its own content tree —
they MUST also follow this discipline doc.

## Precedents

Pre-277 work that established responsive patterns we now build on:

- **Slice 056** — hierarchical risk dashboard. Table-to-card-stack
  collapse at `< md` is the canonical pattern for dense tables.
- **Slice 256** — control-detail Coverage column. Mobile-aware table
  cell rendering (column hides via `hidden md:table-cell` rather than a
  separate `<MobileTable>` component).
- **Slice 213** — audits-header chrome (in-progress audit pill).
  Silent-absence pattern (returns null on zero/error) lets chrome stay
  thin at narrow widths.
- **Slice 214** — sidebar count badges. Slot-prop composition on nav
  rows; drawer-compatible because the badges are pure client components
  consuming `useQuery`.
- **Slice 223** — global ⌘K search. The ⌘K accelerator is desktop-only
  semantically; the search input remains tappable on mobile.
- **Slice 277** — THIS slice. The foundational baseline (viewport meta,
  sidebar drawer, this rubric).

## Adding a new page — the checklist

Before marking a page "done":

1. Open it at 375px width (Chrome DevTools device toolbar → "iPhone SE"
   preset, or `page.setViewportSize({ width: 375, height: 812 })`).
2. Verify no horizontal scroll on the page body. Tab through every
   interactive element with the keyboard and confirm focus is visible.
3. Open it at 768px. Verify the layout transitions cleanly through the
   `md` breakpoint — no overlapping elements, no clipped buttons.
4. Open it at 1280px. Verify the page matches the mockup (or close
   enough that a follow-on slice could close the gap).
5. If any of 1-4 fail and the fix is out-of-slice scope, add a row to
   [`docs/responsive-audit.md`](../../docs/responsive-audit.md) with
   the verdict `partial` or `no` and link to a spillover slice. The
   audit doc is the source of truth for the long tail.

## What this doc is NOT

- Not a styling guide (tokens, colors, spacing scale — those live in
  `web/app/globals.css` and the mockups under `Plans/mockups/`).
- Not an accessibility audit (a11y is its own discipline; this doc
  touches the responsive-specific a11y rules only — focus parity, table
  scroll affordance).
- Not a touch-vs-mouse interaction polish guide (long-press menus,
  swipe gestures, etc. — those are per-page polish that ships from the
  audit doc's spillovers).
