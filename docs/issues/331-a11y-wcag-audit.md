# 331 — Accessibility audit (WCAG 2.1 AA) via voltagent-qa-sec:accessibility-tester

**Cluster:** Frontend
**Estimate:** 1.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

## Narrative

Runs `voltagent-qa-sec:accessibility-tester` against the Next.js
frontend at `web/` to surface WCAG 2.1 AA conformance gaps. The
front end has grown to ~30+ pages across dashboard / control detail
/ audit workspace / risk hierarchy / admin surfaces / atlas-edge
admin / login + tenant-switch flows. The UI is the primary operator
surface; accessibility gaps directly affect the v1 user (a
security-leader who may have temporary impairment — eye strain,
keyboard-only sessions, screen-reader use for review-while-walking).

**Audit surface.** WCAG 2.1 AA pass across:

- **Keyboard navigation.** Every action available via mouse must be
  reachable + activatable via keyboard. Tab order matches visual
  order. Focus traps work for dialogs + drawers + sheets. Skip links
  for the sidebar + page chrome.
- **Screen-reader semantics.** Heading hierarchy (one h1 per page,
  no skipped levels). Landmarks (header, nav, main, footer). ARIA
  roles + labels where shadcn/ui primitives don't supply them.
  Live regions for dynamic content (per slice 322's aria-live fix).
  Form labels associated with controls.
- **Focus management.** Visible focus indicator on every interactive
  element (not relying on browser default). Focus restoration after
  dialog close. Focus trap inside modal dialogs. No focus-stealing
  on scroll.
- **Color contrast.** Text + non-text contrast against the page
  background — both light and dark themes (slice 203 wiring).
  Specific attention to button-disabled states, link-vs-text, and
  status-pill colors (badge / chip variants).
- **Motion sensitivity.** `prefers-reduced-motion` respected for
  any animation > 100ms (transitions, accordions, drawer slides).
  Animation never carries information that's not also in static UI.
- **Resize + zoom.** UI usable at 200% browser zoom + at 320px
  viewport width (mobile baseline per slice 277).
- **Form errors.** Validation errors associated with their fields
  via `aria-describedby` + announced via `aria-live`. Errors
  reachable via keyboard from form submit.
- **Time-based content.** Auto-refresh / polling can be paused or
  extended. No content with > 5s timeout without user warning.

**Why now:** the frontend has crossed the size where ad-hoc a11y
spot-fixes can't keep up. Slice 322 just shipped an aria-live fix
that hints at broader gaps. Slice 277's mobile-responsive work +
slice 203's dark-mode work changed the visual surface enough that a
systematic re-audit is warranted before the v1 binary test (operator
demos including accessibility-conscious orgs).

**Trigger:** Surfaced 2026-05-27 during the agent-driven audit-planning
session — audit slice 5/12.

**Disposition:** read-only audit + follow-up-slice fan-out.

## Threat model

A11y-audit-only slice. STRIDE pass:

- **S (Spoofing):** No auth surface. CLEAN.
- **T (Tampering):** Read-only.
- **R (Repudiation):** Findings logged in
  `docs/audit-log/331-a11y-wcag-audit-decisions.md`.
- **I (Information disclosure):** Audit may screenshot UI for
  evidence. Demo seed only — slice 205 dataset.
- **D (Denial of service):** CLEAN.
- **E (Elevation of privilege):** Dev-level access.

## Acceptance criteria

- [ ] **AC-1.** The `voltagent-qa-sec:accessibility-tester` agent
      runs against the eight WCAG 2.1 AA surfaces in the narrative
      across every top-level page in `web/app/**`.
- [ ] **AC-2.** Findings recorded in
      `docs/audit-log/331-a11y-wcag-audit-decisions.md` with: WCAG
      criterion (e.g. "1.4.3 Contrast (Minimum)") · severity · page
      affected · component affected · one-line disposition.
- [ ] **AC-3.** Critical findings (page is unusable for a
      keyboard-only user OR a screen-reader user) fan out as
      individual `/idea-to-slice` follow-up slices.
- [ ] **AC-4.** High findings (a specific action is unreachable but
      the page is otherwise navigable) fan out as individual slices.
- [ ] **AC-5.** Medium findings (cosmetic-but-noticeable: contrast
      borderline, focus indicator low-visibility) bundled into a
      single "a11y polish round 1" slice OR per-component slices —
      engineer's call.
- [ ] **AC-6.** The audit specifically visits both light AND dark
      themes (slice 203 wiring) — contrast findings tagged with
      which theme is affected.
- [ ] **AC-7.** The audit specifically visits the 320px viewport
      (mobile baseline per slice 277) — touch-target findings
      tagged accordingly.
- [ ] **AC-8.** Cross-references slice 178's UI honesty audit
      harness — if the harness can be extended to enforce a11y
      contracts in CI, file a follow-up slice for the extension.
- [ ] **AC-9.** No code modified. Diff = doc files only.
- [ ] **AC-10.** `pre-commit run --files` passes.

## Constitutional invariants honored

- **Survive third-party security review (canvas §6) — extended.** A
  third-party reviewer running automated a11y scanners (axe-core,
  WAVE) on the live demo is part of any modern diligence pass; a11y
  passes are now table stakes.
- **Manual evidence is first-class (canvas §4.5).** The accessibility
  audit IS a manual-evidence artifact for the platform's own
  compliance posture.

## Canvas references

- `Plans/canvas/09-tech-stack.md` — frontend stack (Next.js 16 +
  shadcn/ui + Tailwind 4)
- `Plans/canvas/01-vision.md` §6 — survive third-party review

## Dependencies

- **#178** (UI honesty audit harness) — `merged`. Provides the
  Playwright extension surface.
- **#203** (dark-mode wiring) — `merged`. Theme switching is in
  scope for contrast findings.
- **#277** (mobile-responsive baseline) — `merged`. 320px viewport is
  in scope.

## Anti-criteria (P0 — block merge)

- **P0-331-1.** Does NOT bundle Critical or High findings into one
  follow-up slice. Tracer-bullet per finding.
- **P0-331-2.** Does NOT auto-merge.
- **P0-331-3.** Does NOT modify code.
- **P0-331-4.** Does NOT operate on production tenant data — demo
  seed only.
- **P0-331-5.** Does NOT include screenshots with PII / customer
  data in the decisions log.
- [ ] **P0-331-6.** Does NOT replace the UI honesty audit harness
      (slice 178). If the audit recommends a11y assertions in the
      harness, file as a follow-up slice.
- **P0-331-7.** Does NOT touch CLAUDE.md, canvas.

## Skill mix

- `voltagent-qa-sec:accessibility-tester` — the named audit agent
- `/idea-to-slice` — for follow-ups
- Browser dev-tools + axe-core / WAVE for in-browser scans
- Playwright (existing `web/e2e-audit/` harness) for any
  reproducibility runs

## Notes for the implementing agent

**Page enumeration suggestion (in scope; visit each):**

- `/` (root → /dashboard redirect)
- `/dashboard` (the hero surface)
- `/controls`, `/controls/[id]` (list + detail; ~50 SOC 2 controls
  via demo seed)
- `/risks`, `/risks/[id]` (hierarchy + detail)
- `/audit-workspace`
- `/board/preview`, `/board/[id]`
- `/evidence`, `/evidence/[id]`
- `/vendors`, `/vendors/[id]`
- `/policies`, `/policies/[id]`
- `/calendar`
- `/admin/*` (tenants, super-admins, demo, exceptions)
- `/login`, `/tenant-switch` (auth flows)

Spot-check rather than exhaustive: visit one of each
template-pattern (list / detail / dashboard / form) and one
edge-case page (e.g. /admin/demo for its dialog patterns).

**Themes + viewports to visit:**

- Light theme + 1440 viewport (desktop default)
- Dark theme + 1440 viewport
- Light theme + 320px viewport
- Dark theme + 320px viewport

**Cross-reference protocol.** A11y findings overlap with slice 178's
UI honesty work in spirit. Note "candidate harness extension: <text>"
in the decisions log where applicable. The maintainer decides
whether to file a harness-extension slice or absorb the finding.

**Audit log filename:**
`docs/audit-log/331-a11y-wcag-audit-decisions.md`
