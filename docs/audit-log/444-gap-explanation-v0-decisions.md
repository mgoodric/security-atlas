# Slice 444 — AI gap-explanation v0 — decisions log

`Type: JUDGMENT`. Claude made the subjective build-time calls itself and
recorded them here; the maintainer iterates post-deployment from the
"Revisit once in use" list. This log does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. All three integration ACs — valid render,
fabricated-id suppression, cross-tenant suppression — passed first run against
real Postgres + RLS.)

## Decisions made

### D1 — The explanation is NOT persisted (no `ai_generations` row). `high`

**Options considered.** (a) Write one `ai_generations` row per generation via
`llm.Service.GenerateAndRecord` (the substrate's documented "thin caller"
shape that 471/440/441 will use). (b) Call `llm.Client.Generate` directly and
persist nothing.

**Chosen: (b).** P0-444-4 is explicit — "Does NOT persist the explanation as an
audit artifact — it is regenerated on demand." The `ai_generations` ledger is
a _snapshot-at-generation forensic record_ meant for approvable / binding
surfaces (board narrative, questionnaire answers); a non-binding comprehension
aid that regenerates on **every** control-detail view would only grow that
ledger without forensic value (and the threat-model R section of the slice
says non-binding ⇒ no audit-trail requirement for the text itself). The
transparency requirement (AC-6, surface the model) is met by returning the
model provenance in the _response_, not by persisting it. The Service therefore
holds a bare `llm.Client`, not a `*llm.Service`/`AuditWriter`.

**Consequence.** This surface is the one place that diverges from the slice-498
"every surface writes a generation row" expectation. The divergence is
deliberate and documented in the package doc.

### D2 — Citation strictness: a SINGLE unresolvable citation fails the whole explanation. `high`

**Options considered.** (a) Strip the unresolvable citation and render the rest.
(b) Suppress the whole explanation and fall back to the deterministic rollup.

**Chosen: (b).** The no-fabricated-coverage invariant (P0-444-1, threat-model
T) is bindingness-independent and cannot be "mostly" honored — a draft that
cites one ID the platform cannot confirm tenant-owned is a draft the operator
cannot trust, even informationally. Partial-strip would leave the operator
reading a model paragraph whose surrounding prose may lean on the stripped
(fabricated) claim. Suppression + graceful fallback to the deterministic rollup
(AC-7) is the honest degradation. Two gates enforce it: a cheap in-memory
**grounding gate** (`allowedIDs` — the model may only cite the control id + the
cited-excerpt ids the rollup actually showed it), then a **tenant-ownership
gate** (the RLS-scoped `Store.Resolve` confirms each id names a row visible to
the requesting tenant). A no-citation draft is ALSO suppressed
(`ReasonNoCitations`): an explanation with no grounding is not a _cited_
explanation.

### D3 — Explanation shape: short paragraph, inline UUID citations, measured tone. `medium`

The system prompt constrains the model to (1) state ONLY the rollup facts,
(2) cite the control + any evidence by exact UUID in parentheses, (3) not
invent ids, (4) measured/factual tone (mirrors the CLAUDE.md board-narrative
ban list even though this surface is lower-risk — a consistent project-wide
LLM voice is cheaper to maintain), (5) 2–4 sentences. UUID-verbatim citation is
load-bearing: it is what makes the regex-parse + resolve validation gate
meaningful. The render shows the text, the resolved citations as `kind:id8…`
badges, and the mandatory non-audit-artifact disclosure.

### D4 — Rollup = freshness facts + bounded evidence excerpts (no recompute). `high`

The deterministic rollup reuses the slice-016 read-models verbatim:
`GetControlByID` (title + tenant-ownership proof), `GetEvidenceFreshnessByControl`
(is_stale / valid_until / latest_observed_at / evidence_count), and
`ListEvidenceRecordsByControl` capped at 8 (the cited excerpts). NO new SQL,
NO migration — the slice reads existing tenant-scoped tables under
`app.current_tenant`. When the freshness read-model has not been refreshed for
a control, the rollup reports stale-with-zero-evidence (the slice-016
definition: a control with no observation is not currently fresh) so there is
always something meaningful to explain.

### D5 — Graceful-degradation copy is a fixed, safe vocabulary. `medium`

The suppression reason surfaced to the UI is one of three closed values
(`generation_unavailable`, `unresolved_citation`, `no_citations`) mapped to
short honest operator copy in the node-testable view-model. It NEVER echoes a
raw backend error or model text (slice-367 leak discipline). An unknown reason
falls back to a neutral "Showing the underlying freshness facts."

### D6 — Frontend: a self-contained card over a node-testable view-model. `medium`

The card (`gap-explanation.tsx`) owns its own TanStack query and mounts on the
Overview right rail beside Freshness — it does not thread props through the
1400-line control-detail page. The NON-BINDING render decisions (show
explanation vs rollup-only, the disclosure, the explicit absence of any
approve/publish/export affordance) live in a pure `gap-explanation-view.ts`
module so they are unit-covered on the node-env vitest surface (slice 069
P0-A3 / slice 353 Q-3 — vitest is node-only, no JSX). The rendered DOM is the
Playwright e2e tier's concern.

## Revisit once in use

1. **Citation resolution is K+1 transactions (D-mitigation tail).** `Store.Resolve`
   opens one tx per cited id; with the ≤8-excerpt cap that is ≤9 short
   transactions per explanation, dwarfed by the 30s LLM call. If explanation
   latency or pool pressure ever shows up, batch into one `ResolveAll` with two
   `id = ANY($1)` queries (one over controls, one over evidence_records). Left
   un-batched for v0 because the bound is small and the rewrite is mechanical.
   (Surfaced by the simplify efficiency pass.)
2. **Per-tenant rate limiting.** The slice's threat-model D names per-tenant
   rate limiting; v0 relies on the existing platform middleware chain + the
   bounded prompt/token/timeout caps. Add a dedicated per-tenant generation
   rate limit if abuse appears (the generation is the expensive part).
3. **Shared control-read guard.** `requireControlRead`/`hasControlRead` are
   copied from `controldetail` (the second copy). They should be lifted into a
   shared helper (e.g. `credstore.HasControlRead` or an `apiauthz` package) so
   the control-read policy lives once — deferred here because `controldetail`
   internals are out of this slice's scope (sibling slice 692 is concurrently
   editing that package). When slice-035's DB-backed `user_roles` becomes the
   role source of truth, both copies must change together.
4. **Explanation quality with the default local model.** Llama 3.1 8B is the
   slice-182 D5 baseline; once real operators read real explanations, judge
   whether the default-model output is good enough or whether the prompt needs
   tightening / few-shot examples. The citation gate guarantees _no fabricated
   coverage_ regardless of model quality, but readability is a model-quality
   question.
5. **Drift facts in the rollup.** v0's rollup is freshness-centric (the core
   gap signal). If operators want the explanation to also speak to the
   slice-016 _drift_ signal (controls that flipped out of passing), feed the
   per-control drift state into the rollup — a small additive change.

## Confidence summary

| Decision             | Confidence |
| -------------------- | ---------- |
| D1 no-persist        | high       |
| D2 strict citation   | high       |
| D3 explanation shape | medium     |
| D4 rollup reuse      | high       |
| D5 degradation copy  | medium     |
| D6 frontend split    | medium     |

The two `medium`-confidence calls (D3 explanation shape, D5/D6 copy + render
split) top the revisit list because they are the operator-experience calls
that only real use can validate.
