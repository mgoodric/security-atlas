# 756 — Questionnaire batch answer-drafting run (state machine + orchestrator)

**Cluster:** AI-assist / Questionnaires
**Estimate:** L (2d)
**Type:** JUDGMENT (run execution model + outcome vocabulary)
**Status:** `ready`

## Narrative

Filed from the OE-595 end-to-end questionnaire-answering design (2026-07-28
repo review). Slice 2 of 4; see #755 (mapping suggest-approve), #757 (review
queue UI, parented on this slice), #758 (approval-gated export).

Slice 441 shipped the single-answer tracer bullet — retrieve, draft via local
Ollama, validate every citation, persist an unapproved draft, approve
one-click — and deliberately scoped batch OUT (P0-441-7: "No batch 'answer all
rows'"). That anti-criterion was slice-scope discipline, not a constitutional
line; the canvas roadmap explicitly names "Inbound questionnaire batch
processing" as the v2 continuation (§4.6.8), and the operator reality is that
a SIG has hundreds of rows — clicking "suggest" one row at a time is not a
workflow. This slice supersedes P0-441-7 with the batch layer while keeping
every boundary invariant exactly where slice 441 put it.

The design: a **run** is a first-class, tenant-scoped record. Starting a run
enumerates the questionnaire's unanswered, mapped questions in display order
and drives `qaisuggest.Service.Suggest` for each — sequentially (concurrency
1: local Ollama on commodity hardware; a parallel fan-out would just queue at
the GPU and multiply timeout ambiguity). Each question lands one run-item
outcome from a fixed vocabulary: `drafted`, `insufficient_evidence`,
`suppressed`, `skipped_needs_mapping`, `skipped_already_answered`, `error`.
The run status machine is `pending → running → completed | failed | canceled`.
One active run per questionnaire (409 on a second start). A finished run is an
immutable record of what the batch did; re-running later skips
already-answered rows, so the operator can map more questions (slice 755),
add evidence, and re-run to fill the gaps.

Everything a run produces is an UNAPPROVED draft — the run only multiplies
slice 441's per-row machinery; it approves nothing, publishes nothing, and a
row that fails the citation gate is recorded `suppressed` with nothing
persisted, identical to the single-row path.

## Threat model (STRIDE) — HEAVY (AI-assist family)

**S — Spoofing.** Run start/status endpoints reuse the questionnaire auth +
JWT role gate.

**T — Tampering / hallucination.** Batch changes nothing about per-row
grounding: every draft passes the qaisuggest citation gate (in-grounding AND
tenant-owned) before persistence. A batch cannot lower the bar because it
calls the same `Suggest`. **Mitigation is inherited, and AC-tested at the
batch tier.**

**R — Repudiation.** Which run drafted what, under which model.
**Mitigation:** run + run-item rows carry timestamps, started_by, and each
drafted item references its answer row, whose ai_generations audit trail
(prompt version, model name/version/provider) slice 441 already writes.

**I — Information disclosure / cross-tenant bleed (PRIMARY at batch scale).**
A long-lived background execution must not outlive or escape its tenant
context. **Mitigation:** the run executor carries the requesting tenant's RLS
context for its whole lifetime; every retrieval/persist inside is the existing
RLS-bound path. Two-tenant batch isolation is a load-bearing AC.

**D — Denial of service (PRIMARY new risk).** A 300-row SIG × 45s generation
timeout is hours of GPU. **Mitigation:** sequential execution, one active run
per questionnaire, per-row timeout inherited from qaisuggest, cancel endpoint,
and a run-level row cap with a fixed `error` outcome past it (cap value is a
JUDGMENT call, recorded).

**E — Elevation of privilege.** Batch must not become an approval bypass.
**Mitigation:** the run NEVER touches approval columns; the DB CHECK
(`ai_assist_human_approver_guard`) stands on every draft; export gating is
slice 758.

## Acceptance criteria

**Schema + state machine**

- [ ] **AC-1.** Migration adds `questionnaire_answer_runs` (status:
      `pending|running|completed|failed|canceled`, counts, `started_by`,
      timestamps) + `questionnaire_answer_run_items` (question_id, fixed-vocab
      outcome, nullable answer_id, fixed reason code) — both tenant-scoped
      under RLS, idempotent + reversible.
- [ ] **AC-2.** Legal status transitions are enforced (no completed→running;
      cancel only from pending/running); one active run per questionnaire
      (second start = 409).

**Orchestrator**

- [ ] **AC-3.** A run enumerates the questionnaire's questions in display
      order and, for each unanswered mapped question, drives
      `qaisuggest.Suggest` under the requesting tenant's context; outcomes map
      1:1 onto the fixed vocabulary (drafted / insufficient_evidence /
      suppressed with reason).
- [ ] **AC-4.** `needs_mapping` questions are skipped and recorded
      `skipped_needs_mapping` (they become draftable after slice-755 approval + a re-run); already-answered questions are recorded
      `skipped_already_answered` and their answers untouched.
- [ ] **AC-5.** Execution is sequential (concurrency 1) with the per-row
      timeout inherited from qaisuggest; a mid-run infrastructure failure
      marks the run `failed` with completed items preserved; cancel stops
      before the next row and marks `canceled`.
- [ ] **AC-6.** Every draft a run produces is unapproved
      (`ai_assisted=true, human_approved=false`); the run writes NO approval
      column, ever.

**API**

- [ ] **AC-7.** `POST /v1/questionnaires/{id}/answer-runs` starts a run;
      `GET .../answer-runs/{runId}` returns status + per-item outcomes;
      cancel endpoint per AC-5. All under the questionnaire role gate.

**Tests**

- [ ] **AC-8.** Integration (stub LLM): a mixed questionnaire (mapped-unanswered,
      needs_mapping, already-answered, insufficient-evidence, gate-failing)
      yields exactly the expected outcome per row; gate-failing rows persist
      nothing and read `suppressed`.
- [ ] **AC-9.** **Two-tenant isolation:** Tenant A and Tenant B each run a
      batch over look-alike questionnaires; no Tenant-B draft cites or quotes
      any Tenant-A evidence/policy row, and neither tenant can read the
      other's run or run-items.
- [ ] **AC-10.** Integration: after a run, zero rows violate
      `ai_assisted ⇒ (human_approved=false OR human_approver IS NOT NULL)` and
      zero drafts are approved.
- [ ] **AC-11.** Integration: second start while running returns 409; re-run
      after completion skips previously drafted/answered rows.

**Docs / JUDGMENT artifact**

- [ ] **AC-12.** Decisions log at
      `docs/audit-log/756-questionnaire-batch-run-decisions.md` (execution
      model: request-scoped vs background goroutine + tenant-context
      propagation; row cap; outcome vocabulary; detection-tier fields; revisit
      list) + changelog entry. The P0-441-7 supersession is recorded
      explicitly.

## Constitutional invariants honored

- **AI-assist boundary (hard).** The batch multiplies the suggest surface only;
  mandatory citations and the no-fabricated-coverage path are inherited per
  row; nothing is published or approved by the run.
- **#6 — Tenant isolation via RLS.** Run + item tables RLS-scoped; batch-tier
  bleed proven absent (AC-9).
- **#9 — Manual evidence is first-class.** `insufficient_evidence` rows are
  surfaced for manual authoring, not papered over.
- **Inference backend.** Local Ollama default (sequential execution is sized
  for it); cloud stays per-tenant opt-in with the banner (surfaced in #757).

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6.4 (inbound flow), §4.6.5 (AI
  boundary), §4.6.8 roadmap ("v2 — Inbound questionnaire batch processing").
- `CLAUDE.md` "AI-assist boundary (hard)".
- `docs/issues/441-questionnaire-ai-answer-suggestion-v0.md` — the per-row
  contract this slice batches; P0-441-7 supersession.

## Dependencies

- **#441** (qaisuggest per-row suggest + citation gate + provenance) —
  `merged`. The run is a driver of this service, not a re-implementation.
- **#155** (questionnaire primitives) — `merged`.
- NOT dependent on #755 (unmapped rows are skipped-and-reported) or #758.
  **#757 is parented on this slice.**

## Anti-criteria (P0 — block merge)

- **P0-756-1.** Does NOT approve, publish, or export anything — drafts only.
- **P0-756-2.** Does NOT bypass or weaken the per-row citation gate (no
  "batch mode" flag on qaisuggest).
- **P0-756-3.** Does NOT run generations for another tenant's rows or leak run
  state across tenants.
- **P0-756-4.** Does NOT overwrite an existing answer (approved or manual).
- **P0-756-5.** Does NOT parallelize generations in v0 (sequential; the GPU is
  the bottleneck and ordering keeps the run legible).
- **P0-756-6.** Does NOT bundle SIG/CAIQ templates — customer-supplied ingests
  only.

## Skill mix (3-5)

`grill-with-docs` · `tdd` (outcome-matrix + two-tenant tests load-bearing) ·
`database-designer` (run/item schema + transitions) · `security-review`
(tenant-context lifetime on a long-running execution) · `simplify`.

## Notes for the implementing agent

- The load-bearing JUDGMENT call is the execution model. Options: (a)
  synchronous request with a hard row cap; (b) background goroutine with an
  explicitly-constructed tenant context detached from the request. Either is
  acceptable if the tenant context provably spans the whole run and a server
  restart leaves the run recoverable (`failed` or resumable) — record the
  call + restart semantics in the decisions log.
- Do not invent a second suggestion path: the run calls
  `qaisuggest.Service.Suggest` verbatim. If the batch needs anything from
  qaisuggest, widen its public seam, don't fork it.
- Outcome counts on the run row are denormalized convenience; the items are
  the record.
