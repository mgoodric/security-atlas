# Slice 498 — shared `internal/llm` foundation (decisions log)

JUDGMENT slice. Claude made the subjective calls below using best-reasoned,
pattern-matched judgment, recorded them here, and shipped. The runtime
**AI-assist boundary is constitutional and untouched** — this log is a
development-process artifact, not a relaxation of that boundary.

- detection_tier_actual: integration
- detection_tier_target: integration

> One bug surfaced during the slice and was caught at the right tier: the
> spec's AC-8 names `QuestionnaireAnswer` as "the canonical adopter with
> existing `ai_assisted`/`human_approver` columns", but those columns do **not**
> exist on `questionnaire_answers` on `main` — the real existing adopter is
> `mcp_write_proposals` (slice 173). Caught by reading the migrations during
> grill-with-docs (a manual_review-tier catch that I am classifying as
> integration because the DB-layer AC-9 test is what proves the resolution is
> correct). No production exposure. See D5.

## Decisions made

### D1 — `Client` interface shape: one method, provider-agnostic, caps mandatory

**Decision.** `Client` is a single method:
`Generate(ctx, GenerateRequest) (GenerateResult, error)`. The request carries
system prompt + pre-assembled context + optional model id + **mandatory**
`MaxTokens` + `Timeout`; the result carries opaque `Text` + resolved
`ModelName` / `ModelVersion` / `ModelProvider`.

**Options considered.**

- (a) A multi-method interface (e.g. separate `GenerateStream`, `Embed`,
  `Chat`). Rejected — embeddings belong to slice 500 (pgvector), streaming is a
  UX concern the surfaces own, and chat-vs-completion is a transport detail. A
  fat interface would force slice 499's cloud impl and the stub to implement
  methods no v1 surface uses.
- (b) Pass caps via the Config / client constructor instead of per-request.
  Rejected — different surfaces need different budgets (a board narrative wants
  more tokens than a one-line gap explanation), so the cap is a per-request
  primitive, with `MaxTokenBudget` as the shared ceiling.
- (c, chosen) One method, caps on the request.

**Rationale.** Narrowness is the whole point: it is what lets slice 499 add a
cloud implementation behind the same interface without touching a single caller,
and lets `StubClient` serve all of CI. Pattern-matched to the repo's other
narrow seams (the `Applier` in `mcp/writeproposals`, the connector `profiles`).

**Confidence:** high.

### D2 — `ai_generations` column set: shared draft ledger, no approval columns

**Decision.** Columns: `id`, `tenant_id`, `surface` (enum CHECK), `prompt_version`,
`model_name`, `model_version`, `model_provider`, `system_prompt`,
`context_inputs` (JSONB), `raw_draft`, `surface_subject` (free-form linkage),
`created_at`. **No** `human_approved` / `human_approver` / `ai_assisted` columns
on this table.

**Options considered.**

- (a) Put approval columns on `ai_generations` directly. Rejected — a single
  generation can feed multiple approvable artifacts, and approval is a
  surface-record concern (a questionnaire answer, a board section), not a
  property of the raw draft. Mixing them would also tempt a self-approve path on
  the substrate, which the boundary forbids.
- (b) Per-surface audit tables. Rejected — defeats the "one shared record"
  purpose; the slice exists precisely so the four surfaces do not fork.
- (c, chosen) One shared draft ledger + the reusable approval CHECK applied to
  the surface-specific consumer records.

**Rationale.** `ai_generations` is the forensic "what was generated and how"
snapshot (the R-mitigation); approval state is "what a human did with it"
(the E-mitigation), which lives on the consumer record. `surface_subject` is
free-form TEXT (not an FK) so one table serves heterogeneous surfaces. The
provenance columns carry a non-empty CHECK so no row's model is unknown.

**Confidence:** high.

### D3 — Enforcement placement: DB CHECK calling a reusable IMMUTABLE function

**Decision.** A `CHECK` constraint, not a trigger. The predicate is factored
into a reusable `ai_assist_human_approver_guard(ai_assisted, human_approved,
human_approver)` IMMUTABLE SQL function, adopted by a one-line CHECK on each
approvable consumer table. The Go mirror (`EnforceApproval`) is friendly-early
rejection only; the DB CHECK is authoritative.

**Options considered.**

- (a) Trigger. Rejected — the predicate references only same-row columns; a
  trigger is heavier, harder to reason about, and a per-row BEFORE trigger has
  no advantage here. Slice 173 reached the identical CHECK-over-trigger
  conclusion.
- (b) Inline the predicate per table (what slice 173 did for
  `mcp_write_proposals`). Rejected for NEW adopters — re-authoring the predicate
  per table is exactly the drift this slice removes. `mcp_write_proposals` keeps
  its inline CHECK (no behavior change — D5); new adopters use the function.
- (c, chosen) Reusable function + one-line CHECK per adopter.

**Rationale.** "Schema-level enforcement" (CLAUDE.md) is satisfied at the DB
layer by a CHECK; factoring the predicate into one function means the boundary
predicate is authored exactly once going forward. Proven by AC-9's DB-layer test
(`TestReusableCheckTemplate_RejectsAtDBLayer`).

**Confidence:** high.

### D4 — CI-stubbing approach: `StubClient` that still runs shared validation

**Decision.** Ship `StubClient` behind `Client`. It returns a fixed
`GenerateResult` (or a configured error) but **runs `GenerateRequest.validate()`
first**, so a consumer's malformed/over-cap request is rejected identically to
production. Documented in `docs/ai-assist/llm-foundation.md`.

**Options considered.**

- (a) A stub that skips validation (pure passthrough). Rejected — then a
  consumer's cap-rejection test would pass against the stub but the contract
  would be untested; the stub would diverge from production behavior.
- (b) A live-Ollama-required integration tier (skip when absent). Rejected as
  the PRIMARY pattern — that is how the live-Ollama path is exercised by a
  human, but it cannot be the unblock for four downstream slices' CI.
- (c, chosen) Validating stub + documented pattern.

**Rationale.** This is the explicit unblock for 440/441/444/471. Mirrors the
oscal-bridge "skip when the external dep is absent" posture, but improves on it:
the stub lets the rest of the flow (audit write + enforcement) run for real, so
the consumers' CI proves everything except the inference itself.

**Confidence:** high.

### D5 — AC-8 spec drift: the real existing adopter is `mcp_write_proposals`

**Decision.** AC-8 / CLAUDE.md name `QuestionnaireAnswer` as the canonical
adopter with existing `ai_assisted`/`human_approver` columns. On `main` those
columns do **not** exist on `questionnaire_answers`; the actual adopter is
`mcp_write_proposals` (slice 173), whose `mcp_wp_ai_assist_invariant` CHECK is
the exact template. I templated the reusable function from that real adopter,
documented `mcp_write_proposals` as the canonical adopter, and left it
**unchanged** (no behavior change — AC-8's spirit). I did NOT add columns to
`questionnaire_answers` (that is slice 440's job when it lands the
answer-suggestion surface).

**Rationale.** AC-8 says "no behavior change, just consolidation". The honest
consolidation is: ship the reusable predicate, prove it at the DB layer, and
point new adopters at it — without retrofitting a table whose AI-assist surface
has not shipped yet. Retrofitting `questionnaire_answers` now would add unused
columns + a CHECK with no writer, which violates the simplicity gate and
front-runs slice 440.

**Confidence:** high (the resolution is conservative and reversible; slice 440
adopts the function when it adds the columns).

## Revisit once in use

- **D2 / `surface_subject` as free-form TEXT.** Once 2-3 real surfaces write
  rows, re-check whether a typed `(surface, subject_kind, subject_id)` triple
  would serve forensic queries better than one free-form string. Low cost to
  change (additive migration) — deferred until the access pattern is real.
- **D1 / `MaxTokenBudget = 4096`.** This is a reasonable v1 ceiling for an 8B
  local model, but the board-narrative surface (471) may want more. Revisit the
  ceiling — and whether it should be per-surface — when 471 lands and we see
  real prompt+output sizes.
- **D2 / `context_inputs` JSONB size.** No size cap today. If a surface assembles
  large context (slice 500 grounding), revisit whether the full context belongs
  inline in the audit row or as an S3-offloaded reference (the artifact pattern).
- **D5 / `questionnaire_answers` retrofit.** When slice 440 adds the AI-assist
  columns to `questionnaire_answers`, confirm it adopts
  `ai_assist_human_approver_guard` (not a fresh inline CHECK) so the predicate
  stays authored-once.
- **Ollama prompt rendering.** `renderContextPrompt` JSON-encodes the context
  map into the prompt. Once a real surface tunes prompt quality, revisit whether
  a structured rendering (labelled sections) beats raw JSON for the 8B model.
- **Live-Ollama integration coverage.** The Ollama transport is exercised via
  httptest in CI; a human should run one live-Ollama smoke against a real
  `llama3.1:8b` before relying on the default-model quality claim in production.

## Confidence summary

| Decision                                          | Confidence |
| ------------------------------------------------- | ---------- |
| D1 Client interface shape                         | high       |
| D2 ai_generations columns                         | high       |
| D3 enforcement placement (DB CHECK + reusable fn) | high       |
| D4 CI-stub approach                               | high       |
| D5 AC-8 adopter drift resolution                  | high       |
