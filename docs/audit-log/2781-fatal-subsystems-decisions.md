# #2781 — fatal subsystems in `cmd/atlas`: decisions log

detection_tier_actual: manual_review
detection_tier_target: unit

Found in the #2776 (#2770) review. A subsystem goroutine in `cmd/atlas` that failed (gRPC failing to bind, say) recorded its error and returned, while every other loop kept running on the signal context. The process stayed up with that subsystem dead and printed the error only after SIGINT/SIGTERM. A liveness check that probes only HTTP passed throughout.

## D1 — Which subsystems are fatal (Matt, 2026-11-20)

Decision recorded on the ticket ([comment](https://github.com/mgoodric/security-atlas/issues/2781#issuecomment-7052506954)):

| Fatal: cancel the root context, exit 1     | Non-fatal: log and back off    |
| ------------------------------------------ | ------------------------------ |
| gRPC server                                | exception expirer              |
| HTTP server                                | decision overdue notifier      |
| streambuf consumer                         | personnel overdue notifier     |
| eval ingest subscriber                     | security awareness roster sync |
| freshness/drift refresh subscriber         | control import sweeper         |
| risk residual subscriber                   | digest scheduler               |
| personnel security worker-event subscriber | exception idempotency sweeper¹ |
| eval scheduler                             |                                |
| freshness/drift scheduler                  |                                |
| metrics scheduler                          |                                |
| staleness scheduler                        |                                |

¹ Added by #2714 after the decision, by its rule: it is a sweeper, so it is non-fatal. It was not in the list Matt decided on.

The fatal set is what serves requests or keeps the evidence and control-state read models current. A process missing one of those is wrong in a way the operator cannot see. The non-fatal set sends notifications or tidies up, and a missed sweep is retried on the next tick.

## D2 — Mechanism: `runGroup` over `context.WithCancelCause`

`cmd/atlas/rungroup.go` wraps the signal context in `context.WithCancelCause`. `run.fatal(name, fn)` starts `fn` in a goroutine; its first non-nil error is recorded as `name: err` and cancels the context with that error as the cause. `run.nonFatal` records the error and cancels nothing. `run.wait()` waits for every goroutine and returns the collected errors, and `main` prints them and exits 1 as before.

The classification sits at the call site in `main.go` (`run.fatal` vs `run.nonFatal`), so a new subsystem has to pick one. The alternative, a `fatal bool` field in a table of subsystems, would have meant restructuring the per-subsystem pool setup in `main` for no gain.

## D3 — "Back off" is the loops' existing behaviour

Every non-fatal loop's `Run` already logs a failed sweep and waits for its next tick (`internal/exception/expiry.go`, `internal/decision/overdue.go`, `internal/personnelsecurity/overdue.go`, `internal/securityawareness/rostersync.go`, `internal/controlimport/sweep.go`, `internal/exception/idempotency_sweep.go`, `internal/notify/scheduler/scheduler.go`); each returns nil on context cancel. No retry wrapper was added around them. If one ever returns an error, it is recorded and printed at exit, and nothing else stops.

## D4 — Backup and restore-verification are unchanged

They were not in the decision, and `RunBackup` / `RunVerify` return no error (a failure raises an in-app alert, slice 510 D9). They are tracked with `run.add` so `wait` still waits for them, and they stop when the context is cancelled, like everything else.

## D5 — Only a non-nil error is fatal

The ticket specifies the first non-nil error. A fatal subsystem that returns nil before the context ends is not treated as a failure. None of the fatal loops do that today.
