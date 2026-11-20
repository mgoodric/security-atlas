# 2575 — Module gate follow-ups: audit dedupe on write failure, strict switch reads, Store.Get callers: decisions log

Issue: mgoodric/security-atlas#2575. Follow-ups from the review of #2570
(#2339). Builds on `docs/audit-log/2339-module-switch-follow-ups-decisions.md`
and `docs/audit-log/2548-background-senders-fail-closed-decisions.md`.

## Detection-tier classification

- **detection_tier_actual:** manual_review. All four items were raised in the
  review of #2570, not by a test.
- **detection_tier_target:** unit. Each is a pure-Go branch (the dedupe map,
  the strict/lenient read choice) and now has a unit test. The audit retry
  test was observed failing with the un-mark removed (2 writes, want 4).

## Decisions

### D1 — a failed gated-attempt audit write drops its dedupe window, and the write outlives the request

`gatedAttemptAuditor.first` marks (tenant, actor, switch) before the write,
so a failed write used to leave a 5-minute window with no row. Both fixes
the ticket offers are applied, because they cover different failures:

- **The write runs on `context.WithoutCancel(r.Context())` with a 5-second
  timeout.** The 404 or 403 is already written when the recorder runs, so a
  client that hangs up straight away used to cancel its own audit row.
- **A failed write calls `forget(key)`,** so the next attempt writes again.
  A DB outage therefore writes nothing but tries on every attempt; that is
  bounded by the request rate the gate already serves, and each failure is
  logged.

The un-mark races a concurrent attempt for the same key: two in-flight
attempts can both write once the first fails. That is an extra row, never a
missing one, matching the cap's "a reset can only write an extra row" rule.

The auditor now holds an `auditWriter` interface (the `Write` method of
`*authz.AuditWriter`) so a test can fail the write. A nil writer is an error
(as `*authz.AuditWriter.Write` on nil already was), and is not marked.

### D2 — every `Enabled()` / switch-read caller and its choice

The rule from the ticket: background work reads strictly and skips on a
flag-store error; a request path keeps the lenient read only where the
gate has already read that switch strictly. Where a request reads switches
the gate did not read, it reads strictly and fails the request.

| Caller                                                                          | Kind       | Read                                  | On a flag-store error                                      | Why                                                                                                                                                                                                       |
| ------------------------------------------------------------------------------- | ---------- | ------------------------------------- | ---------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ModuleGate` (`featureflag/modules.go`, via `lookup`)                           | request    | strict                                | 503 `module_state_unavailable` (unchanged)                 | #2339                                                                                                                                                                                                     |
| `featureflag.RouteOn` (via `lookup`) — `Viewer.Sees`, approvals handler         | request    | strict                                | error to the handler (unchanged)                           | Already strict                                                                                                                                                                                            |
| `Viewer.Modules` — metrics, admin activity log, activity actors                 | request    | **strict (was lenient)**              | 503 `module_state_unavailable` (`moduleaccess.WriteError`) | The route's gate read only its own module (often core: no read). A default-on module a tenant switched off would show its rows in the activity log and metrics during a blip.                             |
| `Viewer.SeesModule` — people handler `?module=` filter                          | request    | **strict (was lenient)**              | 503 `module_state_unavailable` (`moduleaccess.WriteError`) | Same: the module named in the query string was never read by the gate.                                                                                                                                    |
| `writeproposals.Store.moduleOff` (MCP proposal confirm)                         | request    | **strict (was lenient `SwitchedOn`)** | 503 `module_state_unavailable`; proposal stays pending     | The confirm writes into the risk module; the gate read the MCP proposals route, not risk. A lenient read could apply a proposal into a module the tenant switched off.                                    |
| `Views.SwitchedOnStrict` — board pack generator                                 | background | strict                                | generate fails, nothing built (unchanged)                  | #2548                                                                                                                                                                                                     |
| `Views.Reaches` — decision / personnel notifiers; email, Slack, webhook digests | background | strict                                | tenant skipped this sweep, logged (unchanged)              | #2548                                                                                                                                                                                                     |
| `features.Handler.Modules` (`GET` module list for the nav)                      | request    | lenient `Enabled` (unchanged)         | seed default                                               | It returns no tenant data, only which nav entries to draw. A default-on module drawn during a blip leads to its routes, which the gate answers 503. Failing it would blank the whole shell for a DB blip. |

`Views.SwitchedOn` (lenient) now has no production caller; it is kept, with
its tests, as the documented lenient twin of `SwitchedOnStrict`.

Background jobs already read strictly and skip (#2548). What #2575 adds is
the count: every `EnabledStrict` read that fails on `ErrStoreUnavailable`
increments `atlas.module_switch.strict_read_errors` (attribute `flag_key`),
alongside the existing log lines. One counter covers background skips and
the strict request reads above; the log line at each call site says which.

### D3 — `Store.Get` direct callers

`featureflag.Store.Get` returns `ErrStoreUnavailable` alongside the seed
default. A search of non-test code finds one direct caller:
`featureflag.lookup`, which `Enabled`, `EnabledStrict`, `RouteOn` and
`ModuleGate` share. `Store.Set` reads the row through `dbx` directly, not
through `Get`, and the admin features handlers use `List` and `Set`. No
caller turns a degraded `Get` into an unintended 500. The strict request
reads in D2 fail closed with 503 `module_state_unavailable`, the answer
ModuleGate gives (review fold-in: `moduleaccess.WriteError` maps
`ErrStoreUnavailable` to 503 and keeps every other viewer error at 500;
`TestGetTarget_StoreDownIs503` covers one handler end to end).

### D4 — the cap's pruning branch is tested

`TestGatedAttemptAuditorCapPrunes` fills the map to the cap with expired
windows plus one live one, and checks a new attempt prunes the expired
entries, keeps the live window, and does not reset the map.
