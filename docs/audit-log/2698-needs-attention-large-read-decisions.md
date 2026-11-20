# 2698 — Needs-attention large-read warning and second benchmark: decisions log

Issue: mgoodric/security-atlas#2698, follow-up from the review of #2696
(#2555). Builds on #2555 D1
(`docs/audit-log/2555-needs-attention-follow-ups-decisions.md`).

## Detection-tier classification

- **detection_tier_actual:** none. No product bug surfaced. The benchmark
  found a cost the row-count warning cannot see (D4); that cost is
  performance, not a wrong answer.
- **detection_tier_target:** unit (`TestWarnLarge`).

## Decisions

### D1 — the warning is per source read, not per band

`GET /v1/needs-attention` makes three uncapped reads, and none of them
maps to exactly one band:

| `source`   | Query                                       | `band` field            |
| ---------- | ------------------------------------------- | ----------------------- |
| `calendar` | `ListCalendarEvents` from the epoch         | `overdue,due_soon`      |
| `controls` | `ListActiveControlsWithState`, no row limit | `failing,ownership_gap` |
| `risks`    | `ListRisks`                                 | `ownership_gap`         |

So the warning fires per read and names the bands that read feeds. The
calendar read is one `UNION ALL` over exceptions, policies, vendors,
periodic controls, risks and action plans, so its row count is the sum
over those sources. The approvals band is not covered: it is
`approvals.Handler.Pending`, which is not uncapped by this handler.

The log line is `WARN "needs attention: band read passed the row
threshold"` with `tenant_id`, `source`, `band`, `rows` and `threshold`.
It fires when `rows > threshold`, so a read of exactly the threshold is
quiet.

### D2 — the threshold is an env var, default 10,000

`ATLAS_NEEDS_ATTENTION_WARN_ROWS`, read in `cmd/atlas` with the existing
`positiveIntEnv` (an invalid or non-positive value logs and falls back),
carried on `api.Config.NeedsAttentionWarnRows`, and applied with
`Handler.WithWarnRows`. Zero or negative keeps
`needsattention.DefaultWarnRows` (10,000), the "revisit" size in #2555
D1. It is the same shape as `ATLAS_EVALUATE_RATE_PER_MIN` (#2479). It is
documented in `docs-site/docs/configuration.md`, `deploy/docker/.env.example`
and both compose files.

The warning fires on every request that crosses it, not once per tenant.
A tenant past the size will log on each dashboard load. That is noisy on
purpose: it is the signal to revisit the uncapped reads, and a tenant at
that size is rare in v1. Deduplicating it would need per-process state
for a log line.

### D3 — second benchmark: evidence, evaluations, policies, exceptions, vendors, audit periods

Same method as #2555 D1: a throwaway integration test (not committed)
over the attention fixture, through the production router (JWT, OPA with
the live roles resolver, module gates) on a scratch Postgres
(`scripts/scratch-pg.sh`, Postgres 16) on a laptop, `ANALYZE` after
seeding, five runs per query. Times are min–max in ms. For N:

- **items:** N published policies with an overdue review, N active
  exceptions on one control that lapsed, N vendors with a monthly review
  last done a year ago. Each is a calendar row, so the calendar read
  returns 3N + 8 rows.
- **evaluations:** 10N `control_evaluations` rows split over two of the
  fixture's controls, all older than 400 days.
- **evidence:** 10N `evidence_records`.
- **audit periods:** N `audit_periods`, all ended in the past.

`for=me` is the fixture's admin, who holds the policies' owner role, so
it lists the N policies.

**Each source alone, N = 20,000:**

| Seeded                                | items (`for=all`) | `for=all` | `for=me` | `for=all&limit=100&offset=500` |
| ------------------------------------- | ----------------- | --------- | -------- | ------------------------------ |
| nothing (fixture only)                | 12                | 7–25      | 5–10     | 3–7                            |
| evidence (200k) + audit periods (20k) | 12                | 8–27      | 6–9      | 6–7                            |
| evaluations (200k)                    | 12                | 106–126   | 189–209  | 188–208                        |
| items (60k calendar rows)             | 60,012            | 330–377   | 385–418  | 397–414                        |

**Items by N:**

| N      | calendar rows | items (`for=all`) | `for=all` | `for=me` | `for=all&limit=100&offset=500` |
| ------ | ------------- | ----------------- | --------- | -------- | ------------------------------ |
| 5,000  | 15,008        | 15,012            | 87–108    | 98–102   | 100–103                        |
| 20,000 | 60,008        | 60,012            | 330–377   | 385–418  | 397–414                        |

**Everything at once:**

| N      | `for=all` | `for=me` | `for=all&limit=100&offset=500` |
| ------ | --------- | -------- | ------------------------------ |
| 5,000  | 137–158   | 160–175  | 159–164                        |
| 20,000 | 407–588   | 568–581  | 569–572                        |

What this shows:

- **Evidence records and audit periods cost nothing.** Needs-attention
  never reads `evidence_records`, and the calendar's audit branch is
  filtered out by type (`calendarTypes` has no `audit`).
- **Policies, exceptions and vendors are linear in calendar rows**, about
  6–7 ms per thousand rows, in line with #2555 D1's controls and risks
  (about 6 ms per thousand items). The warning fired on every request
  at N = 5,000 (`source=calendar rows=15008`) and N = 20,000
  (`rows=60008`), and on none at N = 1.
- **The first five calls to a query run faster than the rest.** Swapping
  the order of `for=me` and `for=all` swapped their times (106–122 vs
  189–199 ms over 200k evaluations). That fits Postgres moving a prepared
  statement to its generic plan on the sixth execution. I have not
  confirmed which statement changes plan. The steady state is the later
  columns.

### D4 — the evaluation ledger is a cost the warning cannot see

200k evaluations on two controls add about 100–190 ms while the controls
read still returns a handful of rows. `ListActiveControlsWithState`'s
`latest_eval` CTE is `DISTINCT ON (tenant_id, control_id, scope_cell_id)`
over every evaluation in the tenant, so its cost grows with the ledger's
length, not with the number of controls. The ledger is append-only and
grows with every scheduled evaluation, so this is the source most likely
to grow past the measured range in a real tenant.

The row-count warning does not catch it, because the rows returned stay
small. The query is the `/controls` page's own (#1741), so the same cost
applies there. Fixing it (for example, a per-control latest-evaluation
lookup through `idx_control_evaluations_latest`, or a maintained latest
row) is a change to the controls list, outside this ticket. It is left
as a follow-up rather than done here.

### D5 — `Views.Wired()` is unchanged

The ticket's optional item, checking that each store has a pool, is not
done. `httpserver.go` builds both stores on `s.dbPool`, and `cmd/atlas`
still has a no-`DATABASE_URL` in-memory fallback in which that pool is
nil (`cmd/atlas/main.go`, the slice-003 fallback). A pool check in
`Wired` would make `RunHTTP` refuse to start in that mode, a behaviour
change this ticket does not ask for. It would also need a type assertion
on `featureflag.Looker`, an interface. If the in-memory mode is retired,
the check becomes free to add.
