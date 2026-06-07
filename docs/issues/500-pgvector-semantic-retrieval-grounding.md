# 500 — pgvector semantic-retrieval grounding for AI-assist drafts (citations-at-retrieval)

**Cluster:** AI-assist
**Estimate:** L (3d)
**Type:** JUDGMENT (embedding-model choice + chunking strategy + retrieval-relevance threshold)
**Status:** `blocked` (needs #498 — the `internal/llm` client + assembled-context seam)

> Filed 2026-06-06 via the AI-assist/reporting gap analysis. Canvas §4.6.5
> commits to a shared grounding stack: **"The grounding stack is the same for
> either backend: pgvector or Qdrant for embeddings of (a) prior approved
> answers, (b) policy chunks, (c) recent evidence summaries. Citations are
> required at retrieval, not generation — the model can only cite documents that
> the retriever returned."** The tech-stack table pins **pgvector (v2 when
> AI-assist lands); Qdrant is a v3 option for large corpora.** Slice 441 (and
> the other v0 surfaces) explicitly defer this — they ship a **keyword
> first-pass** and name "pgvector semantic retrieval" as an explicit follow-on.
> **No slice owns the semantic-retrieval grounding layer.** This is it.

## Narrative

**Why (the gap today).** The v0 AI-assist surfaces (441 questionnaire answers,
and the grounding-dependent parts of 444/471) retrieve candidate evidence/policy
with a **keyword first-pass** — a deliberate v0 simplification. Keyword retrieval
misses semantically-relevant material that does not share surface terms (a
question about "multi-factor authentication" should surface a policy titled
"strong authentication" or evidence tagged `IAC-06`, even with no keyword
overlap). Worse, weak retrieval undermines the **citations-at-retrieval**
guarantee: the model can only cite what the retriever returned, so retrieval
quality directly bounds answer quality and grounding integrity. The canvas
commits to pgvector semantic retrieval as the grounding stack; nothing owns
building it.

**What (the deliverable shape).**

1. **pgvector-backed embedding store** — a tenant-scoped table holding
   embeddings of the three corpora the canvas names: (a) prior **approved**
   questionnaire answers, (b) policy chunks, (c) recent evidence summaries. Each
   embedding row carries the source ID (evidence/policy/answer) so a retrieval
   hit resolves directly to a citable, tenant-owned record.
2. **An embedding pipeline** — chunk + embed new/updated source records into the
   store (idempotent re-embed on source change). Embeddings are produced via the
   slice-498 inference substrate's embedding capability (local model by default,
   consistent with the local-first posture) — **no external embedding API in v0
   unless the tenant has opted into cloud per slice 499.**
3. **A semantic `Retrieve` seam** — a retrieval function returning the top-N
   most-similar tenant-owned source IDs for a query, under RLS. This is the
   **grounding layer the AI-assist surfaces plug in front of the slice-498
   `Client`**: the surface calls `Retrieve` to get candidate IDs, assembles the
   cited context, then calls `Generate`. The citations-at-retrieval guarantee is
   enforced here — the model only ever sees (and can only cite) what `Retrieve`
   returned.
4. **A swap-in for the keyword first-pass** — the surfaces that shipped keyword
   retrieval (441 et al.) gain semantic retrieval behind the same candidate-ID
   contract, so the v0 citation/approval machinery is unchanged — only retrieval
   quality improves.

**Scope discipline.** This slice ships \*\*the pgvector store + embedding pipeline

- the `Retrieve` seam**, and wires it into the existing v0 candidate-ID contract
  so surfaces can adopt it without changing their citation/approval logic. It does
  **not** ship Qdrant (that is the v3 large-corpus option — explicitly out). It
  does **not** change the AI-assist boundary or the approval gates (retrieval feeds
  grounding; it does not publish anything). It does **not** retroactively re-embed
  historical corpora as a blocking step — the pipeline embeds going forward + on a
  backfill job. **Follow-on slices:\*\* Qdrant adapter for large corpora; embedding
  of additional corpora (e.g., control-implementation notes); hybrid
  keyword+semantic re-ranking if pure-semantic relevance proves insufficient.

## Threat model

STRIDE pass (design-time). Verdict: **has-mitigations** — the dominant surfaces
are **cross-tenant retrieval bleed** (the retriever must never return another
tenant's documents) and **grounding integrity** (the model can only cite what
`Retrieve` returned).

**S — Spoofing.** Retrieval is invoked only inside an already-authenticated
AI-assist surface; it adds no new public ingress.
_Mitigation/AC:_ `Retrieve` runs inside the surface's existing role-gated path;
no standalone unauthenticated retrieval endpoint.

**T — Tampering / grounding integrity (PRIMARY).** If the retriever returns
fabricated or wrong source IDs, the model could "cite" material that does not
support the claim — eroding the citations-at-retrieval guarantee.
_Mitigation/AC:_ every embedding row is keyed to a real source ID; `Retrieve`
returns only IDs that resolve to real tenant-owned rows; the downstream surface's
citation validator (440/441/471) re-asserts resolution before the operator sees
the draft. The model is given ONLY retrieved excerpts — it cannot cite outside
the returned set.

**R — Repudiation.** Which documents grounded a generation must be
reconstructable.
_Mitigation/AC:_ the retrieved candidate IDs are captured in the slice-498
`ai_generations` context-inputs field, so the audit row records exactly what the
retriever surfaced for that generation.

**I — Information disclosure / cross-tenant retrieval bleed (PRIMARY).** The
embedding store holds tenant-confidential text-derived vectors; a retrieval leak
would surface tenant A's policy/evidence/answers as candidates in tenant B's
draft — the severe cross-tenant-seeding breach the AI-assist boundary forbids.
_Mitigation/AC:_ the embeddings table is four-policy RLS-scoped on
`app.current_tenant`; `Retrieve` queries run under the requesting tenant's RLS
context so the similarity search physically cannot return another tenant's rows.
An integration test proves a tenant-B retrieval returns zero tenant-A source IDs
even when tenant-A content is the nearest neighbor by raw vector distance.

**D — Denial of service.** Embedding generation + similarity search over a large
corpus is expensive; an unbounded re-embed or huge top-N exhausts resources.
_Mitigation/AC:_ `Retrieve` caps top-N; the embedding pipeline is batched +
rate-bounded; re-embed is idempotent + incremental (only changed sources), not a
full-corpus storm per write.

**E — Elevation of privilege.** Retrieval feeds grounding; it has no publish path
and cannot approve anything.
_Mitigation/AC:_ `Retrieve` is read-only over tenant sources; it writes nothing
to the evidence ledger and has no approval surface to elevate into.

## Acceptance criteria

### Embedding store + pipeline

- [ ] **AC-1.** An idempotent + reversible migration adds a tenant-scoped
      pgvector embeddings table keyed to a source ID + source kind
      (evidence/policy/approved-answer), with four-policy RLS on
      `app.current_tenant`.
- [ ] **AC-2.** An embedding pipeline chunks + embeds new/updated source records
      idempotently (re-embed on source change; incremental, not full-corpus per
      write); embeddings use the slice-498 substrate's local embedding model by
      default (no external embedding API unless the tenant opted into cloud via
      slice 499).
- [ ] **AC-3.** A backfill job embeds the existing corpora going forward without
      blocking writes.

### Retrieval seam

- [ ] **AC-4.** A `Retrieve(ctx, query, topN) → []SourceRef` function returns the
      top-N most-similar **tenant-owned** source IDs under the requesting tenant's
      RLS context; top-N is capped.
- [ ] **AC-5.** `Retrieve` returns only IDs that resolve to real tenant-owned
      rows; the grounding contract guarantees the model can only cite returned
      documents (citations-at-retrieval).
- [ ] **AC-6.** The retrieved candidate IDs are recorded in the slice-498
      `ai_generations` context-inputs for any generation that uses them
      (grounding provenance).

### Swap-in for keyword first-pass

- [ ] **AC-7.** The semantic retriever is exposed behind the same candidate-ID
      contract the v0 surfaces (e.g., slice 441) used for keyword retrieval, so a
      surface adopts it without changing its citation/approval logic.

### Tests

- [ ] **AC-8.** Integration test: a semantically-relevant-but-keyword-disjoint
      source is retrieved (proves semantic > keyword).
- [ ] **AC-9.** **Cross-tenant isolation test:** a tenant-B retrieval returns
      zero tenant-A source IDs even when tenant-A content is the nearest neighbor
      by raw vector distance (threat-model I).
- [ ] **AC-10.** Integration test: `Retrieve` never returns an ID that does not
      resolve to a real tenant-owned row (grounding integrity).
- [ ] **AC-11.** Integration test: re-embed on source change is idempotent (no
      duplicate embedding rows for the same source version).

### Docs / JUDGMENT artifact

- [ ] **AC-12.** Decisions log (`docs/audit-log/500-pgvector-grounding-decisions.md`):
      the embedding-model choice, the chunking strategy, the relevance/top-N
      threshold, and the "Revisit once in use" list.
- [ ] **AC-13.** Dev docs: the grounding-layer contract (Retrieve → assemble →
      Generate), the corpora embedded, and the pgvector-vs-Qdrant deferral note.

## Constitutional invariants honored

- **AI-assist boundary (hard) / citations-at-retrieval.** The model can only cite
  documents the retriever returned (canvas §4.6.5); no fabricated coverage; no
  cross-tenant seeding.
- **#6 RLS tenant isolation** — the embeddings table + every retrieval are
  tenant-scoped; cross-tenant retrieval bleed proven absent (AC-9).
- **#2 ingestion/evaluation separation** — retrieval is read-only over sources;
  it never writes to the evidence ledger.
- **Vector store** — pgvector per the locked tech-stack table; Qdrant is the v3
  large-corpus follow-on (explicitly deferred).
- **Inference backend** — local embedding model by default; cloud embeddings only
  under the slice-499 per-tenant opt-in + banner.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.5 — the grounding stack (pgvector
  embeddings of prior answers + policy chunks + evidence summaries;
  citations-at-retrieval).
- `Plans/canvas/09-tech-stack.md` — Vector store: pgvector (v2 when AI-assist
  lands); Qdrant is a v3 option for large corpora.
- `CLAUDE.md` tech-stack table — "Vector store | pgvector (v2 when AI-assist
  lands) | Qdrant is a v3 option for large corpora."

## Dependencies

- **#498 (this batch — `ready`/unbuilt)** — the `internal/llm` substrate
  (embedding capability + the assembled-context seam the retriever feeds) and the
  `ai_generations` context-inputs field for grounding provenance. **Hard
  dependency — `blocked` until 498 lands.**
- **#441 / #444 / #471** — the surfaces that consume grounding; this slice
  upgrades their retrieval behind the same candidate-ID contract. Build-ordering:
  the surfaces can ship on keyword first-pass (their v0); this slice swaps in
  semantic retrieval afterward.
- **#155 (merged)** — questionnaire CRUD (the approved-answer corpus to embed).

## Anti-criteria (P0 — block merge)

- **P0-500-1.** Does NOT let a retrieval return another tenant's source IDs
  (cross-tenant bleed — proven by AC-9); the embeddings table is RLS-scoped.
- **P0-500-2.** Does NOT let the model cite a document the retriever did not
  return (citations-at-retrieval); `Retrieve` returns only resolvable
  tenant-owned IDs.
- **P0-500-3.** Does NOT ship Qdrant — pgvector only (Qdrant is the v3
  large-corpus follow-on).
- **P0-500-4.** Does NOT send tenant content to an external embedding API by
  default — local embeddings unless the tenant opted into cloud via slice 499.
- **P0-500-5.** Does NOT change the AI-assist approval gates — retrieval feeds
  grounding only; it publishes nothing.
- **P0-500-6.** Does NOT launch an unbounded re-embed — incremental + batched +
  rate-bounded; top-N retrieval is capped.
- **P0-500-7.** Does NOT use vendor-prefixed test fixture tokens; neutral
  `test-*` only.

## Skill mix (3-5)

- `rag-architect` — chunking, embedding-model choice, retrieval-relevance
  threshold, and the citations-at-retrieval grounding contract.
- `database-designer` — the pgvector embeddings table under four-policy RLS +
  the vector index.
- `tdd` — cross-tenant retrieval isolation + grounding-integrity tests are
  load-bearing.
- `security-review` — cross-tenant retrieval bleed is the dominant risk.
- `grill-with-docs` — align the grounding stack with canvas §4.6.5.

## Notes for the implementing agent

- **RLS makes cross-tenant isolation physical, not policy-by-convention.** Run the
  similarity search under `app.current_tenant` so the nearest-neighbor query
  literally cannot see another tenant's vectors — do NOT filter tenant in
  application code after an unscoped search. Prove it with AC-9 (tenant-A content
  as the raw nearest neighbor, still not returned to tenant B).
- **citations-at-retrieval is the grounding crux.** The model gets ONLY what
  `Retrieve` returned; the surface's existing citation validator re-checks
  resolution. Retrieval quality bounds answer quality, but retrieval integrity
  bounds grounding safety — keep them distinct.
- **Build-ordering.** Needs slice 498's substrate (embedding capability + context
  seam). The v0 surfaces can ship keyword-first; this slice upgrades them behind
  the same candidate-ID contract — no surface re-architecture.
- **Registration note (slice-382).** This slice's `_STATUS.md` row is NOT
  registered on this `docs/500` branch; the orchestrator registers it via a
  `chore/status` action after the spec PR merges.
