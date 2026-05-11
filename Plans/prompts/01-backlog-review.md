# 01 — Backlog Review

Validate the 49-issue decomposition against `CLAUDE.md`, v1 scope, and constitutional invariants. Produces `docs/issues/_REVIEW.md`. **Review only — no file changes in this step.**

**Run once, before any code lands.** Skip only if you fully trust the decomposition.

## Prompt

```
Review docs/issues/ for v1 readiness. Produce a structured findings doc; do NOT modify any issue files in this step.

Read in this order:
- CLAUDE.md (constitutional invariants, anti-patterns, AI-assist boundary, "When code begins")
- Plans/canvas/10-roadmap.md §10.1 (v1 MVP scope table)
- Plans/canvas/01-vision.md §1.5 (eight replacement-grade acceptance criteria)
- docs/issues/_INDEX.md
- docs/issues/_DEPENDENCY_GRAPH.md
- All 49 docs/issues/NNN-*.md files

Run these checks and report:

A. Scope coverage — for every row in 10-roadmap §10.1, confirm ≥ 1 issue implements it. List orphans (scope item with no issue) and extras (issue with no scope mapping).

B. Constitutional invariant compliance — confirm no issue violates any of the 10 invariants in CLAUDE.md "Constitutional principles". Cite issue numbers for any violations.

C. Anti-pattern compliance — confirm no issue would ship: policy template-library theater, AI-generated audit responses without approval, proprietary collector agents, vanity trust centers, fake-continuous monitoring, per-framework duplicated controls, audit-period evidence pollution, closed proprietary connectors.

D. AI-assist boundary — confirm no issue auto-publishes audit-binding artifacts without one-click human approval.

E. Acceptance criteria quality — read these 5 in detail and score each AC set 1–5: 001 (skeleton), 002 (schema), 013 (push API), 030 (OSCAL), 010 (50 controls). Explain anything < 4. Check: integration-test-shaped (not unit-test-shaped), observable through public API, anti-criteria cite specific P0 boundaries, skill mix realistic.

F. Critical-path architectural sanity — the chain is 001 → 002 → 006 → 007 → 010 → 012 → 016 → 028 → 030 → 032 → 043. Confirm it threads the binary v1 success test (run SOC 2 audit + generate board pack from the tool). Flag any link that feels miswired.

G. HITL marker validation — confirm 007, 010, 022, 030, 035 are correctly HITL. Flag any AFK that should be HITL or vice versa.

H. Dependency graph integrity — spot-check 5 random dependencies in _DEPENDENCY_GRAPH.md. Flag orphans or missing.

I. Out-of-order risks — walk cluster Layers 1 → 7 and flag anything depending on something not yet landed.

J. Open-questions blockers — confirm each of the 5 open questions flagged in _INDEX.md is wired to the affected slice (#01 → 006; #13 → 033/034/037; #19 → 018; #17 → 014; #18 → 035).

Output — write to docs/issues/_REVIEW.md:
- Executive summary (PASS / PASS-WITH-EDITS / FAIL)
- Findings table per section A–J (severity: P0/P1/P2/info)
- Specific edits recommended (issue # · current state · suggested change)
- Net change to slice count / critical-path length / total estimate if edits applied
- Decisions you need from me before any file changes

Use Algorithm mode. Initialize a PRD (id: v1-backlog-review). Do NOT modify any docs/issues/*.md files in this step — review only.
```

## What to expect back

- A new file: `docs/issues/_REVIEW.md` with PASS / PASS-WITH-EDITS / FAIL verdict
- Findings table covering sections A–J with severity ratings
- Specific edit recommendations keyed by issue number
- An explicit list of decisions needed from you

## Follow-up prompt (only if verdict is PASS-WITH-EDITS)

```
Apply the P0 and P1 edits from docs/issues/_REVIEW.md. Update _INDEX.md and _DEPENDENCY_GRAPH.md to reflect changes. Skip P2/info edits unless I list them explicitly. Algorithm mode.
```

## Notes

- The review is purely read-only. If you see file changes happen, stop and re-read the prompt — the "do NOT modify" instruction was dropped.
- Treat the spot-checked 5 issues (001, 002, 013, 030, 010) as a sample. If their AC quality is < 4, the rest of the backlog likely needs a sweep.
- Section J catches v1 work that would silently block on an unresolved open question — especially #01 SCF licensing before slice 006.
