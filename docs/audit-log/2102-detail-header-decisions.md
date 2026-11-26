# Issue #2102 — Shared detail-page header

The vendor detail page's header (status, title, Edit, Record review) moves
into `web/components/detail/detail-header.tsx`, with an overflow menu whose
destructive items confirm inline. Vendor detail renders through it with every
testid and its behaviour unchanged. Parent #2071 adopts it on the other record
types.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — The overflow menu is #2090's `RowActions`

**Decision.** The header's overflow menu is `RowActions` from
`components/ui/row-actions.tsx` (#2090, merged as #2259), rendered with a new
`variant="header"` that gives an outline trigger sized like the header's
other buttons (`row`, the default, keeps the compact ghost trigger). The
header holds no menu code of its own.

**Why.** #2071 says destructive items use the row-actions pattern. This PR
first carried its own `OverflowMenu` while #2090 was in progress; the two
were built in parallel with the same item shape. The dispatcher kept
`RowActions` because it has real-browser tests and several adopters, so
`overflow-menu.tsx` and its model were deleted. Every confirm test they had
already exists in `row-actions.test.ts`.

## D2 — Component test in the node tier, without a DOM

**Decision.** `detail-header.test.ts` renders the header with
`createElement` + `react-dom/server` (no JSX, no new dependency). The
menu's confirm rule is tested with `RowActions` in `row-actions.test.ts`.

**Why.** Vitest is node-only and React component tests are out of scope for
v1 (slice 069 P0-A3, slice 353 Q-3). Static markup covers the title, status,
Edit, the primary action, the PageShell slots and the menu-button attributes
on the overflow trigger. The vendor Playwright specs cover the header in the
running app, and #2259's Playwright specs drive `RowActions` in a browser.

## D3 — The header renders through PageShell

**Decision.** `DetailHeader` wraps PageShell (#2080): the title goes in
its H1, status and subtitle in its subtitle slot, and the back link,
primary action, Edit and overflow in its actions slot, where #2226 put the
vendor page's back link. The page body is `DetailHeader`'s children.
`DetailHeaderActions` is exported on its own for a page that builds its
PageShell itself.

**Why.** One header row per page. A second header under PageShell's would
duplicate the H1.

## D4 — Order and variants kept from the vendor page

**Decision.** Primary action (secondary variant), then Edit (default
variant), then the overflow trigger, which is hidden when there are no items.

**Why.** A pure layout move for the vendor page. Vendor has no overflow items
yet, so its rendered header is unchanged.
