# Slice 441 — Questionnaire AI-answer suggestion v0 (decisions log)

JUDGMENT slice. This is the platform's **first AI-WRITE surface** (an
AI-suggested questionnaire answer an operator approves and persists). Claude
made the subjective build-time calls below using best-reasoned, pattern-matched
judgment, recorded them here, and shipped. The runtime **AI-assist boundary is
constitutional and untouched** — this log is a development-process artifact, not
a relaxation of that boundary. The product still never publishes a
customer-facing answer without one-click human approval, never fabricates
coverage, and never seeds Tenant B with Tenant A data.

- detection_tier_actual: integration
- detection_tier_target: integration

> One real bug surfaced during the slice and was caught at the right tier: the
> first `seedPolicy` integration helper omitted `owner_role` / `approver_role`
> / `created_by`, which the `policies` table's NOT-NULL + nonempty CHECKs
> reject (SQLSTATE 23514). Caught by running the integration suite against real
> Postgres (`target=integration, actual=integration`) — exactly where a
> fixture-vs-schema mismatch should surface, not in production. Fixed in the
> seed helper; no production-code change.

## How slice 498's shared guard was adopted on `questionnaire_answers`

The CLAUDE.md AI-assist boundary names this slice as the one that lands the
boundary columns on `questionnaire_answers`. Migration
`20260612000000_questionnaire_answer_ai_columns.sql`:

- **ALTERs** `questionnaire_answers` (slice 155 table) to add
  `ai_assisted BOOLEAN`, `human_approved BOOLEAN`, `human_approver TEXT`, and
  the board-narrative provenance set `prompt_version` / `model_name` /
  `model_version` / `model_provider` (all NOT NULL with safe DEFAULTs so the
  existing manual-answer rows stay valid — they are `ai_assisted=FALSE`).
- **ADOPTS** the slice-498 reusable `ai_assist_human_approver_guard(...)`
  IMMUTABLE function via a one-line CHECK
  (`questionnaire_answers_ai_assist_invariant`) — the predicate is NOT
  re-authored (P0-498-4 discipline). The Go side
  (`internal/llm.EnforceApproval`) is the early-rejection mirror; the DB CHECK
  is authoritative. `TestDBGuard_RejectsApprovedWithoutApprover` proves the
  CHECK fires at the DB layer (admin/BYPASSRLS role) for the one forbidden
  shape.
- The append-only forensic record (prompt + candidate ids + raw draft) is the
  job of the slice-498 `ai_generations` ledger, NOT this table. This table
  carries only the current answer state + approval columns, keeping the
  slice-155 one-row-per-question invariant.

## Decisions made

### D1 — Retrieval strategy: keyword first-pass over policies + evidence

**Decision.** v0 retrieval is a **keyword first-pass** (P0-441-5 — NO
pgvector). The question text is tokenized (`keywordsFrom`: lowercase, split on
non-alphanumerics, drop a small security-tuned stoplist + sub-3-char tokens,
dedupe). Two tenant-owned candidate sources are searched under RLS:

1. **Policies** whose `title`/`body_md` ILIKE-match any keyword, restricted to
   `status IN ('approved','published')` — a draft policy is not something to
   assert to a customer.
2. **Evidence records** whose **control's title** ILIKE-matches any keyword,
   restricted to `result = 'pass'` — evidence has no free text of its own, so
   it is described by its control, and only passing evidence supports a
   positive answer.

The SQL casts a wide net (any-token match, capped at `sqlLimit=50` per source);
`rankCandidates` then tightens it in-memory to a keyword-overlap-scored top
`maxCandidates=8`, dropping zero-score rows.

**Options considered.** (a) pgvector semantic retrieval — explicitly the
follow-on, deliberately NOT pulled into v0 (the grill output names this). (b)
answer_library priors as a third source — deferred to the
prior-response-similarity follow-on (a separate boundary-permitted surface). (c)
TF-IDF / BM25 scoring — overkill for v0; a keyword-overlap count is enough to
surface the obviously-relevant policy/evidence, and the operator reviews every
draft.

**Why.** The boundary-critical part of v0 is the cited-draft + approval
machinery, not retrieval quality. A simple, auditable keyword pass proves the
machinery; retrieval quality is its own slice.

### D2 — Citation strictness: a SINGLE unresolvable citation suppresses the whole draft

**Decision.** `validateCitations` is STRICT, mirroring slice 444. Every cited
id must pass **two** gates: (1) the **grounding gate** — it must be in the
candidate set the prompt showed the model; (2) the **tenant-ownership gate** —
it must resolve to a real tenant-owned policy/evidence row under RLS. A single
failure of either gate suppresses the ENTIRE draft, and **nothing is persisted**
(a draft the operator must not see is never written — P0-441-4).

**Options considered.** (a) drop only the bad citation + keep the rest — rejected:
a no-fabricated-coverage invariant cannot be "mostly" honored, and a
customer-facing answer is the highest-stakes place to half-honor it. (b)
ownership gate only (no grounding gate) — rejected: a model that invents an id
which happens to name some other tenant-owned row the operator may view is
still fabricating coverage it was never shown. Grounding discipline is "answer
ONLY from what you were shown".

**Why.** This is the threat-model-T mitigation. Strictness is the correct
default for the first AI-write surface; an operator who gets "insufficient
evidence" answers manually (D3) rather than receiving a half-grounded draft.

### D3 — "Insufficient evidence" threshold: two layers, both fail to manual

**Decision.** The no-fabricated-coverage path (AC-5) fires at **two** layers:

1. **Structural.** If the keyword pass returns zero candidates, the model is
   **never called** — there is nothing to ground an answer in, so the surface
   short-circuits to `InsufficientEvidence` and persists nothing. Fabrication
   is impossible because there is nothing to fabricate from.
2. **Model-judged.** If candidates exist but the model judges them off-topic,
   the system prompt instructs it to emit the exact sentinel
   `INSUFFICIENT_EVIDENCE`; `isInsufficient` recognizes it (tolerant of
   surrounding whitespace/punctuation, but a real answer that merely _mentions_
   the word is not insufficient) and the surface returns insufficient.

Both layers leave the operator on the manual-authoring surface (invariant #9 —
manual evidence is first-class). The insufficient path is a **feature**, tested
explicitly (`TestSuggest_NoEvidenceInsufficient`,
`TestSuggest_ModelSaysInsufficient`).

**Why.** "Insufficient evidence — answer manually" is the no-fabricated-coverage
guardrail in action, not a fallback. Making the structural layer short-circuit
before the LLM call also means the zero-candidate case never spends an
inference and never risks a hallucinated answer.

### D4 — Draft shape: persist on the valid path, suppress-without-persist otherwise

**Decision.** A valid, fully-cited draft is **persisted** as an unapproved draft
(`ai_assisted=TRUE`, `human_approved=FALSE`, `human_approver=NULL`) so the
operator can return to it and approve in a separate one-click action. The
insufficient and suppressed outcomes persist **nothing**. Approval is a
distinct `Service.Approve` call (no auto-approve path exists); the approver is
always derived from the authenticated credential server-side, never
client-supplied.

**Options considered.** (a) return the draft transiently without persisting (the
slice-444 gapexplain shape) — rejected: a questionnaire answer is approvable +
binding-once-approved, so the draft must survive to be approved; gapexplain is
non-binding and regenerates on demand. (b) persist even suppressed drafts for
forensics — rejected: P0-441-4 says the operator must never see a suppressed
draft, and persisting one risks it leaking into the UI; the `ai_generations`
ledger (a follow-on wiring) is the right home for raw-draft forensics, not the
answer row.

### D5 — Cloud-routing banner: structurally false in v0, plumbed for the follow-on

**Decision.** The `Suggestion.CloudRouted` flag + the frontend banner exist but
are structurally `false` in v0 (`isCloudProvider` treats only
ollama/local/stub as non-cloud). v0 is local-Ollama-only (P0-441-6); the field

- banner are plumbed so the cloud opt-in follow-on surfaces the visible routing
  banner (CLAUDE.md inference-backend rule) without a wire-shape change.

## Revisit once in use

- **Retrieval recall.** The keyword stoplist + 3-char floor are tuned by
  judgment, not data. Once operators run real questionnaires, measure how often
  the keyword pass misses an obviously-relevant policy/evidence (the
  pgvector-follow-on trigger).
- **Excerpt bound.** `maxExcerptRunes=600` per candidate + `maxCandidates=8` is
  a guess at "enough grounding without prompt bloat". Revisit if drafts come
  back under-grounded (too little) or the model wanders (too much).
- **answer_value.** v0 persists an empty `answer_value` on the AI draft (the
  narrative is the cited prose); the yes/no/n.a. chip stays a manual choice.
  Revisit if operators want the model to also propose the discrete value.
- **Per-claim citation granularity.** v0 validates that the draft cites
  _something_ resolvable; it does not bind each sentence to a citation. The
  board-narrative surface's per-claim numeric verification is a heavier
  discipline that questionnaire answers may or may not need — revisit when the
  board-narrative v0 (slice 182 continuation) lands and the patterns can be
  compared.

## Detection-tier classification

- detection_tier_actual: integration
- detection_tier_target: integration
