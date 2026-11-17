# 2339 — Module switch follow-ups: decisions log

Issue: mgoodric/security-atlas#2339 (follow-ups from the review of #2317;
spec #2166). Builds on #2317 (#2170), #2346 (#2171) and #2535 (#2175).

## Detection-tier classification

- **detection_tier_actual:** manual_review. The two behaviour gaps (no
  audit row for a switched-off attempt; a flag-store error falling open)
  were found in the review of #2317.
- **detection_tier_target:** integration. Both are now pinned by tests:
  `TestModuleGate_SwitchedOffAttemptIsAudited` (real Postgres, observed
  failing with the recorder unwired, then passing) and
  `TestModuleGateFailsClosedOnStoreError` (unit).

## Already done on `main` before this ticket

| Ticket item                                        | Landed in     | State                                                                                                                                                                                                                                                                  |
| -------------------------------------------------- | ------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Route guard test; empty `root.Group(...)` wrappers | #2346 (#2171) | The wrappers are gone from `register_board.go` and `register_oscal.go`; `TestEveryRouteBelongsToAModule` (`internal/api/modules_route_guard_test.go`) fails on any registered route `featureflag.Resolve` cannot place, which covers every route those files register. |
| `featureflag.Gate` primitive                       | #2346         | Deleted. The comments the ticket named now describe `ModuleGate`. The one stale mention left, in `featureflag/store.go`, is fixed here.                                                                                                                                |
| Answer runs kept running after a switch-off        | —             | Decided below (D4).                                                                                                                                                                                                                                                    |

## Decisions

### D1 — a switched-off attempt writes a `decision_audit_log` row

`ModuleGate` runs before the OPA step, and the OPA step is what audits every
other decision, so a request to a switched-off module left no trace. The
gate now takes a recorder (`featureflag.WithAttemptRecorder`), called after
the 404 is written. The server wires one that writes a row through the same
`authz.AuditWriter` the OPA step uses:

| Column                                                              | Value                                                           |
| ------------------------------------------------------------------- | --------------------------------------------------------------- |
| `result`                                                            | `deny`                                                          |
| `reason`                                                            | `module_disabled`                                               |
| `resource_type` / `resource_id`                                     | `module` / the module key (`questionnaires`)                    |
| `policy_hits`                                                       | `{module_gate:<switch that is off>}` (module or feature switch) |
| `user_id`, `user_roles`, `action`, `request_path`, `request_method` | as the OPA step writes them                                     |

The response is already sent, so a failed write is logged at ERROR and the
404 stands. A switched-off module is refused whether or not its attempt is
recorded.

### D2 — dedupe: one row per (tenant, actor, switch) per five minutes

A client polling a switched-off page would otherwise write a row per poll.
The recorder keeps an in-process map keyed by tenant, actor and switch, and
writes a row only for the first attempt in each five-minute window. The map
is capped at 10,000 entries: past the cap, expired entries are pruned, and
if that frees nothing the map is reset. A reset can write an extra row; it
can never skip one. Dedupe is per process, so N replicas write up to N rows
per window, which is still bounded.

Rejected: a token-bucket rate limit (drops rows under load, the opposite of
what an audit trail wants) and a DB-side dedupe (a read per refused
request, on the path that is already refusing).

### D3 — a flag-store error answers 503, for every module

Before: `Store.Get` swallowed a DB error and returned the Seed default, so
the gate served a default-on module (Questionnaires, Risk, ...) that the
tenant might have switched off, and 404'd a default-off one (Board
reporting) that the tenant might have switched on. Both guess the tenant's
switch state.

Now the gate reads switches without the default fallback. A failed read
answers **503** `{"error":"module_state_unavailable","retry_after":5}` with
`Retry-After`, logged at ERROR and counted on
`atlas.module_gate.state_errors` (OTel, attribute `module`). It does not
serve the module, since the tenant may have switched it off, and it does not
404, since that would say the tenant switched it off. A core route reads no
switch and is served as before.

Mechanics: `Store.Get` still returns the Seed default, now alongside an
error wrapping `featureflag.ErrStoreUnavailable`. `Enabled` keeps slice 059's
anti-criterion P0 for its callers (feature checks inside handlers,
background jobs per #2175 D4): it swallows the error and answers the
default. Only the gate and `RouteOn` read through the strict path.
`RouteOn` backs the combined views (`moduleaccess.Viewer.Sees`) and
`GET /v1/approvals`, so on a flag-store error those now return an error
(500) rather than list items from a module whose state is unknown.

This supersedes #2171 D6 (fail-open for every module). The rule for later
modules is recorded on #2166: a request to an optional module whose switch
cannot be read answers 503; it never falls back to the default.

Not changed: `Store.List` (behind `GET /v1/me/modules` and Admin ›
Features) still degrades to the defaults, so the sidebar can show a module
the gate then 503s. The page shows the error rather than the module's
data, and the response that matters is the gate's.

### D4 — an answer run in progress is not stopped by a switch-off

`AnswerRunService.Start` runs synchronously inside the
`POST .../answer-runs` request, which passed the gate when it arrived. It is
bounded by the row cap (`AnswerRunRowCap`), writes only AI-drafted answers
that still need human approval, and those drafts are reachable only through
Questionnaires routes, which the gate now refuses. Stopping it mid-run would
mean a switch read before every row for a window of seconds; a run started
after the switch-off is refused at the gate. Revisit if answer runs move to
a background worker, which then follows #2175's background-job rule.

## Acceptance checks

| Check                           | Command                                                                                     | Result                                                                     |
| ------------------------------- | ------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------- |
| Gated attempt audited, deduped  | `go test -tags=integration -run TestModuleGate_SwitchedOffAttemptIsAudited ./internal/api/` | fails with the recorder unwired (`module_disabled rows = 0`), passes wired |
| 503 on a flag-store error       | `go test -run TestModuleGateFailsClosedOnStoreError ./internal/featureflag/`                | pass                                                                       |
| Recorder sees each refusal      | `go test -run TestModuleGateRecordsGatedAttempts ./internal/featureflag/`                   | pass                                                                       |
| Dedupe window and cap           | `go test -run TestGatedAttemptAuditor ./internal/api/`                                      | pass                                                                       |
| Every route belongs to a module | `go test -run TestEveryRouteBelongsToAModule ./internal/api/`                               | pass                                                                       |
