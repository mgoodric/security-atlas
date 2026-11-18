# #2365 — shared date formatter on the remaining pages: decisions

detection_tier_actual: none
detection_tier_target: none

Follow-up to #2084 (`docs/audit-log/2084-date-formatter-decisions.md`).

## D1 — calendar day or instant, per field

`date` style formats in UTC (2084 D2), so it is right for a calendar day
and wrong for a real instant: an instant late in the evening west of UTC
would show the next day. Each converted field was classified:

| Field                                                                                                                 | Kind         | Style      |
| --------------------------------------------------------------------------------------------------------------------- | ------------ | ---------- |
| action plan `due_date`, personnel `due_at`, exception `expires_at`                                                    | calendar day | `date`     |
| vendor `first_review_due`, `last_review_date`, `contract_start`, `contract_end`, `renewal_date`, review `reviewed_at` | calendar day | `date`     |
| exception `requested_at`, action plan `created_at` / `updated_at`, personnel item `completed_at`                      | instant      | `recency`  |
| questionnaire `updated_at`, metric manual-input `observed_at`, API key `last_used_at`                                 | instant      | `recency`  |
| tenant `created_at`, super-admin `granted_at`, API key `expires_at`                                                   | instant      | `datetime` |

Admin rows and key expiry use `datetime`: an operator reading a grant or
an expiry wants the exact moment, not "3 days ago". The metrics audit row
dropped its raw ISO suffix; the exact value is the tooltip.

## D2 — board pack header stays fixed-format UTC

The pack header renders for print and PDF, so it must not follow the
viewer's locale. It now uses the shared `exactUTC`
(`2026-11-14 09:30:05 UTC`) instead of its own regex, which is the same
form `<Timestamp>`'s tooltip uses. It gained seconds; nothing asserted the
old minute-precision text.

## D3 — exact value for assistive tech, not a tab stop

`title` shows only on mouse hover. The exact value is now also emitted as
visually-hidden CSS generated content (`.timestamp-exact::after`, from
`data-exact`), which screen readers read after the visible text. Generated
content is not part of `textContent`, so copy-paste and every existing
`toHaveText` assertion still see only the visible text.

A focusable tooltip was rejected: stamps sit inside row links (the
evidence stream rows on control detail, for one), and a focusable child of
a link is an axe `nested-interactive` violation. Sighted keyboard and touch
users still do not get the exact value; that needs a popover that is not a
tab stop of its own, which is a follow-up if wanted.

## D4 — day-boundary e2e project

Playwright pins every spec to UTC, where a local-zone format and a UTC
format of a midnight-UTC day agree. A `chromium-denver` project
(`timezoneId: "America/Denver"`) runs only
`e2e/timezone-denver-2365.spec.ts`, which renders vendor dates carried as
both a bare day and midnight UTC and checks neither moves to 30 Nov. The
default project ignores that spec.

## D5 — the acceptance grep is a vitest sentinel

`web/lib/no-local-date-formatters.test.ts` runs the ticket's formatter
grep and a `new Date(...).toLocaleString(` check over `app/` and
`components/`, so a new hand-rolled formatter fails `Frontend · vitest`.
Out of its scope, deliberately: `toLocaleDateString` for calendar month
headings, and the two `toLocaleTimeString` "Saved · 3:04 PM" stamps in the
questionnaire editor, which show a client-side save moment, not a stored
timestamp.

## D6 — `time_zone` preference

Still not wired (2084 D5): no client context exposes `/v1/me`'s
`time_zone` yet, and building one is out of scope here.
