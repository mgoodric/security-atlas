# 342 — Vision canvas tone rewrite (banned phrase + em-dash saturation)

**Cluster:** Docs
**Estimate:** 0.5d
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)
**Parent:** 337 (AI-writing tone audit)

## Narrative

Surfaced during slice 337 audit, captured as follow-up per continuous-batch policy.

`Plans/canvas/01-vision.md` was the only high-density file in the bounded audit surface (≥3 findings of substance). The findings:

| Severity | Line  | Issue                                                                                         |
| -------- | ----- | --------------------------------------------------------------------------------------------- |
| High     | 21    | CLAUDE.md banned-phrase hit — "best-in-class" describing SimpleRisk                           |
| Medium   | whole | Em-dash saturation — 22 em-dashes in 1461 words (15 per 1000; persona hard max is 1 per 1000) |
| Low      | 16    | Single Tier 3 compound — "engineering-hostile" (acceptable; flag only if reused)              |

The high-severity hit is structural: CLAUDE.md explicitly bans "best-in-class" anywhere in repo prose, and the canvas vision section is the project's positioning artifact — the same text a customer evaluator reads first.

## What ships in this slice

1. Rewrite line 21 to remove "best-in-class" — replace with a specific, falsifiable claim about SimpleRisk's scope.
2. Triage the 22 em-dashes against the persona's hard max (1 per 1000 words ≈ 1-2 in this file). Most should become commas, periods, or parentheticals; keep the small set that are genuinely load-bearing.
3. No other content changes. Tone audit only; do not modify the structural argument of the section.

## Acceptance criteria

- [ ] **AC-1.** Line 21 no longer contains "best-in-class". Replacement is a specific claim ("SimpleRisk — narrow scope, well-regarded risk register, supports import").
- [ ] **AC-2.** Em-dash count in `Plans/canvas/01-vision.md` is ≤ 2 (per persona hard max for ~1500 words).
- [ ] **AC-3.** No other prose changes — the diff is bounded to tone fixes.
- [ ] **AC-4.** No factual or positioning claim is altered.
- [ ] **AC-5.** `pre-commit run --files Plans/canvas/01-vision.md` passes.

## Constitutional invariants honored

- **AI-assist boundary (CLAUDE.md).** The tone discipline is part of this boundary.
- **Survive third-party security review (canvas §6).** Public-facing positioning artifact stays measured and factual.

## Canvas references

- `Plans/canvas/01-vision.md` (the file being rewritten)
- `CLAUDE.md` "Tone discipline (banned phrases in the system prompt)" — the gate

## Dependencies

- **#337** (AI-writing tone audit) — parent; produces the audit report this slice acts on.

## Anti-criteria (P0 — block merge)

- **P0-342-1.** Does NOT change the section's structural argument or positioning.
- **P0-342-2.** Does NOT add new claims to the file. Tone rewrite only.
- **P0-342-3.** Does NOT touch other canvas sections or files. Scope = `Plans/canvas/01-vision.md` only.
- **P0-342-4.** Does NOT remove em-dashes that are genuinely load-bearing parentheticals — apply persona judgment, not a blanket find-and-replace.
- **P0-342-5.** Does NOT auto-merge.

## Skill mix

- Standard read/edit
- `voltagent-qa-sec:ai-writing-auditor` persona reference for em-dash triage

## Notes for the implementing agent

The audit report at `docs/audits/337-ai-writing-auditor-report.md` lists the specific findings. The high-severity item is the load-bearing fix; the em-dash work is the bulk of the wall-clock.

**Em-dash triage rule of thumb (per the persona):**

- Em-dash inside a bullet introducing a definition → keep
- Em-dash mid-sentence joining two independent clauses → replace with period
- Em-dash as a stylistic flourish ("the answer is X — and here's why") → replace with comma or period

**Audit log filename (if findings surface during execution):**
`docs/audit-log/342-vision-canvas-tone-rewrite-decisions.md`
