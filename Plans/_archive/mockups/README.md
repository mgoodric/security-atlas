# Archived iteration-1 UI mockups

These are the **iteration-1 UI mockups** — hand-written, self-contained HTML
files (Tailwind via CDN, no build step) that shaped the early security-atlas UI
and seeded the production `shadcn/ui` frontend.

They have been moved here from `Plans/mockups/` (slice 437) because they are no
longer the active design surface. They remain in the repo as a **historical
reference** — they were moved, not deleted, and their git history is preserved
through the `git mv`.

## `web/` is canonical

The production frontend lives at `web/` and is the single source of truth for
what the UI actually is. These mockups predate large parts of `web/` and have
drifted from it on a per-page basis by design — `web/` shipped, iterated, and
moved on; the mockups were frozen at iteration 1.

## Per-page divergence is NOT fileable drift

This is the convention this archive exists to make plain: **a divergence between
one of these mockups and the corresponding `web/` page is expected and is NOT
fileable as drift.** A stale mockup is the normal, intended state — not a bug
and not a product gap.

For most of v1 the mockups sat in the active `Plans/` tree, so every mockup-vs-
`web/` difference read as drift worth filing — slices 216, 220, 231, 245, 258,
259 and others in the slice-204 parity-audit family were all "mockup stale vs
production" findings that cost triage time only to confirm "the mockup is just
old." Relocating the mockups out of the active design tree removes that recurring
false-drift source. Before filing a UI-drift finding, check whether the only
discrepancy is "the iteration-1 mockup shows something `web/` no longer does (or
never did)" — if so, there is nothing to file.

(This does not retroactively close the already-filed mockup-drift slices; those
are reconciled separately. This archive stops _new_ ones from being filed.)

## What's here

| File                 | Mockup                  |
| -------------------- | ----------------------- |
| `index.html`         | Mockup index / launcher |
| `dashboard.html`     | Program dashboard       |
| `controls.html`      | Controls list           |
| `control.html`       | Control detail          |
| `evidence.html`      | Evidence list           |
| `risks.html`         | Risk register list      |
| `policies.html`      | Policies list           |
| `audits.html`        | Audit periods list      |
| `board-pack.html`    | Quarterly board pack    |
| `questionnaire.html` | Security questionnaire  |
| `settings.html`      | User settings           |
| `_shared/shell.css`  | Shared chrome styles    |

To view: open any `.html` file in a browser (no build step).
