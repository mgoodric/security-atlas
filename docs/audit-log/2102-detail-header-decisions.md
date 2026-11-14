# Issue #2102 — Shared detail-page header

The vendor detail page's header (status, title, Edit, Record review) moves
into `web/components/detail/detail-header.tsx`, with an overflow menu whose
destructive items confirm inline. Vendor detail renders through it with every
testid and its behaviour unchanged. Parent #2071 adopts it on the other record
types.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Build the overflow menu and inline confirm here, shaped for #2090

**Decision.** The overflow menu is base-ui's `Menu` (already a dependency),
and the inline confirm is a small `InlineConfirm` in the same module. The
rule "a destructive item never fires from the menu" is a pure reducer in
`detail-header-model.ts`.

**Why.** #2071 says destructive items use the row-actions pattern (#2090),
but #2090 is open with no PR, so there is no `RowActions` / `ConfirmInline`
to import. Waiting would block every Spec B detail page. The `OverflowItem`
shape (`label`, `destructive`, `confirmPrompt`, `onSelect`) is what a row
menu needs too, so #2090 can lift `InlineConfirm` and the model into a shared
`RowActions` without changing the header's callers.

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
the confirm rule through the model.

**Why.** Vitest is node-only and React component tests are out of scope for
v1 (slice 069 P0-A3, slice 353 Q-3). Static markup covers status, Edit, the
primary action, the menu-button attributes on the trigger and the inline
confirm. The menu popup is portalled and mounts only in a browser, so the
items' select/confirm/cancel behaviour is asserted through the reducer. The
vendor Playwright specs cover the header in the running app.

## D4 — Order and variants kept from the vendor page

**Decision.** Primary action (secondary variant), then Edit (default
variant), then the overflow trigger, which is hidden when there are no items.

**Why.** A pure layout move for the vendor page. Vendor has no overflow items
yet, so its rendered header is unchanged.
