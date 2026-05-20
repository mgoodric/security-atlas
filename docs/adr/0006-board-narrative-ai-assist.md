# ADR 0006 — Board-narrative AI-assist: seven coupled sub-decisions

**Status:** Accepted (foundation pre-commitments — slice 182). Implementation deferred to v2+ (board-narrative v0).

**Date:** 2026-05-20

**Resolves:** [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) #14

**Implements through:** [`docs/issues/182-board-narrative-ai-assist-foundation.md`](../issues/182-board-narrative-ai-assist-foundation.md) (foundation only); board-narrative v0 slices land at v2+.

**Slot note:** This ADR was specified as `0002-board-narrative-ai-assist.md` in the slice 182 spec. At pickup time (2026-05-20) slots 0002 through 0005 were occupied (bearer-token storage, audit-period freeze hash inputs, control-detail 404 empty state, branch-protection PAT vs. app). Per the slice's "ADR slot computation" note ("Verify the slot is still free at pickup time; if a `0002-` ADR has landed in the meantime, increment.") the ADR ships at slot 0006. All cross-references in this slice's deliverables (CLAUDE.md is already shipped and does not name a slot number; the canvas update and the tone-anti-pattern reference both point at `0006-board-narrative-ai-assist.md`).

---

## Context

The auto-drafted board narrative is simultaneously **the most valuable feature for the solo security leader** and **the highest-risk AI-assist surface in the platform**. Practitioner research (IANS Research, March 2026) reports that 34% of CISOs say boards dismiss security warnings out of hand and only 29% of board directors describe cybersecurity updates as very effective. The market gap is real; no GRC tool today produces a board-ready narrative, and every CISO rebuilds the deck quarterly in Google Slides.

The risk side is equally real and asymmetric to other AI-assist surfaces in the platform:

- **Questionnaire drafting (slice-050 vintage)** — the auditor or prospect on the receiving end usually has the technical literacy to spot a hallucinated control claim. Errors are caught at review.
- **SCF-mapping suggestions** — the maintainer or compliance lead reviewing the mapping is a domain expert; errors are caught at human-approval gate.
- **Freshness explanations** — narrow, factual, easy to verify; low downside if wrong.
- **Board narratives** — the audience is non-technical by definition. The board reads the narrative at face value. A hallucinated coverage number, a fabricated trend, or a marketing-y framing of a real failure can mislead the fiduciary body the platform was built to serve.

That asymmetry is the **deep insight that load-bears the design**: board narratives are unique among GRC outputs because they're consumed by people who cannot verify them. The platform's response cannot be "good enough at most things"; the response must be a coupled bundle of guardrails strong enough that the worst-case output is still acceptable.

Canvas open question #14 deferred the LLM boundary pending design work; this ADR locks it in.

The decision is **seven coupled sub-decisions** (D1 through D7), all chosen together because each one's weakness is mitigated by another's strength. Picking three of seven would not produce a safe surface; picking all seven produces one. The ADR documents each decision with its rationale and the rejected alternatives, so future contributors understand why the bundle is shaped this way.

The CLAUDE.md "AI-assist boundary (hard)" section was already expanded as part of the OQ #14 resolution PR to capture the seven decisions at constitutional level. This ADR is the architecture-record companion that captures **why** each decision was made and **what was rejected**. Slice 182 lands the supporting artifacts (this ADR, the tone-anti-pattern reference, the canvas update, the model-refresh cadence). The actual board-narrative v0 implementation ships at v2+.

## Decision

The seven sub-decisions, in canvas-resolution order. Each is binding when board-narrative v0 ships. Each has a one-paragraph rationale; the rejected alternatives are in the "Alternatives Considered" section below.

### D1 — Input shape: hybrid (structured rollup + cited evidence excerpts)

When the LLM drafts a section, the input is a **deterministic pre-computation rollup** (numeric coverage / freshness / drift / risk summaries the platform computes without LLM involvement) **plus cited evidence excerpts** for every factual claim the section will make. The LLM is forbidden from making claims that do not trace to one of these inputs.

**Rationale:** raw evidence is too expensive (the model wanders, picks up noise, hallucinates). Pure rollup is too compressed (the model fabricates context to fill gaps and editorializes). The hybrid forces grounding by construction — the model has the numbers from the rollup and the supporting text from the excerpts, and the system prompt instructs the model to cite both.

### D2 — Approval granularity: per-section

The narrative is split into **numbered sections** (rollup → trend → exceptions → top risks → asks). Each section is approved, edited, or rejected independently. The operator cannot approve the narrative as a whole without explicitly clearing every section.

**Rationale:** per-narrative is too coarse — the operator who wants to approve four of five sections but reject the fifth has to either edit-and-approve or reject-everything. Per-claim is too friction-heavy — operators won't click through twenty approval prompts and will rubber-stamp by the end. Per-section sits at the right granularity: high enough that the operator engages with each section, low enough that approval is finishable in one sitting.

### D3 — Audit trail: full prompt + full response, every time

Every board-narrative generation records: the system prompt (versioned), the evidence inputs (the rollup payload + the evidence-excerpt set), the model name + version + provider, the generated draft, every operator edit (diff against the draft), and the final approved text. Stored as a single immutable audit record per section.

**Rationale:** the surface is high-risk; the audit trail must be forensically airtight. Storage cost is small (a board-narrative section is a few KB; even a full annual archive is single-digit MB per tenant). Diff-only trails would lose the original prompt at the moment the model behavior matters most. The audit trail is RLS-scoped to the tenant; privacy mitigation is the same RLS posture every other tenant-data primitive carries.

### D4 — Hallucination guardrails: four enforced

The platform ships **all four** of the following guardrails — not as opt-in features, as the constitutional baseline:

- **(a) Mandatory citations.** Every factual claim resolves to a specific evidence ID, control ID, risk ID, or rollup number. Unresolved citations reject the draft before the operator sees it.
- **(b) Numeric verification.** Every number in the draft (`94% fresh`, `47 controls`, `12 exceptions`, `up 6 points`) is cross-checked against the deterministic pre-computation. Numbers that do not match ground truth reject the draft.
- **(c) Section-shape enforcement.** The LLM is constrained to the numbered section template. Freestyle output, additional sections, or rearranged sections reject the draft.
- **(d) Editor-mode operator UX.** The operator cannot approve text with unresolved citations. The UI surfaces unresolved citations as inline blockers.

**Rationale:** any one of these guardrails alone fails — (a) without (b) lets the LLM cite real evidence for fabricated numbers; (b) without (c) lets the LLM rearrange sections to dodge enforcement; (c) without (d) lets the operator rubber-stamp around the gate. The four together are a coupled gate that the worst-case LLM output cannot escape.

### D5 — Inference backend: Llama 3.1 8B Instruct default + cloud opt-in

The default local model is **Llama 3.1 8B Instruct** (q5 quantization baseline per the tech-stack table), runnable on commodity hardware (8-12GB GPU). Operators who need GPT-4-class quality opt into a cloud LLM (Anthropic / OpenAI / Bedrock) per tenant with the visible-banner discipline already shipped in slice 050.

The default model recommendation **refreshes every 6-12 months** as the local-model landscape evolves. The refresh cadence is documented in [`docs/operator/maintenance-cadence.md`](../operator/maintenance-cadence.md) and is a maintainer task, not a slice.

**Rationale:** the local-Ollama-first posture (canvas §9 tech-stack commitment) makes self-hosted deployments not leak tenant data to a third-party API by default. Llama 3.1 8B is the current best-quality local model in the 8-16GB hardware envelope that's commodity in 2026. The model is acknowledged to be lower-quality than GPT-4 / Claude / Gemini at long-form drafting; the cloud opt-in is the escape hatch for tenants who can accept the data-routing trade-off. The 6-12 month refresh cadence keeps the local-first story honest as the landscape evolves.

### D6 — Rejection / iteration flow: inline-edit by default

When the operator wants to change a section, the default action is **inline edit** (operator types directly into the draft text). Three additional affordances are available:

- **(6B) Regenerate** — toss this section's draft, re-run the prompt with the same inputs.
- **(6C) Regenerate with instruction** — operator adds a free-text instruction ("Tone is too defensive on the GitHub exception"), prompt re-runs with the additional steer.
- **(6D) Write from scratch** — operator authors the section without LLM involvement; the audit record captures that the AI was bypassed for this section.

**Rationale:** inline edit is the lowest-friction iteration path and matches how operators actually use AI-assisted drafting in practice (research from the LLM coding-assistant literature is unambiguous on this). Regenerate handles the case where the operator's objection is to the draft as a whole; regenerate-with-instruction handles steerable objections; write-from-scratch is the escape hatch for sections the LLM cannot draft acceptably.

### D7 — Prompt-template versioning: snapshot with each generation

Every board-narrative generation snapshots the prompt template version (a `prompt_version TEXT` column on the eventual board-narrative-records table, when v0 ships). Old reports stay as-is — they are immutable historical artifacts, generated under the prompt revision active at the time. No retroactive re-flagging.

**Rationale:** prompt templates evolve as the maintainer learns from real failure modes (Section 4 of the tone-anti-pattern reference). Versioning forward-only means the audit trail is honest about what the platform was doing at the time. Retroactive re-evaluation would create a perverse incentive (don't fix prompt failures because it might re-open old reports) and would muddy the audit posture.

## Consequences

**Positive:**

- The board-narrative surface ships with constitutional-grade guardrails, not "we'll add safety later." The decision bundle is locked before the implementation starts.
- The hybrid input shape (D1) keeps the LLM working within citable bounds; the four guardrails (D4) catch the residue; the full audit trail (D3) backstops the rest.
- Per-section approval (D2) and inline-edit-default (D6) make the operator experience finishable. The friction is real but bounded.
- Llama 3.1 8B default (D5) keeps the local-first posture honest. Cloud opt-in keeps the quality escape hatch available.
- Snapshot versioning (D7) preserves audit-history integrity while allowing the prompt to improve.
- The tone-anti-pattern reference ([`docs/governance/board-narrative-tone-anti-patterns.md`](../governance/board-narrative-tone-anti-patterns.md)) is the trust root for D4's tone-discipline aspect; the file's living-document discipline (Section 4 of that file) lets the platform learn from real failure modes without architectural changes.

**Negative:**

- Implementation cost is high. Board-narrative v0 is a multi-slice build: prompt construction, numeric-verification library, section-shape enforcement, editor-mode UX, audit-log integration, prompt-template versioning. None of those individually is a tiny slice.
- Llama 3.1 8B's quality ceiling is lower than cloud LLMs. Some operators will encounter sections the local model cannot draft acceptably; they must opt into cloud LLM or write the section by hand. Practitioner expectation needs to be managed via operator docs.
- The four guardrails (D4) interact at the prompt-construction layer. A change to the numbered template (D4-c) cascades to the regex / section-parsing logic; a change to the citation format (D4-a) cascades to the numeric-verification library (D4-b). The coupling is intentional but raises the cost of any single change.
- Full prompt + response audit storage (D3) is small per-tenant but non-zero. At ~5 KB per section, four sections per narrative, four narratives per year, a tenant with 100 years of history would carry ~8 MB of audit log — trivial for any modern deployment but worth naming.
- The tone-anti-pattern reference is a living document; its quality drifts toward the maintainer's attention. The discipline in Section 4 of that file is meant to keep the drift bounded.

**Neutral:**

- Cloud-LLM data routing is already shipped (slice 050); no new tenant-visibility infrastructure is required for D5.
- The schema-level extensions named in the CLAUDE.md expansion (`prompt_version`, `model_name`, `model_version`, `model_provider`) land WITH board-narrative v0, not in this slice. The contract is documented in CLAUDE.md and ADR-0006; the migration is v2+ work.

## Alternatives Considered

Each rejected option is named alongside the sub-decision it would have replaced, with a one-sentence rationale for rejection. The bundle was chosen as a whole, but the alternatives for each individual decision were canvassed.

### Alternatives to D1 (input shape)

- **Raw evidence records only.** Rejected — the model wanders across irrelevant records, picks up noise, and inflates the prompt cost; hallucination rate rises as context grows.
- **Pure rollup only (no evidence excerpts).** Rejected — the model compensates for missing context by fabricating it, which is exactly the failure mode the platform is designed to prevent.
- **Tenant-configurable input shape.** Rejected — the choice is a safety decision, not a tenant preference; allowing per-tenant variation defeats the bundle's coupling.

### Alternatives to D2 (approval granularity)

- **Per-narrative approval (single click).** Rejected — too coarse; the operator will not engage with each section, and a single bad section taints the entire report.
- **Per-claim approval.** Rejected — too friction-heavy; the operator will rubber-stamp by the third or fourth claim, defeating the gate.
- **Per-paragraph approval.** Rejected as a middle ground — the section is the right grain because it matches the audience's reading flow (the board reads a section, asks a question, moves on); paragraphs are arbitrary.

### Alternatives to D3 (audit trail depth)

- **Diff-only audit trail.** Rejected — loses the original prompt + draft, which is the part that matters when the surface fails. Diff alone is forensically incomplete.
- **Hash-only audit trail (prompt and response hashed, original discarded).** Rejected — same failure mode; hashes confirm tampering but cannot reconstruct what the model actually said.
- **Approved-text-only audit trail (no draft retention).** Rejected — the platform's safety case rests on showing how the AI-drafted text was changed by the operator; without the draft, the operator-edit value is invisible.

### Alternatives to D4 (hallucination guardrails)

- **Subset of guardrails (mandatory citations + numeric verification only).** Rejected — leaves section-shape and editor-mode gaps. The four together close the gates the individual guardrails cannot close alone.
- **LLM self-critique (model checks its own output).** Rejected — self-critique is unreliable in current open-weights models and even less reliable when the critique target is the model's own confabulation.
- **Human-graded confidence score.** Rejected — adds operator cognitive load without producing a binary block; the platform's posture is to block bad output, not to grade it.
- **No guardrails; trust the model.** Rejected — incompatible with the deep insight (asymmetric hallucination cost on a non-technical audience).

### Alternatives to D5 (inference backend)

- **Cloud LLM default.** Rejected — violates the local-Ollama-first posture (canvas §9) and the privacy commitment in the AI-assist boundary.
- **No local model (cloud-only).** Rejected — same reason, with the additional flaw that self-hosted deployments lose the feature entirely.
- **Larger local model (70B-class) as default.** Rejected for v0 — hardware requirements exceed the commodity envelope (70B requires 40-48GB VRAM); operators on standard developer or production hardware cannot run it. Available as an opt-in for operators with the hardware; not the default.
- **No model recommendation — operator picks.** Rejected — abdicates a load-bearing decision to the operator; a default with a documented refresh cadence is more honest.

### Alternatives to D6 (rejection / iteration flow)

- **Regenerate-only (no inline edit).** Rejected — the LLM literature is clear that human-in-the-loop iteration is fastest when the human can edit directly; regenerate alone wastes the operator's time on small fixes.
- **Inline edit only (no regenerate / regenerate-with-instruction / write-from-scratch).** Rejected — there are cases where the entire section should be re-drafted (regenerate) or where the operator wants to bypass the AI entirely (write-from-scratch); the bundle covers all four.
- **AI-assisted edit (operator types, AI suggests).** Rejected for v0 — adds a second LLM call per keystroke; cost and latency are unacceptable for an annual feature.

### Alternatives to D7 (prompt-template versioning)

- **No versioning (prompt changes are silent).** Rejected — destroys the audit-trail integrity that D3 establishes.
- **Retroactive re-evaluation (old reports re-graded against new prompts).** Rejected — creates the perverse incentive named above (don't fix prompt failures because it might re-open old reports) and muddies the audit posture.
- **Tenant-controlled prompt templates.** Rejected — the prompt template is part of the safety case; tenant override defeats the coupling. A future tenant-customizable feature could allow non-safety-relevant additions (e.g., the operator's preferred salutation) without touching the safety-critical body.

## References

- [`CLAUDE.md`](../../CLAUDE.md) — "AI-assist boundary (hard)" → "Board-narrative AI-assist" subsection (constitutional commitment).
- [`Plans/canvas/11-open-questions.md`](../../Plans/canvas/11-open-questions.md) #14 — resolved 2026-05-20 (the seven-decision bundle).
- [`Plans/canvas/04-evidence-engine.md`](../../Plans/canvas/04-evidence-engine.md) §4.6 — canvas reflection of the seven decisions.
- [`Plans/canvas/07-metrics.md`](../../Plans/canvas/07-metrics.md) — board reporting first-class commitment; auto-drafted narrative described at §7 with the explicit "no publish without one-click human approval" boundary.
- [`docs/governance/board-narrative-tone-anti-patterns.md`](../governance/board-narrative-tone-anti-patterns.md) — load-bearing tone reference; the file system-prompt and numeric-verification consume.
- [`docs/operator/maintenance-cadence.md`](../operator/maintenance-cadence.md) — local-model refresh cadence (D5's operational discipline).
- [`docs/issues/182-board-narrative-ai-assist-foundation.md`](../issues/182-board-narrative-ai-assist-foundation.md) — slice that landed the foundation pre-commitments.
- IANS Research, March 2026 — "Boards give CISO cybersecurity reporting a mixed grade" — practitioner data on board-narrative effectiveness gap (linked from canvas §7).

## Related decisions

- Composes with the AI-assist boundary (hard) — the board-narrative seven decisions are a sub-bundle of the platform-wide boundary; the boundary establishes "no audit-binding artifact without one-click human approval", and the seven decisions are the concrete shape of that approval for the highest-risk surface.
- Composes with [`docs/adr/0003-audit-period-freeze-hash-inputs.md`](./0003-audit-period-freeze-hash-inputs.md) — both ADRs share the audit-period-immutability posture (D7's snapshot-with-each-generation is the same family as the audit-period freeze: the historical record stays as it was at the time).
- Composes with slice 050 (cloud-LLM opt-in per-tenant) — D5's cloud-opt-in path reuses slice 050's data-routing banner discipline; no new tenant-visibility infrastructure required.
