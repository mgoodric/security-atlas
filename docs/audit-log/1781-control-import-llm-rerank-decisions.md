# Issue #1781 — Control import: optional LLM reranking with rationales

Part of #1734 (bring-your-own-controls import). Builds on #1780's
deterministic candidates.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — "Opt-in allows it" means the tenant routes to a cloud provider

**Decision.** The importer calls the LLM only when the tenant's
`tenant_llm_routing` config names a cloud provider (`cloud.MaskedConfig.IsCloud`).
A tenant with no routing row, or on `local-ollama`, is not reranked.

**Why.** The ticket gates the rerank on "an LLM provider is configured **and**
the organisation's cloud-LLM opt-in allows it". The routing config is the only
per-tenant LLM opt-in the platform has. A cloud route needs a stored provider
key, so it covers "a provider is configured" too. The local default can't tell
an installed Ollama from a missing one, and the rerank must never slow or break
an import on an install with no LLM. A tenant that wants local reranking is a
follow-up, gated on an explicit local opt-in.

## D2 — Source stays the origin; `ai_assisted` marks LLM placement

**Decision.** `control_import_candidates.source` keeps `crosswalk | keyword`.
A candidate the LLM placed and explained gets `ai_assisted = true` and a
`rationale`. The table adopts `ai_assist_human_approver_guard` (slice 498), as
#1780's D6 said it would, plus `rationale IS NULL OR ai_assisted`.

**Why.** The spec's `llm` source would overwrite where an anchor came from.
Reranking only reorders, so the anchor still came from the crosswalk or from
keyword search, and that origin is what tells a reviewer how much to trust it.
`ai_assisted` is the platform's existing name for "an LLM touched this".

## D3 — Unusable answers keep the deterministic list

**Decision.** The LLM returns `{"ranking":[{"scf_id","rationale"}]}`. Anchors
not in the row's list, and repeats, are skipped. Candidates the answer omits
keep their deterministic order after the ranked ones. If the answer is not
JSON, or names no listed anchor, the row is `unavailable` and its candidates
are unchanged. Rationales are flattened to one line and capped at 400 runes.

**Why.** The ticket's invariant is that the anchor set never changes. Dropping
an omitted candidate would remove a suggestion. Inventing one would add an
anchor the reviewer can't trust.

## D4 — Row state: `llm_rerank` = `off | applied | unavailable`

**Decision.** A row column, returned as `llm_rerank`. `unavailable` is the
ticket's "the row notes that no rationale is available". The CLI prints
`LLM rerank unavailable: deterministic order, no rationale`.

## D5 — One generation per row, and one failure stops the batch

**Decision.** Each row with candidates is one `Generate` call under
`llm.SurfaceControlImport` (`control_import`), recorded in `ai_generations`
with `surface_subject` = the row ID. After the first failed call, the remaining
rows are marked `unavailable` without calling. A draft that can't be recorded
is discarded.

**Why.** Per-row prompts stay within the token cap. The circuit breaker means
an outage costs one timeout per batch, not one per row. An unrecorded draft
would break the forensic-record rule (slice 498).

## D6 — Reranked batches go to the background sooner

**Decision.** When the rerank is on, a batch with more than 25 staged rows
computes its suggestions in the background (`suggestions_status: pending`),
not only above the usual 200.

**Why.** One LLM call per row is too slow to hold the upload request open for
200 rows.

## D7 — The existing one-at-a-time suggester uses the import surface too

**Decision.** `controlimport.Service.Suggest` now labels its generations
`control_import`, not `questionnaire`.

**Why.** The spec's Implementation Decisions replace the borrowed
questionnaire label. Leaving one caller on the old label would split import
usage across two surfaces.

## D8 — Down migration restores the surface CHECK as NOT VALID

**Decision.** The down migration re-adds the five-surface CHECK with
`NOT VALID`.

**Why.** `ai_generations` is append-only, so `control_import` rows written
while the migration was applied remain. `NOT VALID` lets the rollback succeed
and still rejects new `control_import` rows.
