# 255 — Control-detail header action buttons + "last evaluated" timestamp

**Cluster:** Frontend / UI parity
**Estimate:** 0.5d
**Type:** JUDGMENT

## Narrative

Surfaced during slice 204's per-page audit of `/controls/{id}` (see
`docs/audit-log/204-page-audit-control.md`, Finding 3). The mockup
(`Plans/mockups/control.html` lines 92-102) places three action buttons
and a "last evaluated" timestamp in the top-right of the control header:

- `Run query` button
- `Edit YAML` button
- `Request exception` button
- "last evaluated `8 minutes ago`" timestamp below the buttons

The live page (`web/app/(authed)/controls/[id]/page.tsx`) has none. The
header has the control title + meta only, no right-aligned action well.

**Why this matters:** these are the page's three primary mutating-or-
investigative actions. Their absence on the most important per-control
page is a discoverability problem. The mockup signals the design
intent of the page — operator opens a control, sees its posture, then
clicks one of three workflows (run the rule, edit the YAML, request an
exception). Without the buttons that mental model is invisible.

**Caveats** (each button maps to a canvas surface that may not be on
main yet):

- `Run query` → control-as-code execution (canvas §4.5 — rule-DSL /
  control evaluation). Likely v2+.
- `Edit YAML` → control-text editor. Likely v2+.
- `Request exception` → exception-request workflow (canvas §4.6).
  Some surfaces exist on main (the `/exceptions` family); the
  per-control "request" affordance may need a new BFF.

The "last evaluated" timestamp is **trivially wireable today** — the
freshness-clock component in the right rail already renders
`state.last_observed_at`. Mirroring it next to the header is a
copy-paste.

This is the audit's hardest finding for severity assignment. The
buttons map to v2+ features; the timestamp maps to today's data.
The JUDGMENT choice for the implementing engineer is the spectrum
between "honest placeholder" and "ship the timestamp now, file
followups for the buttons".

## Threat model

**Verdict.** **no-mitigations-needed-for-timestamp.** The "last
evaluated" timestamp binds to existing read-only `/v1/controls/{id}/state`
data; no new surface. **For the three buttons**, the threat model is
**deferred-until-implementation** — each button surfaces a canvas
section (§4.5, §4.6) whose implementation slice would do its own
threat model. This slice ships either disabled-with-explanation
buttons OR labelled placeholders; neither adds an auth surface.

## Acceptance criteria

- **AC-1.** "Last evaluated `<relative-time>`" sub-line renders in
  the top-right of the control header, below the action-button row
  (matches mockup lines 98-101). Source: `state.last_observed_at`
  from `GET /v1/controls/{id}/state`. Renders `—` if state is
  unavailable; renders "never" if state has no `last_observed_at`.
- **AC-2.** Three buttons render in the top-right of the header
  (matches mockup lines 93-97), in mockup order: Run query · Edit
  YAML · Request exception. Style: shadcn `<Button variant="outline" size="sm">`.
- **AC-3.** Each button's behavior on click is JUDGMENT (see
  below). At minimum: visible affordance, no console error, no
  silent 404 (do not file `<a href="#">` — that pattern is the
  slice 178 dead-link anti-pattern).
- **AC-4.** If the JUDGMENT call is to render buttons as
  "v2 placeholder" affordances, each button shows a tooltip on
  hover/focus that names the canvas section and the v2 status,
  matching the slice 183 / slice 184 pattern (no link;
  explanatory tooltip; no `<a href="#">`).
- **AC-5.** Vitest covers the relative-time rendering for
  AC-1 (8 minutes ago, 1 day ago, never, —).
- **AC-6.** Playwright covers tab order + button focus
  visibility — the three buttons are keyboard-reachable in DOM
  order.

## Constitutional invariants honored

- **UI-honesty anti-pattern.** The buttons either work, or they
  visibly carry "not yet" semantics that match the slice 183 dead-
  link family resolution. No `<a href="#">`.
- **Canvas §4.5 / §4.6 — surfaces named where they belong.** The
  buttons label real canvas concepts; their existence (even as
  placeholders) is the page making a promise that downstream
  slices will keep. The slice 183 resolution made this kind of
  placeholder honest as long as it carries explanatory tooltip /
  no-link semantics.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5 — control-as-code
  (the Run query / Edit YAML surfaces)
- `Plans/canvas/04-evidence-engine.md` §4.6 — exception workflow
  (the Request exception surface)
- `docs/audit-log/204-page-audit-control.md` Finding 3

## Dependencies

- **#204** (UI parity audit) — parent.
- The control-as-code rule-DSL slice family — when filed/built,
  the Run query and Edit YAML buttons activate. This slice ships
  the buttons as placeholders; later slices wire them.

## Anti-criteria (P0 — block merge)

- **P0-255-1.** Does NOT ship a working "Run query" execution
  path. That's a multi-slice rule-DSL feature.
- **P0-255-2.** Does NOT ship a working "Edit YAML" editor.
- **P0-255-3.** Does NOT ship `<a href="#">` for any of the three
  buttons. Either disabled-button + tooltip OR linked-to-a-real-
  destination (Request exception → `/exceptions/new?control_id=…`
  if that route exists; otherwise treat as placeholder).
- **P0-255-4.** Does NOT add a new API endpoint. This slice is
  UI surfacing only.

## JUDGMENT notes (for the implementing engineer)

- **D1.** The shape of the placeholder. Options:
  (a) `<Button disabled>` with `<Tooltip>` explaining "rule
  execution lands in slice family NNN — coming in v2".
  (b) `<Button>` linking to a `/controls/[id]/run` or
  `/controls/[id]/yaml` route that renders a friendly "coming
  in v2" page (analogue of the slice 152 empty-state pattern).
  Engineer picks one; (a) is cheaper and is the slice-183
  resolution analog.
- **D2.** Whether `Request exception` links to the existing
  `/exceptions` page (which exists on main) prefilled with
  `?control_id=<id>`, or behaves as a placeholder. If the
  exception-request route accepts a control_id query param, wire
  it; otherwise placeholder. Record decision in
  `docs/audit-log/255-header-actions-decisions.md`.
- **D3.** Relative-time formatter — reuse the existing
  freshness-clock formatter from
  `web/components/control/freshness-clock.tsx` if one exists,
  otherwise add one in `web/lib/relative-time.ts`. Record
  decision.

## Skill mix (3-5)

1. shadcn/ui Button + Tooltip composition.
2. Next.js relative-time rendering (server-rendered vs
   client-rendered choice for the "8 minutes ago" timestamp;
   client-rendered to avoid SSR/hydration time-drift surprises).
3. UI-honesty discipline — the slice 183 placeholder pattern.
4. JUDGMENT-slice decisions log.
