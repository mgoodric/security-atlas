# 2702 — Module-state outage answers 503 on every module-filtered read: decisions log

Issue: mgoodric/security-atlas#2702. Follow-up from #2697 (#2575), which
added `moduleaccess.WriteError` and used it for the activity, metrics and
people handlers.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The three named sites were found
  in the review of #2697; the rest by the sweep this ticket asked for.
- **detection_tier_target:** unit. Each site now has a store-down unit test,
  observed failing (500) with `httperr.WriteInternal` restored and passing
  (503) with `moduleaccess.WriteError`.

## Decisions

### D1 — the sweep covers every handler whose error can carry a strict switch read

`grep` for `.Sees(`, `.Hidden(`, `.Modules()`, `.For(r)`, `featureflag.RouteOn`
and `SwitchedOnStrict` under `internal/` found these writers still answering
a raw 500 for `featureflag.ErrStoreUnavailable`:

| Handler                                                | Route                                      |
| ------------------------------------------------------ | ------------------------------------------ |
| `decisions.writeDecision` (split out of `GetDecision`) | `GET /v1/decisions/{id}`                   |
| `incidents.writeDetail`, `incidents.refuseHiddenLinks` | incident detail, create, transition, close |
| `calendar.ListEvents`, `calendar.ICS`                  | `GET /v1/calendar`, `GET /v1/calendar.ics` |
| `search.Handle`                                        | `GET /v1/search`                           |
| `needsattention.List`                                  | `GET /v1/needs-attention`                  |
| `approvals.List`                                       | `GET /v1/approvals`                        |
| `dashboardexport.ExportDashboard`                      | `GET /v1/dashboard/export`                 |
| `board.PackHandler.Generate`                           | `POST /v1/board-packs`                     |

Board pack generation is a write, not a read, but it calls `SwitchedOnStrict`
before any store call, and the ticket names `SwitchedOnStrict` callers. MCP
write-proposal confirm already maps the error itself (#2575), and the
background senders log and retry, so neither changes.

### D2 — the writer changes, not the error path

Where one writer serves both a switch read and other failures (approvals,
needs-attention, the dashboard export, board packs), it becomes
`moduleaccess.WriteError`, which answers 503 only for
`ErrStoreUnavailable` and 500 for anything else, so no other failure's status
moves.

### D3 — one shared test helper

The store-down fake, a super-admin request (exempt from module access, so no
access row is read first) and the 503 assertion live in
`internal/moduleaccess/moduleaccesstest`, rather than being copied into eight
packages. `internal/api/metrics` keeps its own copy from #2697.

### D4 — the ICS feed has no unit test

`calendar.ICS` resolves its subscriber with `Views.ForUser`, which reads the
subscriber's roles from the database before any switch. Its writer is the
same one-line change as `ListEvents`, which is tested.
