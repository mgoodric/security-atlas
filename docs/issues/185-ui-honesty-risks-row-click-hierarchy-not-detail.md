# 185 — UI honesty: risks row-click routes to hierarchy, not detail

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d (option A — disable click) · 1.5d (option B — ship per-risk detail page)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 178 first-pass audit, captured per AC-17. The
`/risks` page (`web/app/(authed)/risks/page.tsx`) renders every risk
row as a clickable target. The `onRowClick` handler routes to
`/risks/hierarchy?focus=<id>`. The destination DOES exist (the
hierarchy view, slice 077) — so it's not a 404 case like the audits
one (slice 184). But:

- The risks LIST table presents itself as "click row → see risk
  detail." A user reasonably expects a per-risk drill-down: title,
  description, treatment owner, inherent vs residual score, linked
  controls, history.
- The hierarchy view's `focus=<id>` query param scrolls the org-tree
  to that risk's node. It is not a risk detail page. The user gets
  a different view than the table promised.

The inline code comment is honest:

> "AC-7: Row click navigates to a per-risk detail page placeholder.
> The slice-019 backend exposes `GET /v1/risks/{id}` already; a
> dedicated per-risk detail route lives in a future slice. Today we
> route to `/risks/hierarchy?focus=<id>` so the existing hierarchy
> view at least scopes the user toward that risk without a 404."

The audit's verdict: this is a HONESTY-GAP because the affordance's
ACTUAL behavior diverges from the affordance's PROMISED behavior.
The "no 404" workaround is a band-aid, not a fix.

Same two-option shape as slice 184:

- **Option A (0.5d).** Disable the row-click affordance. Add an
  explicit "View in hierarchy" link per row + a banner "Per-risk
  detail page coming in slice #<future>."
- **Option B (1.5d).** Ship `/risks/[id]/page.tsx`. Read `GET
/v1/risks/{id}` (already exists per slice 019), render the
  fields + linked controls + history. The minimal v0 surface.

Default to Option A; the AC shape below assumes Option A.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A: chrome change.
Option B reads existing endpoint output.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** `/risks` rows are no longer clickable. The
  `onRowClick` prop is removed.
- **AC-2.** Each row carries a "View in hierarchy" link with the
  current `?focus=<id>` href so the existing workflow stays
  intact. The link is explicit; the row-as-link affordance is gone.
- **AC-3.** Banner above the table or as a row-hover hint: "Per-risk
  detail page is a future slice."
- **AC-4.** Existing Playwright spec `risks-list.spec.ts` is
  updated; the row-click navigation assertion (if any) is converted.
- **AC-5.** Slice 178's first-pass F-178-5 finding is resolved on
  the next audit run.

## Constitutional invariants honored

- **Slice 178's spillover discipline.** One slice, one discrete fix.
  The "ship the per-risk detail page" path is a separate slice if
  Option B becomes the chosen direction.
- **Anti-pattern rejected:** affordances whose ACTUAL behavior
  diverges from their PROMISED behavior.

## Canvas references

- `Plans/canvas/06-risk.md` — risk register linkage
- `docs/audit-log/178-ui-honesty-first-pass.md` — F-178-5

## Dependencies

- **#178** (UI honesty audit harness) — `in-progress`.
- **#019** (risks backend `GET /v1/risks/{id}`) — `merged`. Option B
  would consume.
- **#077** (risks-list) — `merged`. The page this slice modifies.

## Anti-criteria (P0 — block merge)

- **P0-185-1.** Does NOT ship `/risks/[id]/page.tsx` in this slice's
  Option-A path. That's Option B (separate slice if pursued).
- **P0-185-2.** Does NOT remove the hierarchy-view workflow — the
  "View in hierarchy" link preserves the existing reachability.
- **P0-185-3.** Does NOT touch the slice-178 audit harness.

## Skill mix (3-5)

1. Next.js App Router — row-click affordance refactor
2. Playwright spec update — slice-069 functional flow stays green
3. shadcn/ui Table primitives
