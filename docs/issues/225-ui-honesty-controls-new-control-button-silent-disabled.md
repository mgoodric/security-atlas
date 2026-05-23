# 225 — UI honesty: "New control" button on /controls is silently disabled

**Cluster:** Quality / UI hygiene (frontend)
**Estimate:** 0.5d (option A — explanatory affordance) · 3d (option B — ship the create-control flow)
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (controls page), captured as
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/controls.html` (lines 121-124) shows a primary-styled
enabled "New control" button. The live `/controls` page
(`web/app/(authed)/controls/page.tsx` line 335) renders the button
with `disabled` at the DOM level:

```tsx
<Button size="sm" disabled>
  New control
</Button>
```

The button retains its primary visual weight (`bg-primary
text-primary-foreground`) but does nothing on click. There is no
tooltip, no adjacent banner, no "coming in slice #N" hint. A user
landing on `/controls` reasonably expects the button to work.

This is the same HONESTY-GAP class slice 178's audit heuristic
defined: an affordance whose ACTUAL behavior diverges from its
PROMISED behavior. Slices 184 and 185 fixed analogous patterns
(audits row-click 404, risks row-click hierarchy-not-detail) by
making the affordance honest about what it does — disabled with an
explanation, or removed entirely.

The slice ships option A by default — make the disabled state
honest. Option B (build the create-control flow) is a substantive
feature; if the maintainer picks B instead, this slice becomes its
spec and the estimate climbs to ~3d.

**Why option A.** Creating a tenant control is a non-trivial flow:
the user picks an SCF anchor, defines `applicability_expr`,
selects framework satisfactions, optionally attaches a control
policy. Slice 100 deferred this surface entirely. Making the
button honest (tooltip + banner) costs 0.5d and removes the lie.

## Threat model

**Verdict.** `no-mitigations-needed`. The slice is a chrome change
for option A. Option B's threat model would surface a substantive
mutation surface and live in its own slice spec.

## Acceptance criteria (option A — chosen path)

- **AC-1.** "New control" button on `/controls` carries a tooltip
  (shadcn/ui `<TooltipProvider>` + `<Tooltip>`) reading:
  `Create-control flow lands in a future slice. For now, controls
are instantiated by the SCF importer or by the atlas CLI.`
- **AC-2.** A `data-testid="controls-new-control-disabled-reason"`
  attribute lands on the tooltip content, used by the Playwright
  spec to assert the explanation renders.
- **AC-3.** OR (alternative presentation): an info-banner above
  the table with the same text, accompanied by an "Open SCF
  catalog" link routing to `/catalog/scf` (which exists on main per
  slice 058) so the user has a positive next step.
- **AC-4.** Existing Playwright spec for `/controls`
  (`web/e2e/controls-list.spec.ts` if it exists; otherwise add one)
  asserts the tooltip / banner is reachable via keyboard focus on
  the button.
- **AC-5.** Slice 204 audit fleet's next run reports no honesty-gap
  on the `/controls` "New control" button.

## Constitutional invariants honored

- **Slice 178's spillover discipline.** One slice, one discrete
  fix. The create-control flow is a separate slice if the
  maintainer chooses option B.
- **Anti-pattern rejected:** affordances whose ACTUAL behavior
  diverges from their PROMISED behavior. The button being honest
  about "this lands in a future slice" is the fix.

## Canvas references

- `Plans/canvas/02-primitives.md` §2 — Control primitive
- `Plans/canvas/01-vision.md` §1.6 — UI-honesty anti-patterns
- `docs/audit-log/204-page-audit-controls.md` — parent audit

## Dependencies

- **#204** (UI parity audit fleet) — parent.
- **#100** (controls list page) — merged. The page this slice
  modifies.
- **#058** (mkdocs Material docs site) — merged. The "Open SCF
  catalog" link target on AC-3 routes to a real surface.

## Anti-criteria (P0 — block merge)

- **P0-225-1.** Does NOT ship the create-control flow in this
  slice's option-A path. That's option B (separate slice if
  pursued).
- **P0-225-2.** Does NOT remove the button entirely without an
  explanation surface — the user needs to understand what to do
  instead (the tooltip / banner is the positive next step).
- **P0-225-3.** Does NOT touch the slice 204 audit harness.

## Skill mix (3)

1. Next.js App Router + shadcn/ui Tooltip — tooltip wiring.
2. Playwright spec update — slice 069 functional flow stays green.
3. UI copy authorship — tooltip text discipline (no marketing-y
   tone; per CLAUDE.md "ban list").
