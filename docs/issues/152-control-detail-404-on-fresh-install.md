# 152 — Control detail 404 on fresh install (seed SOC 2 kit OR friendly empty-state)

**Cluster:** Backend / Frontend
**Estimate:** 0.5d
**Type:** JUDGMENT (D1: seed-on-bootstrap vs friendly-404)
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0:

> "Loading an individual control I get 'Could not load control · 404 Not Found'"

On a fresh install, the controls table is empty. The `/v1/controls/{id}` endpoint correctly returns 404 (the control doesn't exist), but the UX is operator-hostile — you sign in, navigate to controls, click the first one (or arrive via a stale link), and see a generic 404 page.

**Two design choices:**

**(D1-a) Seed the SOC 2 stock control kit (slice 010) on first install.** Bootstrap path (slice 141 territory) ingests the slice-010 kit so the controls table has ~50 SOC 2 controls by default. Operator immediately sees data + can navigate.

**(D1-b) Ship a friendly empty-state on the controls list page.** When 0 controls exist, the list page renders "No controls yet — import a framework or use the SOC 2 starter kit" CTA. Control detail page never gets reached on a truly empty install. Less magic; more explicit.

Maintainer lean: (a) — vCISO operator persona wants to land + start working immediately. (b) is more explicit but adds friction.

**What this slice ships:**

- D1 chosen; recorded in decisions log.
- If (a): bootstrap path (slice 141) ingests `slice-010` stock control kit; ~50 SOC 2 controls present after first OIDC sign-in. Coordinate with slice 141 — same atomic transaction or follow-up?
- If (b): controls list page renders empty-state CTA; control detail 404 page redesigned with helpful copy + back-to-list link.
- Either way: control detail 404 page (when the detail truly is missing) renders friendly copy instead of generic "404 Not Found".

## Acceptance criteria

- [ ] AC-1: D1 decision recorded in `docs/audit-log/152-control-detail-decisions.md`.
- [ ] AC-2: If (a): bootstrap path seeds SOC 2 kit; integration test asserts ~50 controls present after fresh install.
- [ ] AC-3: If (b): controls list page renders empty-state CTA; vitest covers the empty-state render.
- [ ] AC-4: Control detail 404 page: friendly copy ("This control wasn't found. It may have been deleted or you may not have access. Return to controls list."), back-to-list link.
- [ ] AC-5: Playwright e2e: fresh install → controls list renders (with seeds or empty-state) → click first control → detail loads (option a) OR no detail to click (option b empty-state with CTA only).
- [ ] AC-6: CHANGELOG entry: "Control detail no longer hard-404s on fresh install (#152)".

## Dependencies

- **#010** SOC 2 control kit (merged) — source of the stock controls.
- **#141** Multi-tenant login (`ready`, in flight as PR #303) — bootstrap path; option (a) extends it.
- **#012** Control state evaluation (merged) — runs after controls exist.

## Anti-criteria (P0 — block merge)

- **P0-CTL-1** Operator MUST NOT see generic 404 on a fresh-install navigation. Either there's data (option a) OR there's a friendly empty-state (option b).
- **P0-CTL-2** D1 documented in decisions log; engineer doesn't ship without recording the choice.
- **P0-CTL-3** NO scope creep into reshaping the controls list UI.

## Notes for the implementing agent

D1 is a real call. Pickup engineer should re-grill against current canvas + recent slice ordering (slice 141 may already extend bootstrap to do more than just create tenant + super_admin; if it includes scope-cell seeds, this slice's option (a) is a natural extension).

Provenance: filed 2026-05-18 from operator v1.10.0 report.
