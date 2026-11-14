# Issue #1806 — policy review dates through the API

Build-time calls made for
[#1806](https://github.com/mgoodric/security-atlas/issues/1806).
`policies.next_review_at` (slice 094) could only be set by SQL or the #1747
import. The calendar listed policies that had one, but reported the policy's
lifecycle status instead of overdue / due-soon, so nothing could show which
policies were overdue for review.

`detection_tier_actual = none` · `detection_tier_target = integration`
(covered by `internal/api/policies/review_date_integration_test.go`).

## D1 — The status string is `due-soon`, not `due_soon`

The ticket writes `due_soon` and also says "as other event types do". The
other dated calendar branches (controls, offboarding checklists) emit
`due-soon`, and the ICS feed and tests use that spelling. Consistency with
the existing branches wins: policy events emit `overdue` / `due-soon` /
`upcoming`. The policy list's `review_status` field and `?review=` filter
use the same three strings, so a calendar status and a list filter value
are interchangeable.

## D2 — Due soon means within 30 days, and only published policies have a review due

The 30-day window comes from the ticket; the offboarding branch uses 1 day
and controls 14, so the window is per event type. It is named once in Go as
`policy.ReviewDueSoonWindow` and once in the calendar SQL, with each
pointing at the other.

The calendar branch now filters `status = 'published'`, and `review_status`
is empty for any other state. A draft or superseded row is not the policy
in force, so it has no review due. The policy lifecycle state moved from the
event's `status` to its `summary`.

## D3 — The update is `PATCH /v1/policies/{id}/review-date`, audit-logged in `policy_audit_log`

A dedicated endpoint, not a general policy PATCH: the ticket asks for "a
dedicated update", and the policy body stays immutable once published. The
body is `{"next_review_at": "YYYY-MM-DD" | RFC 3339 | null}`. The key is
required, and `null` clears the date. Only a published policy can be
changed (409 otherwise).

The gate is `cred.IsApprover || cred.IsAdmin` in the handler, the same gate
approve and publish use. OPA sees the route as `write` on `policies`, so a
GRC engineer passes OPA and the handler returns 403. A viewer is refused by
OPA.

The audit row goes in the existing append-only `policy_audit_log` (#1747),
not a new table. Migration `20260903000000_policy_review_dates` widens its
`action` CHECK to `review_date_updated` and adds `previous_next_review_at`
and `new_next_review_at`. The actor is the user behind the credential,
falling back to the credential id, which is the rule the import and export
audits use. The update and its audit row commit in one transaction.

## D4 — A new version does not inherit its predecessor's date

Publishing takes an optional `next_review_at`. When a later version
publishes, `SupersedePolicyAtPublish` clears the predecessor's date, and the
new version carries only the date given at publish, or none. Inheriting the
old date would hide that a new version usually resets the review clock.
Leaving the date on the superseded row would keep a stale review in the
calendar, and the D2 published-only filter would hide it anyway.

## D5 — An unknown `?review=` value is a 400

`?status=` ignores unknown values. `?review=` refuses them. A mistyped
`?review=overdu` that returned every policy would look like a list of
overdue policies, and one that returned none would look like "nothing is
overdue". Neither is acceptable for a review-tracking filter.

## D6 — The list UI filters client-side on `review_status`

The list page loads the whole policy library (slice 101) and filters in the
browser. The Review pill (`?review=overdue` / `due-soon`) matches on the
`review_status` the API computes, so the page and the API filter agree on
the boundary and the browser clock is not involved. The "Next review"
column shows the date and an "Overdue" / "Due soon" marker. Upcoming and
undated rows show no marker.
