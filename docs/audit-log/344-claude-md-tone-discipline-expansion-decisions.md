# Slice 344 — CLAUDE.md tone-discipline expansion — decisions log

**Slice:** 344
**Type:** JUDGMENT
**Date:** 2026-05-29
**Parent:** 337 (AI-writing tone audit)

This slice carries the three additions to `CLAUDE.md` that the slice 337 audit
(`docs/audits/337-ai-writing-auditor-report.md`, "Candidate additions to CLAUDE.md
tone discipline") proposed but explicitly did not apply (per P0-337-6). Per the
JUDGMENT slice convention, the wording calls were made here rather than blocked on
a human sign-off; the maintainer iterates post-merge.

---

## D1 — Repetition discipline lands as a sub-bullet, folding points 1 + 2 into one rule

The slice doc proposed three audit candidates, two of which ("first-pass" overuse,
"load-bearing" over-use) are the same underlying pattern: a canonical project term
losing signal through repetition. Rather than add two phrase-specific entries to the
banned-phrase list — which would imply they are banned, which they are not — I added a
single "Repetition discipline" sub-bullet that states the general rule and names the
project jargon as canonical-but-watch-for-overuse. This follows the slice doc's
suggested wording, with one adjustment: I appended "and a synonym or a more specific
phrasing usually reads cleaner" to give the rule an actionable remedy rather than only
a flag. The 3+-in-one-document threshold matches the audit's own observation (it flagged
"load-bearing" at 3× in a single decisions log and 3× in a slice-doc subsection).

## D2 — "harness" exception documented as a sub-paragraph, not a list entry

Adding "harness" to the banned-phrase list would be backwards — the point is that the
project uses it literally and should NOT flag it. So the AC-2 content lands as a
"Project-specific exceptions" sub-paragraph that frames the persona's Tier 2 list as
supplementary and this project's usage as canonical. I kept the slice doc's suggested
wording and added the parenthetical "(Playwright)" to make the slice-178 reference
concrete and unambiguous, matching how the audit itself described it ("a Playwright
test harness (slice 178)").

## D3 — Placement: both tone additions stay inside the existing tone-discipline section

P0-344-5 forbids introducing a new top-level section. The "Repetition discipline"
sub-bullet was appended directly after the existing banned-phrase list's closing line
("Full updated list maintained at slice 182's tone-anti-pattern reference document.")
so it sits visually with the list it complements. The "Project-specific exceptions"
sub-paragraph follows it, before the "Schema-level extensions" paragraph. Both stay
inside the "Tone discipline (banned phrases in the system prompt)" block — no new
heading, no restructuring.

## D4 — Forward note placement: after "Implementation timing", inside the AI-assist boundary

The AC-3 forward note belongs next to the existing "Implementation timing" line, which
already establishes that board-narrative v0 is v2+ work. Placing the note there keeps it
adjacent to the timing context the v2 implementer will read. I kept the slice doc's
suggested wording and added a one-clause clarification that `internal/board/narrative.go`
is "a pure `text/template` renderer with no LLM call site" — this is the audit's own
verified finding (audit §AC-5) and tells the v2 implementer exactly what surface does not
yet exist, so they do not search for a non-existent enforcement hook.

## D5 — Additions only; banned-phrase list and constitutional boundary untouched

Per P0-344-1 and P0-344-3, the existing eight-item banned-phrase list is unchanged, and
no runtime LLM-prompt code is touched. The forward note documents the forward expectation
only — slice 182's v2 continuation owns the enforcement surface. The constitutional
AI-assist boundary text ("This boundary governs the product at runtime...") is unmodified.

## D6 — Decisions-log filename follows the slice doc's canonical name

The orchestrator brief referenced `344-claude-md-tone-discipline-decisions.md`; the slice
doc itself specifies `344-claude-md-tone-discipline-expansion-decisions.md`. The slice doc
is the system of record for the slice's own artifacts, so this log uses the slice-doc
filename. Noted here so the discrepancy is visible rather than silently resolved.

## D7 — No own banned phrases introduced

The additions were checked against the project's banned-phrase list and the persona's
Tier 1 vocabulary. No "robust" filler, no "leverage" verb, no unprompted superlative,
no "best-in-class" / "world-class" / "industry-leading". The voice is measured and
factual, matching the constitutional discipline the slice extends.

---

## Anti-criteria verification

- **P0-344-1** (no shrinking the banned-phrase list): honored — additions only.
- **P0-344-2** (no file other than CLAUDE.md changed by the substantive edit): the
  substantive change is CLAUDE.md only; CHANGELOG + this decisions log + the slice-doc
  status row are the standard slice-bookkeeping companions (status row left to the
  orchestrator per the batch policy).
- **P0-344-3** (no runtime LLM-prompt code changed): honored — forward note documents
  the expectation only.
- **P0-344-4** (no auto-merge): honored — maintainer review required on the
  constitutional document; PR opened, not merged.
- **P0-344-5** (no new CLAUDE.md section): honored — all three additions are sub-points
  inside existing sections.
