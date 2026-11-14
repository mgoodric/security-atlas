# #2057: board-pack pages in theme tokens

- detection_tier_actual: manual_review
- detection_tier_target: playwright

The board-pack list and the pack sheet were written in fixed Tailwind palette
classes (slate, emerald, amber, rose), so in dark mode the list's "Review" link
was near-black on a near-black card and the sheet's headings and numbers were
near-white on white. Triage decided the fix: the list uses the app's theme
tokens, and the sheet always renders as paper through a scoped light token set.

## D1: the paper scope is `.paper`, sharing the `:root` token block

`.paper` is added to the `:root` selector in `web/app/globals.css`, so the
light token set is declared once and re-declared on the sheet's own element.
Descendants inherit the light values even when `<html>` carries `.dark`.
A separate copy of the token list would drift the first time a light token
changes.

The `dark` custom variant gains `:not(:where(.paper, .paper *))`, so `dark:`
utilities inside shadcn components (Button, Input, Badge) don't fire inside
the sheet. The `:where()` keeps the variant's specificity unchanged
elsewhere. `.paper` also sets `color-scheme: light` so native controls (the
number inputs, the narrative textarea) render light.

The whole detail view is paper, including the sticky export bar, because the
bar is the sheet's own toolbar.

## D2: status chips are tinted outlines on the card, not tinted fills

The accent tokens (`--pass`, `--warning`, `--critical`, `--info`,
`--progress`) clear WCAG AA as text on a plain surface (about 4.7:1 on
white). A same-hue fill behind the text (the Badge variants use 15% in light
and 24% in dark) brings that down to about 3.9:1 in light and below 4.5:1
for `warning` in dark. The new contrast spec measured this. Board-pack chips
(status, approval, treatment, freshness) therefore carry the hue in their
text and a 40% border, and sit on `bg-card` (`web/components/board-pack/tone.ts`).

The same shortfall applies to the app-wide Badge `pass` / `warning` / ...
variants. Changing those is outside this ticket's scope (board pack only). It
is reported for a follow-up rather than changed here.

## D3: the contrast check is computed in the page, not via axe

`web/e2e/board-pack-theme-contrast.spec.ts` walks every visible element that
owns a text node. It composites the element's text colour over its stacked
ancestor backgrounds and checks the result against 4.5:1 (3:1 for large
text). Colours are resolved through a canvas, so oklch, oklab and
`color-mix()` values reduce to sRGB without a parser. Disabled controls are
exempt, as WCAG 1.4.3 allows. Using axe would mean adding a new dependency
for one check.

The spec runs in both themes against a mocked list, a draft pack (with an
approver, so it covers the editors, and with a missing section) and a
published pack. It also asserts that the sheet and its section cards are
light surfaces. With the `paper` class removed, the two dark-mode sheet tests
fail, because the sheet then has a luminance of 0.019.
