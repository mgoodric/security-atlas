# #3005: Classify deterministic errors in the evidence-ingest subscribers (decisions log)

These are the follow-ups to #2964 (PR #3003). Every consumer on the
`EVIDENCE_INGEST` stream runs with `MaxDeliver: -1`, so a callee error that
fails the same way on every delivery, and that the subscriber NAKs, loops
forever. This log does not block merge.

- detection_tier_actual: manual_review (the #3003 review named the class; the
  audit below found the instances)
- detection_tier_target: unit

## D1: What the audit found, per subscriber

The rule throughout: an error is deterministic only when it comes from what
is stored, so the same delivery reads the same rows and fails the same way.
Connection errors, statement timeouts, context cancellation and query
runtime errors stay transient. When the audit could not show an error was
deterministic, it stays a NAK.

| Subscriber                     | Deterministic failure found                                                                                                                                                                                                                                                    | Now                                                                                                                                        |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------ |
| `eval/consumer`                | An unknown control. `scope.Store.ControlApplicability` runs before `loadControl` and returned an unwrapped `"scope: control %s not found"`, so `ErrControlNotFound` was never reached and the message NAKed forever.                                                           | `scope.ErrControlNotFound`, translated to `eval.ErrControlNotFound`: Ack, as before for that sentinel.                                     |
| `eval/consumer`                | A malformed `applicability_expr`, `evidence_queries` or exception predicate; a query with an empty expression or unsupported language; a Rego or JSONPath query that does not compile; a SQL query the static guard refuses; a query whose result is not a valid result value. | `eval.ErrUnevaluable`: Term, Warn log, `atlas.eval.control_unevaluable` counter.                                                           |
| `personnelsecurity/subscriber` | A record bound to a control UUID the tenant does not have. The checklist's only foreign key, `(tenant_id, control_id)`, refuses the insert on every delivery.                                                                                                                  | `personnelsecurity.ErrControlNotFound` (SQLSTATE 23503 on that insert): Term, Warn log, `atlas.personnelsecurity.unknown_control` counter. |
| `freshnessdrift/worker`        | None. `freshness.Refresh` and `drift.CaptureSnapshot` can fail on a query, an FK to a control deleted mid-transaction (a race, so a retry passes) or a CHECK whose inputs the Go code always satisfies. The tenant header is already Termed.                                   | Unchanged: NAK.                                                                                                                            |

A query result that is not a valid value (for example, a Rego policy that
leaves `result` undefined for some input) depends on the records as well as
the query. It is still classified as unevaluable. A redelivery of the same
message reads the same records. New evidence arrives as its own message and
is evaluated on its own, and the hourly scheduler sweep re-evaluates every
control, so terming this message drops no evaluation that would otherwise
happen.

A Rego compile error while the context is already done is left transient:
`PrepareForEval` takes the context, so that "compile" error may be the
cancellation.

## D2: `ErrUnevaluable` wraps without changing messages

The sentinel is attached with a small `unevaluableError` wrapper whose
`Error()` is the inner message and whose `Unwrap()` returns both the inner
error and `ErrUnevaluable`. A second `%w` in each `fmt.Errorf` would have
prefixed every message with the sentinel text and broken the existing
message assertions and API error bodies. `ErrRegoQuery`, `ErrSQLQuery` and
`ErrJSONPathQuery` still match through the wrapper.

## D3: controlBreakdown (item 2)

`controlBreakdown` calls three engine methods.

- `EvaluateControl` (the recompute before reading) failing with
  `ErrUnevaluable` falls through, the same way `ErrControlNotFound` already
  did, and the breakdown reads the evaluations the control already has. No
  new evaluation row is coming for that control. Returning an error would
  make every risk linked to it underivable on every event for any sibling
  control, and the eval subscriber already logs and counts the control.
- `Effectiveness` and `ControlState` both call `loadControl`, which parses
  `evidence_queries`. When that parse fails, the read itself cannot happen.
  A zero in its place would report a measured 0% effectiveness, so the error
  becomes `ErrUnderivable` and the residual subscriber terms it (the #3003
  path). Their other errors are database errors and stay transient.

## D4: Recompute after a score PATCH (item 3), inline

`PATCH /v1/risks/{id}` did not touch the residual. A risk whose score was
fixed kept its stale residual (or none) until the next evidence for one of
its controls. There is no risk-change subject on NATS and no publisher to
reuse, so the handler recomputes inline after the update commits, as
`POST /v1/risks/{id}/controls` already does. Adding a risk event stream
would mean a new subject, stream config and consumer for one caller.

- It runs only when the PATCH set `inherent_score` or `methodology`, and
  only for a risk with linked controls, the same risks the subscriber
  recomputes. For a risk with none, the residual the update wrote stays as
  it is (for FAIR, the inherent exposure from `applyRecordFields`). The
  deriver would otherwise replace it with a `no_controls_linked` blob.
- The update has committed when it runs. A failed recompute is logged and
  the PATCH still returns 200: a 500 would invite a retry with the old
  If-Match, which then fails 412. The next control event recomputes it.
- The residual write moves `updated_at`, so the handler re-reads the risk
  and the response's ETag matches a following GET.

## D5: Backoff (item 4): growing NAK delay, no stream or consumer change

`streambuf.RedeliveryDelay` doubles the NAK delay per delivery from 2s to a
5-minute cap, read from the message's `NumDelivered`. All four subscribers
use it, and so does the ledger writer on the same stream, which had the
same fixed 2s NAK. The first retry is still 2s, so a transient blip
recovers as fast as before. An error misclassified as transient now costs
one attempt every five minutes instead of one every two seconds.

Not done, because each changes operations behaviour: a bounded
`MaxDeliver`, the consumer-level `BackOff` list (which JetStream requires
`MaxDeliver` for), and a dead-letter subject. Each needs a new consumer
config on running deployments and a retention decision for the DLQ. With
the classifications above, the remaining NAKs are errors that can pass on
a retry.
