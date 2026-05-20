# Operator maintenance cadence

This document captures the recurring maintenance tasks that the security-atlas maintainer (or self-hosting operator) performs at predictable intervals. Each section names the task, the cadence, the trigger metric, and the recording convention.

This is a **living document**: new cadence-driven tasks land here as they're identified. Each section is independently consultable.

**Cross-references:**

- [`CLAUDE.md`](../../CLAUDE.md) — constitutional principles + AI-assist boundary; this doc operationalizes some commitments named there.
- [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.6 — AI-assist surfaces.
- [`docs/audit-log/`](../audit-log/) — refresh-recording target directory.

---

## Local model recommendation refresh

**Cadence:** every 6-12 months.

**Owner:** the security-atlas repository maintainer. Self-hosting operators can apply the same discipline to their own deployment if they pin a model version locally.

**Trigger metric:** a new local model with comparable hardware requirements (8-16GB GPU, commodity 2026-era developer or production hardware) demonstrably outperforms the current default on board-narrative-style drafting tasks.

**Current default:** **Llama 3.1 8B Instruct** (q5 quantization baseline; locked at canvas open question #14 resolution 2026-05-20). Rationale: best-quality open-weights instruction-tuned model in the 8-12GB GPU envelope that fits the local-Ollama-first posture (canvas §9 tech-stack commitment). Documented quality caveat: lower-quality than GPT-4 / Claude / Gemini at long-form drafting; cloud opt-in per tenant is the escape hatch for operators who can accept the data-routing trade-off.

### Why this is a documented task and not a slice

The refresh is editorial judgment, not engineering work. The maintainer evaluates candidate models against held-out board-pack drafts and decides whether the new option crosses the bar. The platform code does not change — the recommendation lives in operator docs and the AI-assist boundary section of CLAUDE.md. Filing this as a recurring slice would be process-theater; documenting the cadence keeps the discipline honest without inventing make-work.

### The maintainer task

When the trigger metric fires (a new candidate model surfaces in the open-weights community, or the maintainer is performing the scheduled 6-12 month check), the maintainer executes:

1. **Benchmark.** Run the candidate model against a held-out board-pack draft set. The draft set is a curated collection of (rollup, evidence-excerpt, expected-output) tuples that exercise the seven sub-decisions' surface area:
   - Coverage section (numeric rollup → narrative)
   - Trend section (prior-quarter delta → narrative)
   - Exceptions section (raw exception records → narrative)
   - Top-risks section (risk register top-N → narrative)
   - Asks section (operator's manual notes → narrative)
   - Boundary cases: missing-citation, numeric-mismatch, tone-failure, section-shape-violation
2. **Score.** Per-section pass/fail under the four guardrails (D4 in ADR-0006): mandatory citations, numeric verification, section-shape enforcement, editor-mode operator UX rejection of unresolved citations. The candidate is a candidate-for-replacement only if it equals or exceeds the current default on all four guardrails AND demonstrably improves long-form drafting quality on a side-by-side blind evaluation.
3. **Decide.** If the candidate clears the bar, update the default. If not, record the evaluation outcome and re-check at the next 6-12 month interval.
4. **Update.** When a default changes, the maintainer modifies — in a single PR:
   - [`CLAUDE.md`](../../CLAUDE.md) "Board-narrative AI-assist" subsection (`Default local model + refresh cadence` paragraph) — replace the model name + the runtime envelope.
   - [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.6.7 D5 entry — replace the model name.
   - [`docs/adr/0006-board-narrative-ai-assist.md`](../adr/0006-board-narrative-ai-assist.md) D5 entry — replace the model name; add the prior model and the rationale to the "Alternatives Considered" section if appropriate (the prior default becomes a rejected alternative for future readers).
   - This file (the "Current default" line above).
   - The `tech-stack` table in [`CLAUDE.md`](../../CLAUDE.md) (`AI inference` row).
   - Any operator-facing pages in `docs-site/docs/` that name the model (search for the current model name).
5. **Record.** Create [`docs/audit-log/model-refresh-<YYYY>-<MM>.md`](../audit-log/) documenting the refresh. Required fields:
   - Date of refresh.
   - Outgoing default + version.
   - Incoming default + version (or "no change, re-evaluated 2026-11-01" if the trigger did not fire).
   - Benchmark draft set version (the curated tuple set is itself versioned).
   - Per-section pass/fail summary against the four D4 guardrails.
   - Decision rationale (≥1 paragraph; the audit log is a fiduciary record).
   - PR that landed the model swap (link).

### Drift between the four model references

Three documents name the default local model: this doc, CLAUDE.md (in two places — the AI-assist boundary section and the tech-stack table), and ADR-0006. The model-recommendation refresh PR MUST update all four references in the same commit, and slice 182 P0 anti-criterion 6 ("Local-model default MUST be cross-referenced consistently across CLAUDE.md, ADR-0006, and `docs/operator/maintenance-cadence.md`. Drift between these references is a rejected merge.") applies to every subsequent refresh.

A pre-commit or CI lint that grep-checks the model name across the four locations is a reasonable follow-up improvement; not in scope for slice 182. Until that lint lands, the discipline is human review at PR time.

### What does NOT trigger a refresh

- A new cloud LLM (Anthropic, OpenAI, Bedrock) released — these reach the platform via slice 050's cloud opt-in path; tenants pick them per-tenant. The local-model default is unaffected.
- A new quantization of the same model (e.g., q5 → q6 of Llama 3.1 8B Instruct) — that's a deployment tuning question, not a default-recommendation change.
- A new local model with larger hardware requirements (e.g., 70B class) — that breaks the commodity-hardware envelope. Such models are documented as opt-in for operators with the hardware; they do not become the default.
- A new local model with smaller hardware requirements that performs worse — quality regression at lower cost is not a replacement.

### Why 6-12 months specifically

The local-open-weights model landscape moves faster than the GRC product calendar. Six months is the lower bound where the maintainer's evaluation effort is justified (a model released this week is rarely production-grade by next week). Twelve months is the upper bound past which the platform's recommendation looks stale — the open-weights ecosystem in 2026 ships meaningful new instruction-tuned models on roughly a quarterly cadence. The 6-12 month window gives the maintainer flexibility while keeping the recommendation honest.

---

## Other cadences (placeholders for future tasks)

This file is the seeding section under slice 182. Subsequent slices may add:

- SCF catalog version refresh (when SCF ships a new annual release).
- Framework-version refresh per framework (SOC 2 TSP rev. cadence, ISO 27001 rev. cadence, etc.).
- Dependency upgrade cadence (Go / Node / Postgres major versions).
- Documentation-link rot scan.

When such a section is added, follow the same shape: name, cadence, owner, trigger metric, current value, task steps, recording convention, anti-triggers (what does NOT count). Each section stands alone; each is independently consultable.
