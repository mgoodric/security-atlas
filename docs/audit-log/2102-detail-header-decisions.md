# Issue #2102 — Shared detail-page header

The vendor detail page's header (status, title, Edit, Record review) moves
into `web/components/detail/detail-header.tsx`, with an overflow menu whose
destructive items confirm inline. Vendor detail renders through it with every
testid and its behaviour unchanged. Parent #2071 adopts it on the other record
types.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — One shared overflow menu, in `components/ui`, for #2090 too

**Decision.** The overflow menu and its inline confirm are
`components/ui/overflow-menu.tsx` (`OverflowMenu`, `InlineConfirm`), and the
rule "a destructive item never fires from the menu" is a pure reducer in
`components/ui/overflow-menu-model.ts`. The detail header uses
`OverflowMenu`; it holds no menu code of its own.

**Why.** #2071 says destructive items use the row-actions pattern (#2090),
which was still in progress when this landed. One implementation serves both:
`OverflowItem` (`label`, `destructive`, `confirmPrompt`, `onSelect`) is what a
row menu needs, and `testIdPrefix` keeps each surface's selectors distinct.
Whichever of #2090 and #2102 merges second adopts the other's primitive
instead of adding a second menu.

## D2 — base-ui Menu for the menu-button semantics

**Decision.** Use `Menu.Trigger` / `Menu.Popup` rather than a hand-rolled
popover.

**Why.** It gives `aria-haspopup="menu"`, `aria-expanded`, arrow-key
navigation, Esc to close and focus back on the trigger. The one override is
`finalFocus`: when a destructive item opens the confirm, focus goes to its
Cancel button instead of the trigger. Esc or either button closes the confirm
and returns focus to the trigger.

## D3 — Component test in the node tier, without a DOM

**Decision.** `detail-header.test.ts` renders the header with
`createElement` + `react-dom/server` (no JSX, no new dependency) and drives
the confirm rule through the model (`overflow-menu.test.ts` for the menu,
`detail-header.test.ts` for the header).

**Why.** Vitest is node-only and React component tests are out of scope for
v1 (slice 069 P0-A3, slice 353 Q-3). Static markup covers status, Edit, the
primary action, the menu-button attributes on the trigger and the inline
confirm. The menu popup is portalled and mounts only in a browser, so the
items' select/confirm/cancel behaviour is asserted through the reducer. The
vendor Playwright specs cover the header in the running app.

## D4 — The header renders through PageShell

**Decision.** `DetailHeader` wraps PageShell (#2080): the title goes in
its H1, status and subtitle in its subtitle slot, and the back link,
primary action, Edit and overflow in its actions slot, where #2226 put the
vendor page's back link. The page body is `DetailHeader`'s children.
`DetailHeaderActions` is exported on its own for a page that builds its
PageShell itself.

**Why.** One header row per page. A second header under PageShell's would
duplicate the H1.

## D5 — Order and variants kept from the vendor page

**Decision.** Primary action (secondary variant), then Edit (default
variant), then the overflow trigger, which is hidden when there are no items.

**Why.** A pure layout move for the vendor page. Vendor has no overflow items
yet, so its rendered header is unchanged.
