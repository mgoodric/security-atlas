# 337 — AI-writing tone auditor report

**Slice:** 337
**Date:** 2026-05-27
**Auditor:** `voltagent-qa-sec:ai-writing-auditor` persona (instance run)
**Scope:** read-only tone audit; no audited text rewritten in this slice

---

## Methodology

This audit applies two reference frames to a bounded surface:

1. **Primary reference (load-bearing):** the project's own `CLAUDE.md` "Tone discipline (banned phrases in the system prompt)" section. The banned-phrase list is stricter than the persona's defaults and is the gate.
2. **Supplementary reference:** the `voltagent-qa-sec:ai-writing-auditor` persona at `~/.claude/plugins/marketplaces/voltagent-subagents/categories/04-quality-security/ai-writing-auditor.md` — 34 detection categories across formatting, sentence structure, and a 103-entry tiered vocabulary list.

**Severity mapping** (used in findings below):

- **High** = literal `CLAUDE.md` banned-phrase hit OR persona Tier 1 vocabulary (always-replace).
- **Medium** = persona Tier 2 vocabulary clustered (two or more in a paragraph) OR formatting saturation past the persona's hard max.
- **Low** = persona Tier 3 vocabulary near density threshold, or single Tier 2 word with no cluster.

### Audit surface (bounded)

The slice doc names six surface categories. The slice estimate (1d) does not support exhaustive coverage of all six. The following bounded surface was selected for high operator-visibility:

| #   | File                                              | Why selected                                                          |
| --- | ------------------------------------------------- | --------------------------------------------------------------------- |
| 1   | `README.md`                                       | Public top-of-funnel artifact; first contact with the project's voice |
| 2   | `Plans/canvas/01-vision.md`                       | Most operator-facing canvas section (positioning + personas)          |
| 3   | `Plans/canvas/03-ucf.md`                          | Most technical canvas section (graph model) — different tone register |
| 4   | `docs-site/docs/oauth-grants.md`                  | Recent docs slice 325 output — sample of current docs voice           |
| 5   | `docs/audit-log/340-chromedp-flake-decisions.md`  | Recent JUDGMENT-slice decisions log — tests engineer-output tone      |
| 6   | `docs/audit-log/341-chromedp-fanout-decisions.md` | Same — second sample of recent decisions-log tone                     |
| 7   | The 10 most-recently-modified `docs/issues/*.md`  | Sample of slice text quality (slices 331-341, excluding 337 itself)   |

Initial pass surfaced more than three substantive findings on this bounded surface, so the audit was NOT expanded per the bounded-scope rule.

### Out of scope (deferred to a future audit pass)

- `Plans/canvas/02-primitives.md` and 04-11 (other canvas sections)
- `Plans/EVIDENCE_SDK.md`, `Plans/UCF_GRAPH_MODEL.md`
- The remaining ~330 `docs/issues/*.md` files
- The remaining ~130 `docs/audit-log/*.md` files
- `docs/adr/*.md`
- `docs/governance/*.md`
- `docs-site/docs/**` beyond `oauth-grants.md`
- README screenshots directory (`docs/images/`)
- The mockups at `Plans/mockups/`

A future audit slice can apply the same methodology to a different bounded surface; the cap on this slice is 1d.

### AC-5 — LLM prompt enforcement verification

AC-5 of the slice doc requires: "verifies banned-phrase list is enforced in LLM prompts (not just documented)."

Searched the codebase for active LLM call sites:

- `internal/board/narrative.go` is a pure Go `text/template` renderer with explicit anti-LLM comments (`// AC-3 + AC-6 + the P0 anti-criterion "Does NOT include LLM-generated narrative in v1": the brief narrative is produced by a Go text/template over the structured Brief. There is NO LLM, no inference call, no network path.`). No banned-phrase exposure surface.
- No `internal/ai/`, `internal/llm/`, or equivalent package was found.
- `internal/mcp/` is an MCP-client subsystem, not an LLM-prompt subsystem.

**Disposition:** the v1 board narrative is template-driven; no LLM prompt currently consumes the banned-phrase list. The list is documented but not yet enforced anywhere because no enforcement surface exists yet. Per CLAUDE.md, board-narrative v0 is v2+ work — the enforcement gap is structural, not a regression. This is captured as a forward-looking checklist item in the spillover slice for slice 182's continuation work (see Spillover section below).

---

## Findings

### File 1: `README.md`

| Severity | Line(s) | Category                        | Excerpt                                                                 | Suggested rewrite                                                                                                            |
| -------- | ------- | ------------------------------- | ----------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| Medium   | whole   | Formatting — em-dash saturation | 30 em-dashes in 1713 words (17.5 per 1000)                              | Persona hard max is one per 1000 words. Replace most em-dashes with commas, periods, or parentheticals. Triage by paragraph. |
| Low      | 26      | Persona Tier 3 density          | "operator-grade today"                                                  | Acceptable as project jargon; flag for review only if reused elsewhere.                                                      |
| Low      | 200     | Recurring jargon — "first-pass" | "first-pass review", "first-pass audits" used twice in adjacent bullets | Vary the second occurrence: "scheduled review" or "scheduled audit cadence" reads fresher.                                   |

**Density:** 1 medium + 2 low = bundle into "tone polish round 1" (not high-density per the 3-findings-of-substance threshold).

### File 2: `Plans/canvas/01-vision.md`

| Severity | Line(s) | Category                              | Excerpt                                                   | Suggested rewrite                                                                                                                                                                                         |
| -------- | ------- | ------------------------------------- | --------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| High     | 21      | CLAUDE.md banned phrase — superlative | "SimpleRisk — best-in-class risk register, narrow scope." | CLAUDE.md explicitly bans "best-in-class". Use a specific, falsifiable claim: "SimpleRisk — narrow scope, well-regarded risk register." Even when praising a third party, the banned-phrase list applies. |
| Medium   | whole   | Formatting — em-dash saturation       | 22 em-dashes in 1461 words (15 per 1000)                  | Same persona hard max. Many of these are load-bearing parentheticals; the rest can be commas or periods.                                                                                                  |
| Low      | 16      | Tier 3 density (single occurrence)    | "engineering-hostile" (compound)                          | Specific and useful; flag only if reused as a recurring pattern.                                                                                                                                          |

**Density:** 1 high + 1 medium + 1 low = high-density per the ≥3 threshold. Warrants its own rewrite slice. The high-severity banned-phrase hit at line 21 is the load-bearing reason.

### File 3: `Plans/canvas/03-ucf.md`

| Severity | Line(s) | Category                   | Excerpt                                 | Suggested rewrite                                                                                |
| -------- | ------- | -------------------------- | --------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Low      | whole   | Formatting — em-dash count | 5 em-dashes in 646 words (7.7 per 1000) | Over persona hard max but within document-length tolerance. Drop two; the rest are load-bearing. |

**Density:** 1 low. No rewrite slice. Bundle into tone polish round 1 if other low-density findings accumulate; otherwise no action.

### File 4: `docs-site/docs/oauth-grants.md`

No findings against CLAUDE.md banned phrases or Tier 1 vocabulary. Em-dash density within tolerance for technical reference docs. The tone is measured, factual, and matches the project's discipline. No action.

### File 5: `docs/audit-log/340-chromedp-flake-decisions.md`

| Severity | Line(s)          | Category                                | Excerpt                                                 | Suggested rewrite                                                                                                |
| -------- | ---------------- | --------------------------------------- | ------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| Low      | 36, 39, 137, ... | Project-jargon density — "load-bearing" | "load-bearing change", "load-bearing diagnostic record" | Project-canonical term (appears 3× in this file). Acceptable; flagged only if the term loses meaning by overuse. |

**Density:** 1 low. The decisions-log voice is sober and technical throughout — no banned-phrase hits, no Tier 1 vocabulary, no marketing-y framing. Good baseline. No action.

### File 6: `docs/audit-log/341-chromedp-fanout-decisions.md`

No findings. Tone is mechanical and factual throughout. Decisions log voice matches the discipline. No action.

### File 7: 10 most-recently-modified `docs/issues/*.md`

Files audited: 331, 332, 333, 334, 335, 336, 338, 339, 340, 341.

| File               | Severity | Line(s)            | Category                           | Excerpt                                               | Suggested rewrite                                                                                   |
| ------------------ | -------- | ------------------ | ---------------------------------- | ----------------------------------------------------- | --------------------------------------------------------------------------------------------------- |
| 331-a11y           | Low      | 122, 139, 149, 182 | Project-jargon density — "harness" | "UI honesty audit harness" (× 5)                      | Proper-noun-like reference to a specific slice (178). Not a Tier 2 AI-ism in this usage. No action. |
| 333-qa-strategy    | Low      | 154, 156           | Recurring jargon — "load-bearing"  | "load-bearing decisions", "load-bearing decisions"    | Project-canonical. Acceptable.                                                                      |
| 334-test-framework | Low      | 119, 155           | Project-jargon — "harness"         | "UI honesty audit harness" (× 2)                      | Same as 331. No action.                                                                             |
| 336-ux-flow        | Low      | 102, 184           | Project-jargon — "harness"         | "UI honesty harness"                                  | Same as 331. No action.                                                                             |
| 338-pentest        | Low      | 92, 134, 184       | Recurring jargon — "load-bearing"  | "Load-bearing" prefix used three times on subsections | Vary the prefix occasionally; "primary objective" or "central probe" reads cleaner.                 |

**Density:** all low. No high-density findings. Bundle into tone polish round 1.

**Aggregate observation across the slice docs:** the project has its own evolved jargon vocabulary ("load-bearing", "harness" as a slice-178 reference, "tracer-bullet", "diligence the diligence tool"). None of these are AI-isms in the persona's sense — they are working domain terms. They become AI-ism-adjacent if overused; the audit observed mild overuse of "load-bearing" but no hard violations.

---

## Pattern-density summary

Ranked by finding count + severity weight:

| Rank | File                                              | High | Medium | Low | Disposition                                          |
| ---- | ------------------------------------------------- | ---- | ------ | --- | ---------------------------------------------------- |
| 1    | `Plans/canvas/01-vision.md`                       | 1    | 1      | 1   | **High-density — file its own rewrite slice (342).** |
| 2    | `README.md`                                       | 0    | 1      | 2   | Bundle into "tone polish round 1" (slice 343).       |
| 3    | `Plans/canvas/03-ucf.md`                          | 0    | 0      | 1   | Bundle into tone polish round 1.                     |
| 4    | `docs/audit-log/340-chromedp-flake-decisions.md`  | 0    | 0      | 1   | Bundle into tone polish round 1.                     |
| 5    | 10 recent `docs/issues/*.md` (combined)           | 0    | 0      | 5   | Bundle into tone polish round 1.                     |
| 6    | `docs-site/docs/oauth-grants.md`                  | 0    | 0      | 0   | Clean — no action.                                   |
| 7    | `docs/audit-log/341-chromedp-fanout-decisions.md` | 0    | 0      | 0   | Clean — no action.                                   |

**Total:** 1 High · 2 Medium · 10 Low = 13 findings across 7 files (within the bounded surface).

**High-density files (≥3 findings of substance):** 1 — `Plans/canvas/01-vision.md`.

**Spillover slice fan-out:** 2 (one rewrite + one polish round). Cap is 5; under cap.

---

## Candidate additions to CLAUDE.md tone discipline

The audit surfaced three patterns that may merit codification in `CLAUDE.md`'s banned-phrase list:

1. **"first-pass" overuse in the security-audit context** — README.md uses this twice in adjacent bullets. Not banned, but a candidate for "vary terminology in adjacent occurrences" guidance.
2. **"load-bearing" overuse** — appears in the project's own discipline as a meta-term ("constitutional invariants", "load-bearing decisions"). Useful as jargon; risk of meaning-loss if applied to non-load-bearing concerns. Candidate for a "use only when the claim is falsifiable" qualifier.
3. **"harness" as a Tier 2 word the project has reclaimed** — persona Tier 2 flags "harness" as an AI-ism, but the project uses it literally to name a Playwright test harness (slice 178). Candidate for documenting that the persona's Tier 2 list is supplementary and project-specific exceptions apply.

These are NOT modifications to CLAUDE.md in this slice — they are proposed for a separate spillover slice (slice 344). Per slice 337's P0-337-6, this slice does not touch CLAUDE.md.

---

## Spillover slices filed

Per Amendment 2 continuous-batch policy:

| Slot | File                                                     | Type                                                                                                                         |
| ---- | -------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| 342  | `docs/issues/342-vision-canvas-tone-rewrite.md`          | High-density file rewrite for `Plans/canvas/01-vision.md` (banned-phrase + em-dash saturation)                               |
| 343  | `docs/issues/343-tone-polish-round-1.md`                 | Low-density bundle covering README + 03-ucf.md + decisions-log jargon + slice-doc jargon                                     |
| 344  | `docs/issues/344-claude-md-tone-discipline-expansion.md` | Propose three additions to CLAUDE.md's banned-phrase list (first-pass overuse, load-bearing qualifier, harness-as-exception) |

Cap is 5; 3 filed. No board-narrative LLM-enforcement spillover filed — the surface does not yet exist (template-only v1 board narrative). The enforcement gap is captured in slice 344's narrative for future reference when slice 182's v2 work begins.

---

## Cross-references

- **Slice 182** (board-narrative AI-assist foundation) — the canonical tone-anti-pattern reference. This audit confirms the v1 board-brief renderer is template-only and the banned-phrase list does not yet have a runtime enforcement surface.
- **`CLAUDE.md`** "Tone discipline (banned phrases in the system prompt)" — primary reference frame.
- **`docs/governance/board-narrative-tone-anti-patterns.md`** — the full anti-pattern list maintained alongside slice 182.
