# 184 — UI honesty: audits row-click 404 (per-period detail placeholder)

**Cluster:** Quality / UI hygiene
**Estimate:** 0.5d (option A — disable click) · 2.0d (option B — ship detail page)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 178 first-pass audit, captured per AC-17. The
`/audits` page (`web/app/(authed)/audits/page.tsx`) renders every
audit-period row as a clickable target. The `onRowClick` handler
calls `router.push(/audits/${id})`. **The destination route does not
exist.** Clicking any row 404s on Next.js's standard not-found UI.

The slice author was deliberate about this — the inline code comment
acknowledges:

> "the route is a placeholder — the per-period detail page is a
> future slice. Today this routes to /audits/{id} which 404s with
> the standard Next.js not-found UI; that is the correct placeholder
> behavior per the slice text ('placeholder OR drawer')."

That is honest internal documentation but a poor user experience: a
solo security leader running their first SOC 2 audit-period in this
tool will click on the table row expecting to see audit-period
detail, get bounced to a 404, and lose confidence.

The fix has two paths:

- **Option A (0.5d).** Disable the row-click affordance. Render rows
  as non-clickable. Add a banner above the table: "Per-period detail
  is a future slice." This is the slice-178 first-pass's
  recommendation — close the HONESTY-GAP cheaply, ship the detail
  page on its own timeline.

- **Option B (2.0d).** Ship a minimal per-period detail page. It
  reads `GET /v1/audit-periods/:id` (which DOES exist — slice 048),
  renders the period's metadata + frozen status + sample counts,
  and that's the v0. Heavier surface; bigger slice.

The maintainer picks A or B at start. Defaulting to A in the AC
shape below — Option A is the smaller, lower-risk slice that closes
the audit finding; Option B is filable as a follow-on once the
detail-page UX is decided.

## Threat model

**Verdict.** **no-mitigations-needed.** Option A is purely a chrome
change. Option B reads existing data via the existing endpoint.

## Acceptance criteria (Option A — chosen path)

- **AC-1.** `/audits` rows are no longer clickable. The
  `onRowClick` prop on `ListTable` is either removed or replaced
  with a no-op handler.
- **AC-2.** A banner / disclosure above the table reads "Per-period
  detail is a future slice — see issue #<NNN> to track."
- **AC-3.** Existing Playwright spec `audits-list.spec.ts` is
  updated: the row-click assertion (if any) is removed or
  converted to assert no-navigation.
- **AC-4.** Slice 178's first-pass F-178-4 finding is resolved on
  the next audit run; the audit's sticky PR comment for this
  PR shows zero new HONESTY-GAP findings on `/audits`.

## Constitutional invariants honored

- **Invariant 9 (manual evidence is first-class).** Audit periods
  manage manual evidence flows; this slice does not regress them.
- **Slice 178's spillover discipline.** One slice per discrete fix.
  The "ship the detail page" option is a separate slice if Option B
  becomes the path.
- **Anti-pattern rejected:** UI affordances that 404 on click.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` — auditor role, audit-period
  freezing
- `docs/audit-log/178-ui-honesty-first-pass.md` — F-178-4

## Dependencies

- **#178** (UI honesty audit harness) — `in-progress`. Surfacing
  parent; not a build-time prerequisite.
- **#048** (audit-periods backend) — `merged`. The endpoint Option
  B would consume; for Option A, no runtime dependency.

## Anti-criteria (P0 — block merge)

- **P0-184-1.** Does NOT ship a `/audits/[id]/page.tsx` in this
  slice's Option-A path. Option B (if chosen) ships exactly that
  one page + nothing else.
- **P0-184-2.** Does NOT modify the audits list-page data contract
  or backend.
- **P0-184-3.** Does NOT touch the slice-178 audit harness — the
  detection is fine; this slice is the corrective.

## Skill mix (3-5)

1. Next.js App Router — disabling a row-click affordance
2. Playwright spec update — keeping the slice-069 functional flow
   green
3. shadcn/ui Table primitives
