# security-atlas Design System — Claude Design sync bundle

This folder mirrors security-atlas's UI primitives (`web/components/ui`,
shadcn &middot; base-nova &middot; **neutral**) as self-contained HTML preview cards,
pushed to the **security-atlas Design System** project on
[claude.ai/design](https://claude.ai/design) via the `DesignSync` tool.

Each card renders in **light and dark** using security-atlas's real oklch
tokens, extracted verbatim from `web/app/globals.css` into `_tokens.css`. The
palette is the shadcn base-nova / `baseColor: neutral` seed — pure achromatic
surfaces (oklch chroma 0, no hue) with a single chromatic destructive. No brand
hue has been chosen yet; the system is intentionally neutral.

Cards are fully static (no JS, no Tailwind, no CDN beyond a Google Fonts
`@import` for Geist + Geist Mono) so they render identically locally and in the
Claude Design preview pane. Interactive components (dialog, sheet, select) are
shown in their open/resting state.

```
_tokens.css              shared palette, radii, fonts (light + dark) — re-extract if globals.css changes
index.html               catalog / overview card + token swatches
components/<name>/index.html   one preview card per component; line 1 is the `@dsCard` marker
```

Project id: `14a3f2c9-f8a6-4174-840e-de654f5ce485`

## The round-trip workflow

**1 — Push (codebase → Claude Design).** To push (or refresh after editing a
preview locally), run the sync **one component at a time**:

```
DesignSync finalize_plan  writes=["components/button/**"]  localDir=<this folder>
DesignSync write_files     planId=<id>  files=[{path, localPath}]
```

**2 — Design (on the canvas).** Open the **security-atlas Design System**
project at claude.ai/design. Refine a component visually — spacing, states,
variants, color. Claude Design edits the card's HTML/CSS, which references the
same `_tokens.css`, so changes stay on-system.

**3 — Pull back (Claude Design → codebase).** When a card looks right, bring the
change home **one component at a time** — never a wholesale overwrite:

- `DesignSync get_file path="components/<name>/index.html"` to read the refined card.
- Translate the CSS delta back into the real component:
  - **Token changes** (color, radius, spacing scale) → edit
    `web/app/globals.css`, then re-extract into `_tokens.css` so previews and
    code stay in lockstep. (If a brand hue is finally chosen, this is where it
    lands — `_tokens.css` is downstream of `globals.css`, never the source.)
  - **Component changes** (variant classes, sizes, layout) → edit the matching
    `web/components/ui/<name>.tsx` (base-nova Tailwind classes).
- Verify in the running app (`npm run dev` from `web/`) before committing.

## Rules of the road

- `_tokens.css` is **generated** from `globals.css` — change colors in
  `globals.css`, not here. Re-extract the `:root` and `.dark` blocks verbatim if
  `globals.css` changes.
- The values are **neutral and verbatim**. Do not invent a brand hue or nudge an
  oklch value inside this bundle; the seed is deliberately achromatic.
- Source of truth for component behavior is `web/components/ui/*.tsx`. The cards
  are a visual mirror, not the implementation.
- Keep pushes incremental (per component) so the Design System pane diffs cleanly.

## Component coverage

| Card       | Mirrors                              | Variants / states shown                                                                                                       |
| ---------- | ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------- |
| `button`   | `button.tsx`                         | default, outline, secondary, ghost, destructive (soft), link; sizes xs/sm/default/lg + icon-xs/icon-sm/icon/icon-lg; disabled |
| `badge`    | `badge.tsx`                          | default, secondary, destructive (soft), outline, ghost, link; status-dot                                                      |
| `alert`    | `alert.tsx`                          | default, destructive                                                                                                          |
| `input`    | `input.tsx`                          | default, focus, disabled, invalid, with label                                                                                 |
| `checkbox` | `checkbox.tsx` + `checkbox-class.ts` | unchecked, checked, focused, disabled                                                                                         |
| `progress` | `progress.tsx`                       | several values + a wide track                                                                                                 |
| `card`     | `card.tsx`                           | header, content, footer, action (ring-1, not border)                                                                          |
| `dialog`   | `dialog.tsx`                         | static open modal (bg-card, ring-1, shadow-lg)                                                                                |
| `select`   | `select.tsx`                         | native select: default, focus, disabled + open list                                                                           |
| `sheet`    | `sheet.tsx`                          | static open left drawer with nav                                                                                              |
| `skeleton` | `skeleton.tsx`                       | avatar+lines, card block, table rows                                                                                          |
| `table`    | `table.tsx`                          | header + rows, selected row, status pills                                                                                     |
