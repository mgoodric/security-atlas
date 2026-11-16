- **theme:** slice 203 — dark-mode stylesheet wiring. Selecting **Dark**
  in `/settings` now actually themes the page. Slice 170 wired the
  picker to persist the operator's choice + write `<html data-theme>`;
  slice 176 wired the logo to swap variants from `data-theme`. Neither
  slice wrote the `class="dark"` selector that the Tailwind v4 dark
  variant in `web/app/globals.css:5`
  (`@custom-variant dark (&:is(.dark *))`) actually keys off. Selecting
  Dark therefore swapped the logo (light-ink) but left page styles in
  light mode — light-ink logo on white background rendered invisible
  (the user-reported failure on the most recent release). Slice 203
  extends `choose()` in the picker to add/remove `class="dark"` on
  `<html>` alongside the existing attribute write, adds an inline
  early-paint `<script>` in `web/app/layout.tsx` `<head>` to apply the
  class BEFORE React hydrates (prevents the flash-of-light-mode on page
  load), and mounts `<ThemeClassSync>` under `<Providers>` to react to
  OS `prefers-color-scheme` changes without a reload. The slice-170
  "Dark-mode stylesheet pending" banner in `/settings` is retired.
  `data-theme` remains the source-of-truth (P0-A1); the class is in
  addition. No new dependency. Hardcoded-color violations in
  board-pack / exceptions / control coverage components surfaced during
  the visual sanity-check are filed as follow-on slices per the
  spillover discipline; the picker swatches (literal Light/Dark color
  previews) are intentionally left hardcoded.
