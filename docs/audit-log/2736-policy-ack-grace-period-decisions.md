# 2736 — Never-acknowledged policies escalate to overdue after a grace period: decisions log

Issue: mgoodric/security-atlas#2736 (from the review of #2729, #2587 D3).
Supersedes the "never acknowledged" row of
`2587-needs-attention-policy-ack-decisions.md` D3.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The review of #2729 found that
  a policy ignored for two years ranked below one acknowledged and lapsed.
- **detection_tier_target:** unit. `AckItems` is a pure function; a test
  ranking an old never-acknowledged item against a lapsed one shows it.

## Decisions

### D1 — the grace is 14 days, the calendar's due-soon window

`policy.AckGraceDays = 14`, the value the ticket proposes and
`calendar.DueSoonDays`. It is a separate constant in `internal/policy`
rather than an import of the calendar's, because `internal/api/calendar`
sits above `internal/policy`. Changing one does not change the other.

### D2 — one rule, `policy.Pending.Due`, read by both endpoints

| State                                 | Status / band | Due                             |
| ------------------------------------- | ------------- | ------------------------------- |
| Last ack older than 365 days (lapsed) | `overdue`     | last ack + 365 days (unchanged) |
| Never acknowledged, due day not ended | `due_soon`    | effective date + 14 days        |
| Never acknowledged, due day ended     | `overdue`     | effective date + 14 days        |

`GET /v1/needs-attention` bands its `policy_ack` item by it and
`GET /v1/me/acknowledgments` reports it on every pending row as `status`
and `due_at` (new, additive fields). The handler judges with the store's
own clock (`AckStore.Now`), the instant the pending list was cut at.

A due-soon item now carries `due_at` instead of `since`: the due date is
known, and the item type doc says the due-based bands carry `due_at`.
Within the due-soon band, items still sort oldest first.

### D3 — overdue once the due day has ended

The never-acknowledged due date is a date (effective date is a `DATE`), so
it follows `duedate.IsOverdue`, the rule every date-valued due date in the
estate uses: overdue from the next UTC day. On its due day it is still due
soon. The lapsed case keeps its instant rule from #2587; its due is a
timestamp, not a date.

### D4 — an unlapsed old-version ack still counts as none

As in #2587 D3, an ack that would lapse after now is treated as no ack, so
the row follows the never-acknowledged rule and is dated from its effective
date plus the grace.
