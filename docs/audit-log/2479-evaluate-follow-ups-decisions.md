# 2479 — Evaluate now follow-ups: decisions log

Issue: mgoodric/security-atlas#2479, the follow-ups from the review of #2469
(#2110, `POST /v1/controls/{id}/evaluate`). Three items: record who ran a
manual evaluation, confirm overlapping evaluations across replicas leave
correct state, and add a per-principal rate limit.

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced; the overlap tests pass
  on `main`'s reconcile without a new lock (D2).
- **detection_tier_target:** integration. A regression in the reconcile's
  locking is caught by `reconcile_overlap_integration_test.go`, which was
  observed failing with the lock removed (D2).

## Decisions

### D1 — the actor is a column on the evaluation row

`control_evaluations.triggered_by TEXT NULL` (migration
`20261238000000_control_evaluation_triggered_by.sql`; the dispatcher
allotted `20261236000000`, but `main` already held `20261237000000`, so the
number moved up to stay strictly highest).

- **Where:** on the ledger row, not in a separate log. The row is the record
  of the run, so the history endpoint and any as-of read carry who ran it
  with no join. `decision_audit_log` already records the POST itself (D4);
  what it cannot say is which evaluation rows that POST produced.
- **What:** `credstore.PrincipalOf(cred)`: the bare users(id) for a person
  (a JWT `user:` session and a user-bound key give the same value), else the
  key's or OAuth client's own id. The same value approval records as a
  requester (#2101).
- **CHECK:** `triggered_by IS NULL OR trigger = 'manual'`. Only a manual run
  has a caller. A manual row may still be NULL: the demo seeder labels its
  rows `manual` and has no caller, and manual rows written before this
  migration have none. A second CHECK bounds the value to 1..256 chars.
- **Engine:** `EvaluateControl` keeps its signature (ingest subscriber,
  scheduler, demo seeder and tests are unchanged) and delegates to the new
  `EvaluateControlBy(ctx, id, trigger, actor, asOf)`. An actor with a
  non-manual trigger is refused in Go with `ErrActorNotManual` before the
  CHECK would refuse it in Postgres.
- **Single-flight joiners.** Concurrent Evaluate now calls for one control
  share one run (#2110 D3). The run's rows name the caller who started it; a
  caller who joined it is not recorded on the rows. Each joiner's POST is
  still in `decision_audit_log`.
- **UI:** `GET /v1/controls/{id}/history` now returns `trigger` and
  `triggered_by`; the control page's audit-log card shows "run by <person>"
  on manual rows through `<Person>`, the one actor resolver (#2085).

### D2 — overlapping evaluations converge; no advisory lock

Single-flight is per process, so across replicas a manual, a scheduled and
an ingest evaluation of one control can run at once. Reading the code:

- There is no mutable current-state table. `control_evaluations` is
  append-only, and current state is the latest run by `evaluated_at`.
  Overlapping runs each append their own rows with their own `eval_run_id`;
  nothing is overwritten.
- `evaluated_at` is stamped before the run reads evidence, so the ingest
  run for a new record (which starts after the push commits) always stamps
  later than any run that could have missed the record.
- The #2109 reconcile (`action_plans_reconcile_verification`) runs as one
  statement inside the evaluation transaction. It takes `FOR UPDATE` on the
  linked plans in id order. A second run blocks on that lock, and after the
  first commits, Postgres hands it the updated row, so it sees `verified`
  and does not move the plan again. It recomputes pass/fail from the latest
  committed run, so whichever transaction commits last writes the status
  the latest run implies.

The dispatcher's instruction was to prove that with a concurrent test
first, and to add `pg_advisory_xact_lock` only if the test showed
divergence. `internal/actionplan/reconcile_overlap_integration_test.go`
runs six evaluations at once (manual, scheduled, ingest mixed):

- `ConcurrentRunsMoveThePlanOnce`: over passing evidence, the plan ends
  `verified` with exactly one system audit row, and each run appended its
  own rows.
- `StateConvergesAsEvidenceArrives`: the ingest run pushes new evidence
  while five others overlap it. After each round the latest run matches
  the evidence, the plan's status matches the latest run, and the audit rows
  form an unbroken chain of real moves. Repeated over four controls.

**Result:** both pass, 40 runs in a row under `-race`. To check the test
can see divergence, the reconcile function was replaced on a scratch
cluster with a copy that drops its `FOR UPDATE`: both tests failed 20 of 20
runs (four to six duplicate `completed -> verified` audit rows). With the
function restored, they passed 20 of 20 again. **No advisory lock was
added.** The existing row lock is the guard, and this test now pins it.

### D3 — per-principal limit, in the handler, per process

- **Where:** `internal/api/controlstate/evaluate_limit.go`, checked after
  the authz gate (a refused 403 spends nothing) and before single-flight (a
  caller who would join a run in flight still counts).
- **Key:** `credstore.PrincipalOf(cred)`, the same value D1 records.
- **Algorithm:** GCRA, one theoretical-arrival timestamp per principal. The
  minute's whole budget may be spent at once; after that one request is
  admitted every `60s / rate`. Entries whose timestamp has passed carry no
  state and are pruned at most once a minute.
- **Default 6/min**, set by `ATLAS_EVALUATE_RATE_PER_MIN` (wired in
  `cmd/atlas`, documented in `docs-site/docs/configuration.md`, both compose
  files and `.env.example`). Six covers someone fixing a control and
  re-checking it several times in a minute; one evaluation can take up to
  30 s, so a single principal can keep at most a few running at once.
- **Over the limit:** 429, `Retry-After` in whole seconds rounded up.
- **Per process.** Each replica keeps its own buckets, so behind N replicas
  a principal can start up to N times the limit, like every other limiter
  here ("Rate limits are per replica" in the configuration docs).
- **Not shared.** There is no shared limiter package: `evidence`, `oauth`
  and `admindemo` each keep their own private bucket, and `ratekey` only
  computes IP keys. Extracting a shared package was out of scope for this
  ticket; the dispatcher files it when this PR lands.

### D4 — the POST is in decision_audit_log with its actor

The authz middleware (`internal/api/authzmw`) writes one
`decision_audit_log` row per request, on allow and on deny, before the
handler runs, with the caller's user id, roles, method and path. So every
`POST /v1/controls/{id}/evaluate`, including refused and rate-limited
ones, is recorded with who made it. Nothing was added there.
