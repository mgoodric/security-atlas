# 264 — Decisions log

Slice: `264 — MOCKUP-STALE: questionnaire Excel column-mapping review UI`
Type: AFK (chrome-only mockup edit; no production code)
Date: 2026-05-23
PR: pending (this slice)

The slice spec offered two paths and explicitly defaulted to Option A. This
log records the executing engineer's confirmation of that default and the
specific edits applied.

---

## Decisions made

### D1 — Choose Option A (drop Stage B from the mockup), not Option B (build it)

**Options considered.**

| Path                         | Shape                                                                                                       | Effort                     | Risk                                                                                                      |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------- | -------------------------- | --------------------------------------------------------------------------------------------------------- |
| A — drop Stage B from mockup | Edit `Plans/mockups/questionnaire.html`; add audit-honesty comment.                                         | ~20 min, 2 files           | None — closes a stale-mockup finding.                                                                     |
| B — build Stage B            | New `:stage` + `:confirm` backend routes, staged-parse TTL, server-side mapping validation, new UI surface. | Days of backend + UX work. | Speculative — slice 155 D3 explicitly chose to wait for operator feedback before building manual mapping. |

**Chosen path.** Option A.

**Rationale.**

1. **The spec defaults to Option A.** The slice 264 spec ("Path forward")
   defaults to Option A and explicitly conditions Option B on field signal
   ("If operator feedback shows the heuristic misses frequently…"). Slice
   263 — the questionnaire frontend page that would generate that signal
   — is `ready` but has not shipped. There is no operator data yet to
   flip the decision.
2. **Slice 155 D3 is load-bearing.** Slice 155's decisions log explicitly
   defers manual column-mapping UI. Building Stage B now would re-open a
   resolved decision without new evidence — exactly the failure mode the
   `JUDGMENT`-slice convention is meant to surface and avoid.
3. **CLAUDE.md anti-pattern.** "UI affordances that promise features the
   platform does not ship" is an enumerated anti-pattern (CLAUDE.md
   constitutional principles). Stage B in the mockup violates this; the
   cheapest remediation is to remove it.
4. **Scope-lock honored.** The slice's P0 anti-criteria forbid shipping
   Stage B UI on the Option A path; Option A keeps this slice tightly
   scoped to a chrome change and avoids re-opening slice 155 D3.

**Confidence.** `high`.

### D2 — Replace the Stage B `<div>` block with an HTML comment placeholder (not silent deletion)

**Options considered.**

- (a) Delete the Stage B section entirely (lines 113-175), leaving no trace
  in the file.
- (b) Replace with an HTML comment that documents what was removed and why,
  pointing at this decisions log.

**Chosen path.** (b) — comment placeholder.

**Rationale.** Future contributors reading the mockup will see the existing
`STAGE A → STAGE C` numbering and may wonder what happened to Stage B. A
short HTML comment with a date, a slice number, and a pointer to this
decisions log resolves that confusion without re-introducing the unshipped
UI surface. Cost is ~8 lines of comment in a file that already has a
detailed scope-lock comment at the top — symmetric, idiomatic.

**Confidence.** `high`.

### D3 — Also add a top-of-file "Mockup edit history" stanza inside the existing scope-lock comment

**Rationale.** The top-of-file comment block already documents slice 155's
scope-lock decisions. Appending a dated edit-history entry there gives a
single canonical place to read the mockup's evolution, separate from the
inline placeholder at the former Stage B location. This satisfies AC-2's
"top-of-file comment in `Plans/mockups/questionnaire.html` documents the
removal" requirement explicitly.

**Confidence.** `high`.

### D4 — Fix the stale Stage A → Stage B transition reference

**Context.** Stage A's section comment said "After upload, transitions to
Stage B." Once Stage B is removed, that sentence is itself a MOCKUP-STALE
artifact pointing at a non-existent surface.

**Chosen path.** Rewrote the Stage A comment to read: "After upload, parsed
rows persist via header-row heuristic and the operator lands directly on
Stage C." This describes what slice 155 actually ships today.

**Why this is in-scope for slice 264.** AC-2 asks for a top-of-file
removal comment, but a literal reading would leave the Stage A → Stage B
reference dangling. Fixing the immediate dangling reference at the same
time as the removal is the only honest way to close the audit finding;
otherwise the next per-page audit pass would re-surface the dangling
phrase as a new MOCKUP-STALE finding. Tight, scoped, no UI changes.

**Confidence.** `high`.

---

## Revisit once in use

Tracked items the maintainer should re-evaluate once slice 263 has shipped
and operators have used the questionnaire upload flow against real
non-canonical vendor xlsx files.

1. **Heuristic miss rate.** After slice 263 ships, watch the
   `unmapped_columns` count returned by the import path across N real
   uploads. If unmapped-column counts on customer-supplied xlsx files
   exceed (rough threshold) ~10% of total uploads, that's the field
   signal that warrants opening Option B as a fresh slice. Track via
   slice 263's import-result telemetry once available.

2. **Pattern of misses.** If misses cluster on a specific xlsx shape
   (e.g., HECVAT v3 with multilingual headers, custom vendor templates
   with merged-header rows), Option B's design should narrow to those
   shapes rather than build a general drag-drop mapper — i.e., the
   answer may be "extend the heuristic," not "ship Stage B UI."

3. **Operator workaround behavior.** If operators are repeatedly
   pre-processing xlsx files outside the platform to make them
   heuristic-friendly, that's a strong Option B signal — same as
   high miss rate, different surface. Surface via operator interviews
   on the v2-feedback cadence.

4. **Re-audit of the mockup.** Slice 204's per-page UI parity audit
   should re-run after this slice merges to confirm the MOCKUP-STALE
   finding for questionnaire.html no longer surfaces. The aggregate at
   `docs/audit-log/204-page-audit-questionnaire.md` is the cite (AC-4).

---

## Confidence summary

| Decision                                    | Confidence |
| ------------------------------------------- | ---------- |
| D1 — choose Option A                        | high       |
| D2 — comment placeholder vs silent deletion | high       |
| D3 — top-of-file edit-history stanza        | high       |
| D4 — fix Stage A comment dangling reference | high       |

No `low`-confidence decisions in this slice. All four calls follow
explicit prior decisions (slice 155 D3, slice 264 spec default, CLAUDE.md
anti-pattern enumeration) — there is no novel design surface here, just
honest chrome maintenance.

---

## Files touched

- `Plans/mockups/questionnaire.html` — Stage B `<div>` block (former lines
  113-175) replaced with an 8-line HTML comment placeholder; top-of-file
  scope-lock comment extended with a "Mockup edit history" stanza; Stage A
  section comment rewritten to remove the dangling "transitions to Stage
  B" reference.
- `docs/audit-log/264-decisions.md` — this file.

No production code touched. No `_STATUS.md` / `CHANGELOG.md` updates per
slice anti-criteria.
