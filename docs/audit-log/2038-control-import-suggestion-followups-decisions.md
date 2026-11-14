# Issue #2038 — Control import suggestions: stuck pending batches, pinned keyword retrieval, composite row FK

Follow-ups from the review of #2035 (#1780), after #2074 (#1781) added the
LLM rerank.

- `detection_tier_actual`: manual_review
- `detection_tier_target`: integration

## D1 — The sweeper fails stale batches; it does not re-run them

**Decision.** `controlimport.Sweeper` marks a batch `failed` when its
suggestions have been `pending` for more than 30 minutes. It never recomputes
them. The operator re-uploads.

**Why.** The ticket allows either. Re-running from a sweeper would start LLM
rerank calls with no request behind them, outside the caller's budget (#2079),
and needs the fully wired `Stager` (reranker, inference client, opt-in gate),
which today exists only inside the API server. Failing is one idempotent
`UPDATE`. A batch read as `failed` tells the operator the truth. A batch read
as `pending` forever does not.

## D2 — The cutoff is generous, and a late run still wins

**Decision.** `DefaultStaleSuggestionsAfter = 30m`, swept at start and every
10 minutes. Age is measured from `created_at`, since a batch is pending from
the moment it is created. If a slow run finishes after its batch was failed,
`SaveSuggestions(..., Ready)` still writes, and the batch reads `ready`.

**Why.** A batch of thousands of rows with rerank can take minutes. A
heartbeat column would let the cutoff be tight, but it adds a write per row
batch for a failure that only a restart causes. The late-win rule means a
false positive costs a short window of `failed`, never lost candidates.

## D3 — Two pools, tenant context for the write, SKIP LOCKED for replicas

**Decision.** Same shape as the staleness scheduler. The migrator pool lists
the tenant IDs that have stale batches and does nothing else. Each tenant's
update runs on the app pool under `tenancy.WithTenant`, so RLS binds the
write. The update claims rows with `FOR UPDATE SKIP LOCKED` and re-checks
`suggestions_status = 'pending'`. Two replicas sweeping at once fail each
batch once and never block each other. A partial index on `created_at WHERE
suggestions_status = 'pending'` keeps the listing cheap.

## D4 — One semaphore covers every suggestion run, inline and background

**Decision.** `Stager.runs` (default 4, `WithRunLimit` to change) is taken by
`suggest` for the whole run, LLM rerank included, whether the run is inline
in the upload request or in the background. A run that cannot get a slot
before its context ends marks the batch failed.

**Why.** The review asked for a cap on concurrent runs. Capping only the
background path would let inline runs, which also call the LLM, bypass it.
One cap at the run level is what #2079's call budget can share, rather than
adding a second limiter beside it.

## D5 — Every error path marks the batch failed, with a detached context

**Decision.** `markFailed` writes `failed` using
`context.WithoutCancel(ctx)` with a 10-second timeout. It runs when the
computation fails, when the final `ready` save fails, and when no slot frees
up.

**Why.** The ready save most often fails because the request context ended or
the database blipped. A mark that reused the dead context would fail too and
leave the batch pending, which is the bug. A mark that still fails is
reported in the returned error, and the sweeper is the backstop.

## D6 — Keyword retrieval takes the release; the questionnaire path is unchanged

**Decision.** `qmapsuggest.Store` gains `RetrieveCandidatesInRelease`.
`RetrieveCandidates` keeps reading the current release, so questionnaire
mapping behaves as before. The importer passes `b.SCFRelease.VersionID`.

## D7 — Composite candidate-to-row FK

**Decision.** `control_import_rows` gains `UNIQUE (tenant_id, batch_id, id)`.
`control_import_candidates` drops its `row_id`-only FK for
`(tenant_id, batch_id, row_id)` with `ON DELETE CASCADE`, the same shape as its
batch FK. Migration `20261010000000`.
