# 459 — Sweep code-comment `Plans/mockups/` provenance citations to the archived path

**Cluster:** Infra
**Estimate:** S
**Type:** JUDGMENT
**Status:** `ready` (no dependency)

> Surfaced during slice 437 (archive `Plans/mockups/` iteration-1 HTML out of the
> active tree).

## Narrative

Slice 437 moved the iteration-1 mockups from `Plans/mockups/` to
`Plans/_archive/mockups/` via `git mv`, updated every **instructional /
functional** pointer (CLAUDE.md, ARCHITECTURE_CANVAS, the `web/e2e-audit`
harness `mockupsDir()` resolver + its emitted strings + schema, the active
canvas docs, `web/docs/responsive-discipline.md`, and the moved `index.html`
self-references), and **consciously left** the point-in-time historical
references per the slice's AC-4 conscious-leave clause:

- `CHANGELOG.md` (immutable historical record)
- `docs/audit-log/**`, `docs/audits/**`, `docs/issues/**` (dated slice/audit
  records — point-in-time facts)
- `docs/design/logo-candidates/**` notes (design provenance)
- ~50 inline code comments under `web/` and `internal/` of the form
  `// per Plans/mockups/control.html lines 139-152` — design-provenance
  citations recording where the design reference lived when the code was
  written.

The inline code-comment citations are now technically stale paths (the file is
at `Plans/_archive/mockups/...`). They are not _functional_ references (nothing
resolves them at runtime — `mockupsDir()` is the only functional resolver and
slice 437 already repointed it), so they do not break anything. But a developer
chasing a `// per Plans/mockups/board-pack.html` comment from `internal/board/`
will hit a dead path until they realize the move happened.

This slice is the optional, mechanical follow-up: sweep the **code-comment**
citations (the `web/**/*.{ts,tsx}` + `internal/**/*.go` comments) from
`Plans/mockups/` to `Plans/_archive/mockups/` so the provenance links resolve.
It deliberately does NOT touch `CHANGELOG.md` or the dated `docs/` records —
those stay verbatim as historical fact (consistent with slice 437's boundary).

## Scope discipline

- Comment-only edits — no behavioral change, no test-assertion change.
- `web/**` and `internal/**` source comments only.
- Does NOT touch `CHANGELOG.md`, `docs/audit-log/**`, `docs/audits/**`,
  `docs/issues/**`, or `docs/design/**` (point-in-time records).
- Does NOT touch `docs/issues/_STATUS.md` / `_INDEX.md`.

## Threat model

STRIDE N/A across the board — comment-only edits, no runtime code, no auth
surface, no data path. The only risk is a typo'd path in a comment (Tampering,
trivial). **Verdict: CLEAN.**

## Acceptance criteria

- [ ] **AC-1.** Inline `Plans/mockups/` references in `web/**/*.{ts,tsx}` and
      `internal/**/*.go` comments are updated to `Plans/_archive/mockups/`.
- [ ] **AC-2.** No behavioral or test-assertion change — comment text only.
- [ ] **AC-3.** `CHANGELOG.md` and the dated `docs/` records are NOT modified.
- [ ] **AC-4.** No `web/` production behavior changes (comment-only diff in any
      production file).

## Dependencies

- Slice 437 (the move) — merged first.

## Notes for the implementing agent

This is a low-value, high-churn cleanup; pick it up only if comment-path
navigability becomes a real friction. The functional surface is already correct
after slice 437 — this is polish, not a fix.
