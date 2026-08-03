# Slice 755 — Questionnaire SCF Mapping Decisions

## D1 — Retrieval Scoring

The v0 retriever uses keyword overlap across the current seeded SCF catalog
(`scf_id`, family, title, description), then ranks in Go by distinct keyword
matches. This mirrors the slice-441 first-pass discipline without pgvector.
The candidate set is capped at eight anchors and each description is bounded
before it reaches the prompt.

Rejected: embeddings/vector search. It is explicitly outside this slice and
would widen the infra dependency surface.

## D2 — Single Pick

The model returns one JSON pick: `scf_id` plus a one-sentence rationale. A
ranked list was rejected for v0 because the workflow needs one-click approve
and every proposal is still human-reviewed before it becomes canonical.

## D3 — Validation Tier

A model pick is accepted only when both checks pass before persistence or
render:

- The `scf_id` is in the exact candidate grounding set sent to the model.
- The `scf_id` resolves to a real row in the current SCF catalog.

Failures suppress the proposal with fixed reason codes and write no proposal
row. This is the qaisuggest citation gate translated from cited evidence IDs
to SCF anchor IDs.

## D4 — Re-Suggest Semantics

Each suggest request creates a new pending proposal when valid. Rejection marks
the proposal rejected; it is no longer active, and the operator can ask again.
The rejected row stays for auditability rather than being physically deleted.

## D5 — Audit And Provenance

The shared `ai_generations` ledger records the raw model generation and prompt
context. `questionnaire_mapping_proposals` carries prompt/model provenance, and
`questionnaire_mapping_proposal_audit` records suggest/approve/reject/suppress
events with the same provenance fields.

## D6 — Revisit List

- Add a manual SCF picker for rows where the model returns `no_candidates`.
- Consider top-three ranked proposals only if operators ask for comparison.
- Revisit retrieval quality once the project intentionally adds vector
  retrieval for questionnaire AI surfaces.

## D7 — Tenant Relationship Enforcement

The proposal and proposal-audit tables use composite `(tenant_id, question_id)`
foreign keys back to `questionnaire_questions`. RLS scopes reads and writes, but
the composite FK makes cross-tenant proposal/question pairing impossible at the
database tier even if a future service path passes a mismatched UUID.
