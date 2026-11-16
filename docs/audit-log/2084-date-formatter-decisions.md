# #2084 — one date formatter: decisions

detection_tier_actual: none
detection_tier_target: none

## D1 — extend `lib/relative-time.ts`, render through `<Timestamp>`

`formatTimestamp` lives beside `relativeTime` and reuses it for the
recent branch. A pure function keeps the 7-day boundary, timezone and
locale behaviour vitest-testable without a DOM (vitest is node-only, Q-3).
`<Timestamp>` is the only render path: a `<time dateTime>` whose `title`
is the exact instant.

## D2 — three styles, and calendar days format in UTC

- `recency` (default): relative within 7 days, absolute date and time
  otherwise. A stamp more than a minute in the future renders absolute,
  so clock skew never shows as "just now".
- `date`: the calendar day. Due dates, review dates, period bounds and
  decision dates use it; they never go relative.
- `datetime`: absolute day and time (evidence observed/ingested, the
  frozen evidence horizon).

The backend carries calendar days as midnight UTC (or a bare
`YYYY-MM-DD`). Formatting them in the viewer's zone would show the
previous day west of UTC, which is the #2104 bug the risk page fixed with
`utcDay`. So `date` style and date-only values format in UTC; instants
format in the viewer's zone.

## D3 — tooltip is UTC, labelled

`2026-11-14 09:30:05 UTC`, to the second. Auditors compare against
ledger rows and exports, which are UTC; a local tooltip would invite a
misread. A date-only value's tooltip is the bare day.

## D4 — hydration

The viewer's locale, zone and clock exist only in the browser. The
server render and the hydrating pass format a fixed `en-GB`/UTC absolute
form; the first client render after hydration (`useSyncExternalStore`
server snapshot `false`) swaps in the viewer's form. No mismatch warning,
and the client-only pages (most of them) never show the fallback because
their data arrives after hydration.

## D5 — viewer timezone is the browser's

`/v1/me` carries a `time_zone` preference, but no client context exposes
it to arbitrary components today. The browser's zone is the viewer's zone
for this change; wiring the profile preference through is a follow-up.

## D6 — scope split

The dispatcher capped the change near 25 files. This PR covers the core
pages the ticket names (Controls, Evidence, Activity, audit periods,
Policies) plus risk detail and the decision timeline. The remaining
per-page formatters (exceptions, action plans, personnel security, vendor
detail, minted keys, board-pack header, metrics `formatRelative`, admin
super-admins/tenants, audit log, questionnaires) move in a follow-up
issue (#2365).

## D7 — Playwright pins `en-US` / `UTC`

Formatted dates depend on locale and zone, so the e2e config pins both;
the three specs that asserted `YYYY-MM-DD` text on converted cells now
assert the formatted form.
