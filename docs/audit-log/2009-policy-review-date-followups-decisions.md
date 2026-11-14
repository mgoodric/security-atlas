# Issue #2009 — policy review dates: audit trail, down migration, due-day semantics

Build-time calls made for
[#2009](https://github.com/mgoodric/security-atlas/issues/2009), the
follow-ups from the review of #1992 (#1806). They revise #1806's D2 and its
choice to null a superseded row's date
(`docs/audit-log/1806-policy-review-dates-decisions.md`).

`detection_tier_actual = manual_review` · `detection_tier_target = integration`
(the review of #1992 found all three; the boundary and supersede behaviour
are now pinned by `internal/api/policies/review_date_integration_test.go`).

## D1 — A superseded version keeps its review date; no new audit action

The ticket offers two fixes: keep the date on superseded rows, or write an
audit row for both the set-at-publish and the null-at-supersede events.
Keeping the date is chosen. With it, nothing about a version's review is
lost:

- v1's due date stays on v1's row, beside v2's `published_at`, so "was v1
  reviewed on time" is a comparison of two columns on the chain.
- The date set at publish is on the row until an approver changes it, and
  every change already writes a `review_date_updated` row carrying the
  before and after values (#1806 D3). So the full history is recoverable
  without a publish-time audit row.

Writing audit rows instead would have needed a migration widening the
action CHECK and still left the superseded row without the date it was
due. No migration is added.

Only a `published` row has a review due, which the calendar's
`status = 'published'` filter and `ReviewStatusOf` already enforce. The
web list hides a superseded row's date in its Next review column, and the
detail page labels it "Review was due".

## D2 — The down migration refuses rather than deletes

`20260903000000` down used to turn off `policy_audit_log_no_update_trg`
and delete `review_date_updated` rows. It now `RAISE EXCEPTION`s when any
exist, naming the count, and changes nothing. The append-only trigger is
never disabled. Removing those rows is a deliberate owner act, after they
are archived.

`20260921000000` down (#1987) had the same delete-under-disabled-trigger
shape for `draft_discarded` rows. It gets the same fix in this PR, at the
dispatcher's request: it refuses while any `draft_discarded` row exists.
Run in order, `0921` down now refuses before `0903` down is reached.

## D3 — Overdue means after the due day, in UTC

The due day is the UTC calendar date of `next_review_at`. A date-only value
is stored as 00:00 UTC, so it is its own due day; an RFC 3339 value with a
time is due on its UTC date.

| Status     | When (all in whole UTC days)   |
| ---------- | ------------------------------ |
| `overdue`  | due day < today                |
| `due-soon` | today <= due day <= today + 30 |
| `upcoming` | due day > today + 30           |

UTC is the server's reference zone for every stored timestamp, and a
tenant time zone does not exist yet. A reviewer in New York sees a policy
due 13 Nov turn overdue at 19:00 local on 13 Nov; that is the documented
cost of one zone for everyone.

Go (`policy.ReviewStatusOf`) and SQL (`calendar.sql`) both compare dates,
not instants: Go truncates to the UTC day and adds `ReviewDueSoonDays` (30)
with `AddDate`, and SQL compares `(ts AT TIME ZONE 'UTC')::date` with
`date + 30`. Neither involves an `INTERVAL` or a 24-hour duration, so DST
cannot move the boundary and the list and the calendar agree.
`ReviewDueSoonWindow` (a `time.Duration`) is replaced by
`ReviewDueSoonDays`.
