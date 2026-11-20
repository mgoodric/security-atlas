# 2555 — Needs-attention review follow-ups: decisions log

Issue: mgoodric/security-atlas#2555, nits from the review of #2553 (#2134).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. `cmd/atlas`
  already wires the OPA engine and the module views; the change makes that
  a startup requirement instead of an assumption.
- **detection_tier_target:** unit (`TestRunHTTP_RefusesWithoutAuthzEngine`,
  `TestViews_Wired`).

## Decisions

### D1 — no per-band SQL cap; the reads stay unbounded

The ticket asked for a measurement before any cap. A throwaway integration
test (not committed) seeded N unowned active controls and N open risks,
half of them owned and overdue, into tenant A of the attention fixture. It
then timed `GET /v1/needs-attention` through the production router on a
scratch Postgres (`scripts/scratch-pg.sh`) on a laptop, five runs each:

| N (controls + risks each) | items in view (`for=all`) | `for=all`  | `for=me`   | `for=all&limit=100&offset=500` |
| ------------------------- | ------------------------- | ---------- | ---------- | ------------------------------ |
| 1 (fixture only)          | 15                        | 5–7 ms     | 4 ms       | 3–5 ms                         |
| 5,000                     | 12,512                    | 61–78 ms   | 71–75 ms   | 73–96 ms                       |
| 20,000                    | 50,012                    | 241–306 ms | 299–307 ms | 299–313 ms                     |

The cost is linear, about 15 ms per thousand rows per source. The v1
tenant is a 50–150-person company. Its control count is bounded by the SCF
catalog (about 1,400), and its risk register holds a few hundred rows. That
is well under 50 ms. A cap of "band count plus page" would also break the
response's `total`, `counts` and `type_counts`, which promise exact numbers.
Bounding the reads would need per-band `COUNT` queries next to limited
reads. That is not worth adding until a tenant's latency shows it.
Revisit if a tenant's sources pass roughly 10,000 rows.

### D2 — the startup assertion lives in `Server.RunHTTP`

`calendar.ReadableTypes` with a nil engine and `moduleaccess.Views.For`
with a nil or storeless Views admit everything. That is deliberate for
unit servers, which build the handler through `HTTPHandlerForTests` with no
OPA engine. `RunHTTP` is the only serving entry point (`cmd/atlas`), so it
now refuses to start when `AttachAuthz` was not called. It also refuses when
the module views built by `httpHandler` are not wired to both stores (the
new `Views.Wired`). The check sits after `configErr`, so the #2549
config-error test still sees its own error first.

### D3 — the exception owner lookup needs no tie-break

`control_owner_assignments` has `PRIMARY KEY (tenant_id, control_id)`
(migration `20260612060000_control_owner_assign_saved_views.sql`). The
exception owner join therefore yields at most one row per exception. A
comment on `ownerQueries` now says so; no query change.
