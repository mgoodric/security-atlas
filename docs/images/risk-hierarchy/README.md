# Risk hierarchy dashboard — screenshots

This directory holds the AC-10 screenshots of the `/risks/hierarchy`
view (slice 056). Slice 057 (README screenshots) consumes them.

## Status

The PNGs are **pending a live instance**. Capturing them requires a
running `web` dev server pointed at a running platform with a seeded
tenant — the same live-backend constraint slice 040 hit. This worktree
has no running platform; the capture procedure below is committed so
the images can be produced against a seeded instance (slice 057 is the
natural place to run it). See
`docs/audit-log/056-hierarchical-risk-dashboard-decisions.md` decision 5.

## Capture procedure

### Seed preconditions

The capture tenant must have:

- at least two `org_units` in a parent/child relationship (so the tree
  has structure to expand/collapse)
- the 10 default themes (slice 053 seed) — optionally one tenant-private
  theme to show the default-left ordering
- at least one **active** aggregation rule targeting a theme (so the
  heatmap cell-hover tooltip cites real thresholds)
- at least one decision with a future `revisit_by`
- at least one decision whose `revisit_by` is in the past (so the amber
  "Revisit overdue" pill renders)

### Steps

1. Start the platform and the `web` dev server; sign in as a credential
   in the seeded tenant.
2. Set the browser viewport to **1440×900**.
3. Navigate to `/risks/hierarchy`.
4. Capture in **light** theme:
   - `risk-hierarchy-light.png` — the full three-panel view
   - `risk-hierarchy-heatmap-cell-light.png` — with a heatmap cell
     clicked (side panel open)
5. Toggle to **dark** theme; capture:
   - `risk-hierarchy-dark.png` — the full three-panel view
   - `risk-hierarchy-heatmap-cell-dark.png` — with a heatmap cell
     clicked (side panel open)

The `engineering-advanced-skills:full-page-screenshot` skill handles the
tall-page / scroll-container capture if the timeline column overflows
the viewport.

## File manifest (to be added)

| File                                    | Theme | Content                      |
| --------------------------------------- | ----- | ---------------------------- |
| `risk-hierarchy-light.png`              | light | full three-panel view        |
| `risk-hierarchy-dark.png`               | dark  | full three-panel view        |
| `risk-hierarchy-heatmap-cell-light.png` | light | heatmap cell side panel open |
| `risk-hierarchy-heatmap-cell-dark.png`  | dark  | heatmap cell side panel open |
