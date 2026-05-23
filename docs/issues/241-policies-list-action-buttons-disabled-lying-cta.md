# 241 — Policies list: "Acknowledgment report" + "New policy" buttons render disabled (lying CTAs)

**Cluster:** policies (UI honesty)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `ready`
**Parent:** #204 (UI parity audit fleet — `/policies` page)

## Narrative

Surfaced by the slice 204 audit of `/policies` against
`Plans/mockups/policies.html` (see
`docs/audit-log/204-page-audit-policies.md`).

The production page at `web/app/(authed)/policies/page.tsx`
(lines 326–332) renders two action buttons in the right-rail
header position:

```tsx
<Button variant="outline" size="sm" disabled>
  Acknowledgment report
</Button>
<Button size="sm" disabled>
  New policy
</Button>
```

Both are unconditionally `disabled`. No tooltip explains why. No
linked-issue indicator says "coming in slice X". The mockup
(`Plans/mockups/policies.html` lines 116–121) shows both buttons as
**active, primary affordances** — the user sees a button-shaped
control that looks clickable and does nothing.

This is the slice-178 honesty-gap class: forward-looking-UI
affordances that promise capability the platform does not yet
deliver. Slice 101's `P0-A4` notes the constraint for `New policy`
("NO policy-create UI bundled") but the disabled-without-tooltip
treatment lies to the user about why the button exists.

The fix is **not** to ship the wizard or the report (those are
larger slices); the fix is to make the disabled state honest:
either (a) remove the buttons entirely until backing slices land,
or (b) keep them with a tooltip explaining the gap + an open-issue
link.

## Threat model

**Verdict.** **no-mitigations-needed.** UI-only honesty fix; no
new auth surface, no new data path. The buttons are presently
inert; the fix makes the inertness honest.

## Acceptance criteria

- **AC-1.** Both `Acknowledgment report` and `New policy` buttons
  are EITHER removed from the action area OR rendered with a
  tooltip that explains the gap and links to the tracking slice
  (suggested copy: "Coming in slice <NNN>. The report / wizard
  hasn't shipped yet.").
- **AC-2.** If the tooltip path is chosen, the tooltip uses the
  shadcn `Tooltip` primitive and the trigger is keyboard-
  accessible (tab focus + Enter shows the tooltip).
- **AC-3.** Decisions log entry at
  `docs/audit-log/241-policies-disabled-cta-decisions.md`:
  (D1) remove-vs-tooltip path chosen + rationale,
  (D2) the tracking slice numbers cited (if tooltip path),
  (D3) any other disabled-CTA patterns in the codebase that this
  decision should be applied to going forward.
- **AC-4.** Unit test (or Playwright spec) asserts the buttons
  are EITHER absent (remove path) OR present + carry a non-empty
  tooltip text (tooltip path).
- **AC-5.** The Export CSV/JSON/XLSX trio (slice 138) stays
  unchanged — those are active, working affordances and out of
  scope for this slice.
- **AC-6.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Anti-pattern explicitly rejected.** "Vanity trust centers" —
  buttons that look clickable but do nothing are exactly the
  anti-pattern. This slice converts the inert affordance into
  either honest absence or honest disclosure.
- **AI-assist boundary.** No AI-generated content touched.

## Canvas references

- `Plans/canvas/01-vision.md` §1.6 — UI honesty anti-pattern
- `docs/audit-log/178-ui-honesty-first-pass.md` — same honesty-gap
  class
- `Plans/mockups/policies.html` lines 116–121 — mockup shows active
  buttons (drives the audit comparison)

## Dependencies

- **#204** (audit parent) — `in-progress`.
- **#101** (policies list view, where the disabled buttons are
  declared) — merged.
- **#138** (policies CSV/JSON/XLSX export) — merged. NOT affected
  by this slice; the export buttons remain active.

## Anti-criteria (P0 — block merge)

- **P0-241-1.** Does NOT ship a policy-create wizard. That's a
  separate, larger slice.
- **P0-241-2.** Does NOT ship an acknowledgment-report
  generation surface. That's a separate, larger slice.
- **P0-241-3.** Does NOT remove the Export CSV/JSON/XLSX buttons
  (slice 138). Out of scope.
- **P0-241-4.** Does NOT use vendor-prefixed test fixture tokens.

## Skill mix

1. shadcn/ui `Tooltip` primitive (if tooltip path chosen).
2. Decision-logging discipline — small slice, real choice.
3. Slice 178 honesty-gap class — reuse classification language.
