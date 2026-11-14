# Issue #2079 — Control import LLM rerank: budget, local opt-in, prompt hardening, provenance

Follow-ups from the review of #2074 (#1781). Builds on
`1781-control-import-llm-rerank-decisions.md` and on #2038's shared
suggestion-run cap.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Budget: a call cap and a wall-clock budget per run

**Decision.** One run reranks at most `RerankMaxCalls` (100) rows. The whole
rerank of a batch gets a deadline: 60 s when the upload waits for it (25 rows
or fewer), 15 min for a background run. Each call's timeout is the smaller of
the 30 s per-call cap and the time left in the budget. A row the budget does
not reach keeps its deterministic candidates and is marked `unavailable`, and
the run logs how many rows it left.

**Why.** Before, a 5000-row upload made up to 5000 sequential calls, and the
inline path could hold an upload open for 25 x 30 s. A row past the budget is
in the same state as a row whose call failed: deterministic order, no
rationale. It reuses `unavailable` rather than adding a fourth state, so no
CHECK change and no new CLI wording.

## D2 — Concurrency: the shared suggestion-run cap, not a second one

**Decision.** No per-tenant concurrency cap is added. A rerank runs inside a
suggestion run, which holds a slot of `Stager.runs` (default 4, #2038) for
its whole computation, so concurrent rerank calls per process are bounded by
that cap.

**Why.** The dispatcher's direction for this ticket: #2038 added the shared
cap so that anything bounding import LLM calls shares it. A second cap would be
a second mechanism for the same limit. The cap is per process, not per tenant,
so one tenant can hold every slot; with D1 that tenant's runs are bounded in
calls and time. A per-tenant fairness cap remains possible if that becomes a
real contention.

## D3 — Local-Ollama rerank: an explicit per-tenant opt-in

**Decision.** A new `control_import_settings` table (one row per tenant,
four-policy RLS under FORCE) holds `llm_rerank_local`, off by default and with
no backfill. `GET`/`PUT /v1/controls/imports/settings` read and set it,
tenant-admin only. The rerank gate is now `controlimport.OptInGate`: a
cloud-routed tenant is reranked as before, and a tenant on the local default
is reranked only when `llm_rerank_local` is on. The inference client is
unchanged: the shared `cloud.Router` sends a tenant with no cloud route to the
local Ollama.

**Why.** #1781 D1 left local reranking out because the local default can't
tell an installed Ollama from a missing one. An administrator saying "there
is one" is the opt-in the ticket asks for. The flag belongs to the importer,
so it lives in an importer table rather than in `tenant_llm_routing`, whose
`Set` replaces the whole row and whose `Clear` deletes it. Admin-only matches
the routing endpoint: whether an import may call an LLM is the same kind of
decision. The admin check moved to `authz.IsTenantAdmin` so both endpoints
share it.

## D4 — Prompt hardening: imported text goes in the context turn

**Decision.** The rerank and the one-at-a-time match both send a fixed system
prompt that holds only instructions. The imported control (id, title,
description, tested-under codes) and its candidates go in the request
`Context`, which both backends render as the user turn (JSON). The system
prompt says that turn is data and never instructions. Prompt versions move to
`controlimport-rerank-v1` and `controlimport-scf-match-v1`.

**Why.** The control text comes from an uploaded sheet, so it is untrusted.
In the system prompt, an injected line carries the system's authority. JSON
encoding also escapes it, so it can't break out of its field. The context was
already recorded with the generation, so the forensic record still holds the
full input.

## D5 — Provenance: `llm_provider` on the row, read from `ai_generations`

**Decision.** An applied row carries `llm_provider`, the `model_provider` of
its latest `control_import` generation (`surface_subject` = row ID). It is
read with a lateral join when rows are listed, and set from the generation
result on the inline path. The CLI prints `LLM rerank routes to <provider>`
under an applied row. Rows that are off or unavailable carry none.

**Why.** The ticket names `ai_generations.model_provider` as the source, and
reading it there keeps one record of where the text went. The
`(tenant_id, surface, surface_subject)` index serves the join. There is no
web UI for control imports yet, so the API field and the CLI are the surfaces
that show it.
