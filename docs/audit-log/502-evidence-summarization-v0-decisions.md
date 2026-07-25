# Slice 502 — AI evidence-summarization v0 — decisions log

`Type: JUDGMENT`. Claude made the subjective build-time calls itself and
recorded them here; the maintainer iterates post-deployment from the
"Revisit once in use" list. This log does NOT block merge.

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. All integration ACs — valid render, bounded
top-N, fabricated-id suppression, cross-tenant suppression, unknown-control
not-found — passed first run against real Postgres + RLS via the slice-498
`StubClient` CI seam. The pure-Go service + handler suppression branches passed
first run on the fast surface.)

## Context

Slice 502 is the §10.2 **sibling** of slice 444 (gap-explanation). Where 444
explains a _gap state_ from the deterministic freshness rollup, 502 summarizes
the _evidence content_ from the actual records. The slice was deliberately built
as a thin mirror of 444's shape (same Service/seam structure, same
validate-then-suppress + graceful-degradation contract, same non-binding
disclosure UX, same never-persist posture) so the two §10.2 surfaces stay
consistent and the well-trodden 444 pattern carries the constitutional weight.
The decisions below record where 502 _differs_ from 444 (the corpus it
summarizes) and re-affirm the inherited calls.

## Decisions made

### D1 — The summary is NOT persisted (no `ai_generations` row). `high`

**Options considered.** (a) Write one `ai_generations` row per generation via
`llm.Service.GenerateAndRecord`. (b) Call `llm.Client.Generate` directly and
persist nothing.

**Chosen: (b)** — identical to 444 D1. P0-502-4 is explicit ("regenerated on
demand"). The `ai_generations` ledger is a snapshot-at-generation forensic
record for approvable/binding surfaces; a non-binding comprehension aid that
regenerates on **every** control-detail view would only grow that ledger
without forensic value (threat-model R: non-binding ⇒ no audit-trail
requirement for the text itself). Transparency (AC-6) is met by returning the
model provenance in the _response_, not by persisting it. The Service holds a
bare `llm.Client`, not a `*llm.Service`/`AuditWriter`. This surface joins 444 as
the second place that diverges from the slice-498 "every surface writes a
generation row" expectation — deliberate, documented in the package doc.

### D2 — Citation strictness: validate every citation, suppress the whole summary on a single failure. `high`

**Options considered.** (a) Strip the unresolvable citation and render the rest.
(b) Suppress the whole summary and fall back to the deterministic evidence list.

**Chosen: (b)** — identical to 444 D2. The no-fabricated-coverage invariant
(P0-502-1, threat-model T) is **bindingness-independent** and cannot be "mostly"
honored: a summary that asserts coverage one cited ID cannot confirm
tenant-owned misleads the operator just as badly as a binding artifact would.
Partial-strip would leave the operator reading model prose whose surrounding
sentences may lean on the stripped (fabricated) claim. Two gates enforce it: a
cheap in-memory **grounding gate** (`allowedIDs` — the model may only cite the
control id + the cited-excerpt ids it was actually shown), then a
**tenant-ownership gate** (the RLS-scoped `Store.Resolve`). A no-citation draft
is ALSO suppressed (`ReasonNoCitations`): a summary with no grounding is not a
_cited_ summary. A new fourth reason, `ReasonNoEvidence`, short-circuits BEFORE
the model call when the control has no live evidence — the model could only
fabricate from nothing, so we never burn the call.

### D3 — Corpus bounding: top-N **most-recent** live records (N = 8, `observed_at DESC`). `high`

This is the slice's headline JUDGMENT call (P0-502-8 / AC-1 — "bounded top-N,
not the full history").

**The bound: N = 8.** Matches 444's `maxCitedExcerpts`. A control can accumulate
hundreds of records over its life; feeding the full history would (a) blow the
prompt/token budget (threat-model D), (b) make the model wander, and (c) grow
the set of citable IDs unbounded. Eight of the freshest records is enough
grounding for a "what does this evidence collectively show" paragraph while
keeping the prompt small and the latency low.

**The ordering: recency, not relevance.** The set is `observed_at DESC` — the N
**most-recent** live records — reusing the existing control-detail evidence data
path (`ListEvidenceRecordsByControl`) verbatim. Recency was chosen over a
relevance/semantic rank because (1) v1 has no semantic-retrieval substrate
(pgvector is slice 500, not yet landed — a relevance rank would be keyword
heuristics at best, as slice 441 had to do), and (2) for the "what does my
evidence show **right now**" question the operator is actually asking, the
freshest records ARE the most relevant — an access-review completion from last
week speaks to current posture better than one from two years ago. The bound is
deliberately the same data path the deterministic evidence list already renders,
so the summary and the list-it-degrades-to are over the same records. The UI
labels it "Summarizing the N most-recent of M live records" so the bound is
honest and visible (the `total` count comes from a new read-only
`CountEvidenceRecordsByControl`).

### D4 — CURRENT LIVE evidence only; never a frozen audit-period population. `high`

P0-502-5 / invariant #10. The retrieval reads the live `evidence_records`
ordered by `observed_at DESC`; it does **not** draw from a frozen audit-period
sample population. The wire shape carries an always-true `live_only` flag and
the UI labels the set "current live evidence only", so the operator can never
mistake a live comprehension aid for a period-scoped audit view. A period-scoped
summary that respects audit-period freezing is a NAMED follow-on (see Revisit
#1), explicitly not built here — mixing the two populations in one v0 surface is
exactly the audit-period-pollution anti-pattern the constitution rejects.

### D5 — Route via the slice-499 per-tenant inference client. `high`

P0-502-6. The Service is wired with `s.inferenceClient()` (the slice-499
`cloud.Router` over the local Ollama client), the SAME client every other
AI-assist surface (440/441/444/471) uses. This means local Ollama is the
off-by-default backend and cloud egress happens only under the tenant's explicit
opt-in, with the slice-499 routing banner inherited for free on the frontend
(the shared `<CloudRoutingBanner />`, rendered in the card with zero extra
wiring). The Service call site (`s.client.Generate`) is unchanged from 444 — the
per-tenant routing is transparent to the surface (P0-499-6).

### D6 — Graceful-degradation copy is a fixed, safe vocabulary. `medium`

The suppression reason surfaced to the UI is one of four closed values
(`generation_unavailable`, `unresolved_citation`, `no_citations`, `no_evidence`)
mapped to short honest operator copy in the node-testable view-model. It NEVER
echoes a raw backend error or model text (slice-367 leak discipline). An unknown
reason falls back to a neutral "Showing the underlying evidence records." The
deterministic evidence list (the `showing`/`total` bound label + the records
themselves) renders in every state (AC-7 / P0-502-7).

### D7 — Frontend: a self-contained card over a node-testable view-model. `medium`

Mirrors 444 D6. The card (`evidence-summary.tsx`) owns its own TanStack query
and mounts on the control-detail Overview rail beside the gap-explanation card —
it does not thread props through the control-detail page. The NON-BINDING render
decisions (show summary vs evidence-list-only, the disclosure, the explicit
absence of any approve/publish/export affordance) live in a pure
`evidence-summary-view.ts` module, unit-covered on the node-env vitest surface
(slice 069 P0-A3 / slice 353 Q-3). The rendered DOM + the cloud-banner render
are the Playwright e2e tier's concern.

## Revisit once in use (named follow-ons — NOT built here)

1. **Period-scoped evidence summary (audit-period freezing).** A summary that
   draws from a frozen audit-period sample population (`observed_at ≤ frozen_at`)
   for an in-progress audit, respecting invariant #10. This is the explicit
   §10.2 follow-on named in the slice doc. It is a distinct surface (different
   corpus, different labeling, an audit-workspace mount) — kept out of v0 so the
   live and frozen populations never mix in one code path. **Filed as a
   spillover slice (see below).**
2. **Multi-control / portfolio evidence summary.** Summarizing the evidence
   posture across many controls (a framework, a scope cell, the whole program)
   rather than one control. The slice doc names this as a follow-on. It needs a
   different retrieval shape (cross-control rollup) and a different mount (a
   dashboard, not control-detail). **Filed as a spillover slice (see below).**
3. **Citation resolution is K+1 transactions (D-mitigation tail).**
   `Store.Resolve` opens one tx per cited id; with the ≤8-excerpt cap that is ≤9
   short transactions per summary, dwarfed by the LLM call. If latency or pool
   pressure shows up, batch into one `ResolveAll` with two `id = ANY($1)`
   queries. Left un-batched for v0 (small bound, mechanical rewrite). Inherited
   verbatim from 444 — when 444's tail is batched, 502's should be too.
4. **Per-tenant rate limiting.** Threat-model D names per-tenant rate limiting;
   v0 relies on the platform middleware chain + the bounded prompt/token/timeout
   caps. Add a dedicated per-tenant generation rate limit if abuse appears.
5. **Shared control-read guard.** `requireControlRead`/`hasControlRead` are
   copied from `controldetail`/`gapexplain` (now the third copy). They should be
   lifted into a shared helper so the control-read policy lives once — deferred
   here for the same reason 444 deferred it (the `controldetail` internals are
   out of scope; sibling control-detail slices are concurrently editing that
   package). When slice-035's DB-backed `user_roles` becomes the role source of
   truth, all copies must change together.
6. **Summary quality with the default local model.** Llama 3.1 8B is the
   slice-182 D5 baseline; once real operators read real summaries, judge whether
   the default-model output is good enough or whether the prompt needs tightening
   / few-shot examples. The citation gate guarantees _no fabricated coverage_
   regardless of model quality; readability is a model-quality question.
7. **Recency vs relevance once pgvector lands (slice 500).** D3 chose recency
   because v1 has no semantic substrate. Once slice 500 ships pgvector, revisit
   whether a relevance-ranked excerpt set produces better summaries than the
   strict-recency set for controls with heterogeneous evidence kinds.

## Confidence summary

| Decision                           | Confidence |
| ---------------------------------- | ---------- |
| D1 no-persist                      | high       |
| D2 strict citation                 | high       |
| D3 corpus bounding (top-N recency) | high       |
| D4 live-only                       | high       |
| D5 per-tenant routing              | high       |
| D6 degradation copy                | medium     |
| D7 frontend split                  | medium     |

The two `medium`-confidence calls (D6/D7 copy + render split) top the revisit
list because they are the operator-experience calls only real use can validate.
D3 (corpus bounding) is `high` on the _mechanism_ (bounded, recency, honest
label) but its recency-vs-relevance choice is explicitly revisited once pgvector
lands (Revisit #7).
