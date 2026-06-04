# 437 — Archive `Plans/mockups/` iteration-1 HTML out of the active tree

**Cluster:** Infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready` (no dependency)

## Narrative

`Plans/mockups/` holds the iteration-1 UI mockups — 11 hand-written HTML files
(~292K) built with Tailwind via CDN, no build step. They served their purpose:
they shaped the early UI and seeded the shadcn/ui frontend. But the production
frontend now lives at `web/` and is canonical — CLAUDE.md "Working norms"
already says "treat the mockups as reference, not production code."

The problem is that the mockups sit in the _active_ `Plans/` tree, so every
divergence between a mockup and the shipped `web/` page reads as drift worth
filing. Slices 216, 220, 231, 245, 258, 259 (and others in the 204 parity-audit
family) are all "mockup stale vs production" findings — the mockups have become
a recurring **false-drift source** now that `web/` is the source of truth. Each
such finding costs triage time to confirm it is "mockup is just old," not a
real product gap.

This slice moves the mockups to an archive location (`Plans/_archive/mockups/`)
with a `README` pointer explaining their iteration-1 status and that `web/` is
canonical, updates the CLAUDE.md "Mockups" references to point at the new
location (or note the archive), and establishes the convention that per-page
mockup-vs-production divergence is **no longer fileable drift**. The mockups
remain in the repo as historical reference — they are moved, not deleted.

**Scope discipline.** This is a `git mv` + a README + a docs-pointer update. It
does NOT delete the mockups (they stay as reference). It does NOT touch any
`web/` production code. It does NOT retroactively close the already-filed
mockup-drift slices (216/220/231/etc.) — those are tracked separately; this
slice only stops _new_ ones from being filed by relocating the false-drift
source and documenting the convention.

## Threat model

STRIDE pass for a file-move-plus-docs slice. No runtime code, no auth surface,
no tenant data path — the runtime STRIDE categories are N/A. The only real
risks are Tampering (a broken link after the move) and the meta-risk of a
mockup containing something that shouldn't be archived-but-still-tracked.

**S — Spoofing.** N/A — no endpoints, no identities.

**T — Tampering (the only live category).** Moving the files breaks any link
that points at the old `Plans/mockups/` path — a docs-integrity regression.
Mitigation: AC-4 greps the tree for `Plans/mockups` references and updates
every one (CLAUDE.md has at least five: the reading-order entry, the repo-layout
diagram, the working-norms note, and the quick-references line). The move is
`git mv` (history-preserving), not delete+add.

**R — Repudiation.** N/A — no audit-logged operation.

**I — Information disclosure.** Worth a beat: the mockups are static HTML with
no secrets and no tenant data (they predate any real backend), so the move
discloses nothing. Confirm no mockup file embeds a real credential or token
before archiving (it won't — they are CDN-Tailwind static pages — but the
check is cheap; AC-5).

**D — Denial of service.** N/A.

**E — Elevation of privilege.** N/A — no role check, no privileged path.

**Verdict:** CLEAN — a history-preserving move plus a link sweep. The single
guard is AC-4 (no dangling `Plans/mockups` reference after the move); the
secrets spot-check (AC-5) is belt-and-suspenders.

## Acceptance criteria

- [ ] **AC-1.** All `Plans/mockups/*.html` files are moved (via `git mv`,
      history-preserving) to `Plans/_archive/mockups/` — no mockup file
      deleted.
- [ ] **AC-2.** A `Plans/_archive/mockups/README.md` is added explaining: these
      are iteration-1 reference mockups; `web/` is the canonical production
      frontend; per-page divergence between a mockup and `web/` is expected and
      is NOT fileable drift.
- [ ] **AC-3.** The CLAUDE.md "Mockups" reference (reading-order item 4) and the
      "Working norms" mockup note are updated to point at the archived location
      (or to note the archive), so the canonical doc no longer steers readers to
      the old path.
- [ ] **AC-4.** `rg "Plans/mockups"` across the tree returns no dangling
      reference — every pointer (CLAUDE.md repo-layout diagram, quick-references
      line, any doc/slice cross-link that should follow the move) is updated or
      consciously left (with the conscious-leave cases noted, e.g. historical
      slice docs that cite the old path as a point-in-time fact).
- [ ] **AC-5.** A spot-check confirms no archived mockup file embeds a real
      credential/token (they are static CDN-Tailwind pages; the check is a
      grep for obvious secret patterns).
- [ ] **AC-6.** No `web/` production code is modified.
- [ ] **AC-7.** The convention "mockup-vs-`web/` per-page divergence is not
      fileable drift" is recorded where future triagers will see it (the
      archive README + a one-line note in the CLAUDE.md working-norms mockup
      entry).

## Constitutional invariants honored

- No architecture invariant is touched — file move + docs only; no schema,
  auth, tenancy, or RLS surface.
- Honors the CLAUDE.md "Working norms — Editing Plans/ vs editing code" rule
  that `web/` is the production frontend and mockups are reference; this slice
  makes that already-stated rule structurally true by getting the reference
  out of the active design tree.
- Honors "Ask before destructive operations" — this is explicitly a non-
  destructive move (`git mv`, not delete).
- Style: no emojis; markdown README.

## Canvas references

- CLAUDE.md "Read the canvas (reading order)" item 4 + "Working norms" item 3 —
  the two places that currently point at `Plans/mockups/` and treat it as
  reference; this slice relocates the target and updates both.

## Dependencies

- None.

## Anti-criteria (P0 — block merge)

- Does NOT delete any mockup file — `git mv` to the archive, history preserved.
- Does NOT touch any `web/` production code.
- Does NOT retroactively close the already-filed mockup-drift slices
  (216/220/231/245/258/259/etc.) — those are reconciled separately; this slice
  stops NEW false-drift filings.
- Does NOT leave a dangling `Plans/mockups` reference in CLAUDE.md or other
  active docs (AC-4 is the integrity guard).
- Does NOT rewrite historical slice docs that cite the old path as a point-in-
  time fact (those are an accurate record of where the file was then).

## Skill mix (3-5)

- `git-worktree-manager` / git — history-preserving `git mv`.
- `monorepo-navigator` — sweep the tree for `Plans/mockups` references.
- `simplify` — keep the README tight.
- `ship-gate` — confirm no dangling reference + no `web/` touch.

## Notes for the implementing agent

`git mv` (not `mv` + `git add`) so the mockups' history follows them — they are
a historical record and the blame/log lineage is worth keeping. The reference
sweep is the load-bearing step: CLAUDE.md alone has the mockups path in at
least four spots (reading-order item 4, the repo-layout ASCII diagram, working-
norms item 3, and the quick-references line) — update the _instructional_ ones
(reading order, working norms, quick references) to the new path; the repo-
layout diagram is a planned-layout illustration and can show the archive
location too. Historical slice docs under `docs/issues/` that mention the old
path are point-in-time facts — leave those (AC-4's "consciously left" clause).
The README's most useful sentence is the convention statement: future triage
should know on sight that a stale mockup is expected, not a bug — that is the
recurring cost this slice exists to kill.
