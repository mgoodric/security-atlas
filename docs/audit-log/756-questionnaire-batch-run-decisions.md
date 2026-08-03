# Slice 756 — Questionnaire Batch Answer-Run Decisions

## Decision Summary

Slice 756 implements inbound questionnaire batch answer drafting as a tenant-scoped run record over the existing `qaisuggest.Service.Suggest` single-row path. It supersedes slice 441's P0-441-7 batch-out anti-criterion as slice discipline only; the runtime AI-assist boundary remains unchanged.

## D1 — Execution Model

**Decision:** request-scoped synchronous execution with concurrency 1.

The batch starts a `questionnaire_answer_runs` row, transitions it to `running`, enumerates questions in display order, and calls `qaisuggest.Service.Suggest` once per eligible row before returning the final run detail. No detached goroutine survives the request.

**Rationale:** tenant context lifetime is the primary new risk. Keeping the executor request-scoped means the same context that passed the questionnaire role gate spans retrieval, generation, citation resolution, draft persistence, and run/item writes. There is no background context that can outlive a tenant switch or restart.

**Rejected:** detached background goroutine. It would improve responsiveness, but it needs a durable tenant-context construction and restart reconciliation pattern that this repo does not yet have.

## D2 — Restart Semantics

**Decision:** no restart recovery job ships in v0.

Because execution is request-scoped, a process restart kills the request and leaves any already-written item rows preserved. The run can be marked failed only while the request is alive; a future detached-worker slice should add startup reconciliation for stale `running` rows before changing the execution model.

## D3 — Row Cap

**Decision:** `AnswerRunRowCap = 100`.

Rows beyond the cap are recorded as item outcome `error` with reason `row_cap_exceeded`. This is intentionally visible in the run record rather than silently truncating enumeration. The value is stored on each run row so old runs keep their original cap meaning after future tuning.

## D4 — Outcome Vocabulary

**Decision:** run items use the fixed outcomes from the slice spec:

`drafted`, `insufficient_evidence`, `suppressed`, `skipped_needs_mapping`, `skipped_already_answered`, `error`.

Reason codes are fixed and non-sensitive. Backend details are bounded and only recorded for infrastructure `error` outcomes.

## D5 — Detection-Tier Fields

Run rows carry denormalized counts for fast status rendering, but run items are the record. The implementation recomputes counts from items before terminal status. Item rows record `question_id`, `answer_id` only for drafted rows, `sort_order`, outcome, reason, and timestamps.

## D6 — Approval Boundary

The batch path never writes approval columns. Draft persistence remains inside `qaisuggest.Store.PersistDraft`, which writes `ai_assisted=true`, `human_approved=false`, and `human_approver=NULL`. The batch skips any question with an existing answer, so it does not overwrite manual or previously drafted answers.

## Revisit List

- Add a detached worker only after a durable tenant-context construction and stale-run reconciliation pattern exists.
- Revisit the row cap after SIG/Core-size customer data is available.
- Add frontend review-queue ergonomics in slice 757.
- Keep export gating in slice 758; batch runs do not export.
