# 342 — vision canvas tone rewrite: decisions log

**Slice:** 342 (JUDGMENT)
**Date:** 2026-05-29
**File rewritten:** `Plans/canvas/01-vision.md`
**Parent audit:** `docs/audits/337-ai-writing-auditor-report.md`

This is a JUDGMENT-type slice: the build-time tone calls are made here and recorded,
not gated on a human sign-off. The product runtime AI-assist boundary is unaffected.

---

## D1 — Banned-phrase fix (AC-1, High severity)

**Before (line 21):** `- **SimpleRisk** — best-in-class risk register, narrow scope. We support import.`
**After:** `- **SimpleRisk**: narrow scope, well-regarded risk register, supports import.`

- "best-in-class" is on the CLAUDE.md banned list. The audit flagged it even though
  the praise is directed at a third party — the ban list applies to all repo prose.
- Replacement is specific and falsifiable: SimpleRisk genuinely has a narrow scope (risk
  register only), is well-regarded in that scope, and security-atlas supports importing
  from it. No new positioning claim is introduced; the original sentence already made all
  three of these claims, only "best-in-class" was an unfalsifiable superlative.
- "well-regarded" was checked against the ban list: it is not a banned superlative (it is
  attributable/falsifiable, not an unprompted superlative like "world-class").
- Confidence: high.

## D2 — Em-dash triage (AC-2, Medium severity)

Persona hard max ≈ 1 per 1000 words. File is 1443 words, so the budget is ~1-2 em-dashes.
Started at 22 true em-dashes (U+2014); en-dashes (U+2013) in number ranges like "50–150"
and "30–80" are NOT em-dashes and were left untouched.

Triage applied the persona rule of thumb:

| Line                    | Original use                   | Decision | Replacement                                                              |
| ----------------------- | ------------------------------ | -------- | ------------------------------------------------------------------------ |
| 9 (thesis)              | flourish before "instead of"   | replace  | comma                                                                    |
| 13 (incumbents)         | paired parenthetical           | replace  | parentheses; folded "(lock-in)" into "as lock-in" to avoid double parens |
| 15 (renewal cliff)      | flourish joining two clauses   | replace  | period                                                                   |
| 19 (eramba)             | definition after bold term     | replace  | colon (term-definition separator)                                        |
| 20 (OpenGRC)            | two mid-list clause joins      | replace  | comma + "where" clause                                                   |
| 21 (SimpleRisk)         | definition after bold term     | replace  | colon (also D1 banned-phrase line)                                       |
| 22 (CISO Assistant)     | definition after bold term     | replace  | colon                                                                    |
| 38 (primary persona)    | paired appositive interruption | replace  | parentheses; un-nested "(prospect-driven)" to "prospect-driven"          |
| 40 (generic persona)    | flourish                       | replace  | period                                                                   |
| 52 (setup filter)       | definition gloss               | replace  | colon                                                                    |
| 54 (vendor filter)      | clause join                    | replace  | comma                                                                    |
| 56 (manual controls)    | clause join                    | replace  | period                                                                   |
| 58 (why this persona)   | flourish                       | replace  | comma + "because"                                                        |
| 68 (AC-5)               | clause join                    | replace  | comma                                                                    |
| 60 (§1.5 heading)       | heading term-gloss             | **KEEP** | load-bearing — see below                                                 |
| 70 (AC-7)               | clause join                    | replace  | semicolon                                                                |
| 71 (AC-8)               | flourish                       | replace  | comma                                                                    |
| 83 (anti-pattern table) | flourish after quoted question | replace  | "and"                                                                    |

Final em-dash count: **1** (0.7 per 1000 words). AC-2 satisfied (≤ 2).

### The one em-dash kept as load-bearing

Line 60: `## 1.5 "Replacement-grade" — measurable acceptance criteria`

- This is a section heading where the em-dash separates a defined quoted term
  ("Replacement-grade") from its gloss ("measurable acceptance criteria"). This is the
  canonical "title — subtitle" construction; a colon would also work but the em-dash is
  the conventional, cleanest reading in a heading and is genuinely a definition separator,
  not a stylistic flourish or a clause-join.
- Keeping exactly one (under the 1-per-1000 budget for a 1443-word file) honors P0-342-4:
  do not blanket find-and-replace; keep the genuinely load-bearing one.
- Confidence: high.

### Notes on specific replacements

- Line 13: the original had a dash-pair parenthetical AND a separate "(lock-in)" parenthetical
  in the same sentence. Converting the dash-pair to parentheses would have produced an awkward
  double-parenthetical, so "(lock-in)" was folded inline as "as lock-in". This preserves the
  exact claim (these commitments are defensible because they create lock-in) with no meaning
  change. Confidence: high.
- Line 38: the appositive list was inside a bolded persona definition. Converting the dash-pair
  to parentheses required un-nesting the inner "(prospect-driven)" to avoid nested parens; it
  now reads "ISO 27001 prospect-driven". Same claim, no nesting. Confidence: high.
- All other replacements are mechanical comma/period/colon/semicolon swaps with no claim change.

## D3 — Scope discipline (AC-3, AC-4, P0-342-1/2/3)

- No content added or removed beyond tone. Diff is 17 lines changed, line-for-line.
- The structural argument (why-not-incumbents, OSS prior art, non-goals, personas,
  replacement-grade criteria, anti-patterns) is byte-for-byte intact except for punctuation
  and the single banned-phrase substitution.
- No other file in the repo was touched (P0-342-3). The README and 03-ucf.md em-dash findings
  belong to slice 343 (tone polish round 1), already filed by the parent audit; not touched here.
- `docs/issues/_STATUS.md` and `_INDEX.md` were NOT touched (orchestrator-owned; slice 382 CI guard).

## D4 — Verification

- Full CLAUDE.md ban-list grep on the final file: 0 hits
  ("best-in-class", "world-class", "industry-leading", "robust", "leverage" as verb,
  "we are proud to report", "exceeded expectations").
- Em-dash count (U+2014): 1.
- `pre-commit run --files Plans/canvas/01-vision.md CHANGELOG.md ...` run before push (AC-5).

## Spillover

None. The other em-dash findings (README, 03-ucf.md) were already captured by the parent audit
as slice 343. No new out-of-scope tone issue surfaced during this rewrite.
