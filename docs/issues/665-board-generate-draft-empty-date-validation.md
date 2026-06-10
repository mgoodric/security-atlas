# 665 — Board pack "Generate draft" gives no feedback when quarter-end date is empty

**Cluster:** Board packs
**Estimate:** XS-S (<0.5d)
**Type:** AFK
**Status:** `ready` — surfaced by the 2026-06-10 empty-tenant UI audit (ATLAS-015).

## Narrative

On `/board-packs`, clicking **"Generate draft"** with an empty quarter-end date produces
**no visible validation message or toast** — the action silently no-ops, leaving the
operator unsure whether it failed or is processing. Re-verified on `main` build `2a3805b`.

## Threat model

No security surface — form validation feedback only.

## Acceptance criteria

- [ ] **AC-1.** Submitting "Generate draft" with an empty (or invalid) quarter-end date
      shows clear inline validation / a toast prompting for the date; no silent no-op.
- [ ] **AC-2.** The submit control is disabled (or the form blocks) until a valid date is
      present, consistent with the rest of the app's form-validation pattern.
- [ ] **AC-3.** A valid date still generates the draft (no regression); Playwright asserts
      both the empty-date feedback and the happy path.

## Anti-criteria

- Does NOT change board-pack generation semantics — only the client-side validation/feedback.

## Dependencies

- The board-packs "Generate quarterly pack" form (`web/app/(authed)/board-packs`).

## Notes

Source: 2026-06-10 empty-tenant browser audit, item **ATLAS-015** (priority low /
severity minor). Re-tested open on build `2a3805b`.
