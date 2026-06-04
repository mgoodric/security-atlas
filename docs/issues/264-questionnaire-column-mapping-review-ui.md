# 264 — MOCKUP-STALE: questionnaire Excel column-mapping review UI

**Cluster:** Frontend
**Estimate:** 1.0d (single page surface; reuses existing shadcn/ui Table primitives)
**Type:** AFK
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** slice 204 (per-page UI parity audit) — finding category iv (MOCKUP-STALE)

## Narrative

Surfaced during slice 204 per-page audit of `Plans/mockups/questionnaire.html`
(audit log: `docs/audit-log/204-page-audit-questionnaire.md`).

**The gap.** The mockup includes a "Stage B" surface (lines 113-175 of
`Plans/mockups/questionnaire.html`) — a post-upload review step where the
operator confirms detected Excel column → schema-field mapping before parsed
rows persist. The mockup labels it "Review parsed rows · acme-caiq-v4-1.xlsx"
and shows a table of source-column / mapped-field / sample-value with
"Confirm & import" and "Cancel" buttons.

Slice 155 explicitly deferred this UI in decisions log D3:

> "**D3 — Excel column mapping: header-row heuristic, manual override deferred.**
> Decision. Auto-detect via header-row heuristic. Manual column-mapping UI
> step DEFERRED to a spillover slice. ... A proper in-UI mapping step
> (drag-drop column → field) is real UX work — better landed as its own slice
> once we have operator feedback on the heuristic miss rate."

So the mockup promises a UI that slice 155 explicitly chose NOT to ship.
This is a MOCKUP-STALE finding per the slice 204 audit taxonomy: text /
visual in the mockup that references a feature whose implementation does
not (and currently should not, per scope-lock) exist.

**Why this is its own slice (not folded into slice 263).** Slice 263 ships
the questionnaire frontend page consuming the slice 155 backend as-is —
the upload flow goes straight from "drop xlsx" to "questions are persisted
via auto-detect." That keeps slice 263 within scope of what the backend
supports today. Stage B is additive: it requires either (a) a new backend
route that returns "parsed but unpersisted" rows + accepts a column-mapping
patch + then persists, or (b) a client-side parse step that posts the
final mapping. Either is real backend or client design work and belongs
behind operator feedback, per slice 155 D3.

**Path forward (maintainer triages priority).** Two options when picked up:

- **Option A — drop Stage B from the mockup.** If after slice 263 ships,
  the heuristic miss rate is low (operators rarely see `unmapped_columns`
  in production usage), close this finding by editing
  `Plans/mockups/questionnaire.html` to remove Stage B and add a comment
  noting the deferral was honored. No frontend or backend work.
- **Option B — build Stage B.** If operator feedback shows the heuristic
  misses frequently (e.g., custom vendor questionnaires with non-standard
  headers), build the Stage B UI + a small backend extension to support
  the unpersisted-parse pattern.

Defaulting to Option A in the AC stub below — the cheap chrome change
that closes the audit finding without speculative UX work. Maintainer
flips to Option B if the field signal warrants.

## Threat model

| STRIDE                | Threat                                                                                                                                                                                               | Mitigation                                                                                                                                                                                                   |
| --------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **S** Spoofing        | None — chrome-only change in Option A. Option B reuses existing auth path.                                                                                                                           | n/a (A) / existing patterns (B)                                                                                                                                                                              |
| **T** Tampering       | Option B: a parsed-but-unpersisted parse step could be abused (operator submits a manipulated mapping that maps Notes-column → answer_text, leaking internal data into the persisted question text). | Option-B AC: server-side validation that field mappings only allow source-columns explicitly tagged as text-bearing, and that the mapping reuses slice 155's known field set (no free-form field invention). |
| **R** Repudiation     | n/a                                                                                                                                                                                                  | n/a                                                                                                                                                                                                          |
| **I** Info disclosure | Option B: parsed rows sit in memory or temp storage. If the operator abandons the mapping step, the temp data should expire.                                                                         | Option-B AC: 15-minute TTL on staged-parse state; explicit "Cancel" route deletes it.                                                                                                                        |
| **D** DoS             | Option B: large xlsx uploads sit in staged-parse storage longer than today.                                                                                                                          | Slice 155's 5MB cap covers it; expire-on-cancel + TTL prevents pile-up.                                                                                                                                      |
| **E** EoP             | n/a                                                                                                                                                                                                  | n/a                                                                                                                                                                                                          |

**Verdict (Option A).** no-mitigations-needed (chrome change).
**Verdict (Option B).** mitigations enumerated above must land with the slice.

## Acceptance criteria (Option A — chosen path)

- [ ] **AC-1.** `Plans/mockups/questionnaire.html` Stage B block
      (lines 113-175 of current file) is removed.
- [ ] **AC-2.** A top-of-file comment in `Plans/mockups/questionnaire.html`
      documents the removal: "Stage B (post-import column-mapping review UI)
      was removed 2026-05-NN per slice 264 — slice 155 D3 deferred the manual
      column-mapping step. The header-row heuristic auto-detects today;
      re-add Stage B if operator feedback shows heuristic miss rate is high."
- [ ] **AC-3.** Slice 263's stub AC-3 (which explicitly carves Stage B
      out of slice 263's scope) is no longer needed — slice 263's acceptance
      shape is unchanged by this slice.
- [ ] **AC-4.** Slice 204's first-pass aggregate
      (`docs/audit-log/204-page-audit-questionnaire.md`) shows the MOCKUP-STALE
      finding marked resolved by this slice in any subsequent audit run.

## Acceptance criteria (Option B — if picked instead)

(Defer detail until field signal warrants the path. Skeleton:)

- AC-B1. New route `POST /v1/questionnaires/{id}/import-excel:stage`
  parses + stages without persisting; returns parsed rows + detected
  mapping + `unmapped_columns`.
- AC-B2. New route `POST /v1/questionnaires/{id}/import-excel:confirm`
  takes a final mapping + persists.
- AC-B3. Stage B UI consumes both; cancel → DELETE stage state.
- AC-B4. 15-minute TTL on staged-parse rows.
- AC-B5. Server-side validation that field-mapping only assigns to
  known slice-155 field set.

## Constitutional invariants honored

- **AI-assist boundary (hard).** This slice ships ZERO AI-assist. The
  Option-A path is purely chrome; the Option-B path is a deterministic
  parse + persist flow.
- **Anti-pattern rejected (CLAUDE.md).** UI affordances that promise
  features the platform does not ship. This slice closes one such gap.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — questionnaire shape
- `docs/audit-log/155-questionnaire-tracer-decisions.md` D3 — the
  scope-lock decision this slice honors

## Dependencies

- **#155** (questionnaire backend tracer) — `merged` at `12da637`.
  Establishes the D3 deferral that this slice resolves.
- **#263** (questionnaire frontend page) — `ready`. If 263 lands
  before 264 reaches `in-progress`, 263's stub AC-3 explicitly omits
  Stage B; if 264 lands first (Option A), the mockup is updated and
  263's AC-3 becomes a no-op.

## Anti-criteria (P0 — block merge)

- **P0-264-1.** Does NOT ship the Stage B UI on Option A path.
  Option A is mockup-edit-only.
- **P0-264-2.** Does NOT modify slice 155's backend routes on
  Option A path.
- **P0-264-3.** Does NOT speculate operator feedback. The
  Option-A-vs-B choice waits for real signal from slice 263's
  shipped page.

## Notes for the implementing agent

This is a tiny slice on Option A path (one HTML file edit + one
comment + one status flip). The value is purely audit-honesty:
closing the MOCKUP-STALE finding so future audits don't re-surface
it. Option B is real work — defer.

Provenance: filed 2026-05-23 from slice 204 per-page UI parity audit
of `Plans/mockups/questionnaire.html`. Audit log:
`docs/audit-log/204-page-audit-questionnaire.md`.
