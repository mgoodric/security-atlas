# #2693 — An inverted review-due range is a 400; saved-view columns tied to the chooser

detection_tier_actual: none
detection_tier_target: none

Follow-up from the review of #2692 (#2550).

## D1 — `GET /v1/risks` returns 400 on `review_due_from > review_due_to`

The ticket asked whether it should. Yes: `parsePageFilter` already refuses
every malformed value instead of ignoring it (slice 067's "present but
malformed is a 400"), and an inverted range is a query that can never match.
An empty page hid the mistake; the register now shows the 400 message in its
load-error alert. The check moved into `parsePageFilter`, and
`ValidateSavedView` dropped its own copy, since it already runs the whole
view through `parsePageFilter`.

## D2 — stored views are protected on read, not by auditing live data

The ticket said to check existing saved views for inverted ranges first,
because they would start failing on apply. This is a self-hosted product with
no central database to audit, so a one-off check on one instance would not
cover the others. Instead `SanitizeSavedView`, which every stored risks view
passes through on read (`toWire` in `internal/api/controls/saved_views.go`),
drops both ends of an inverted range. Such a view keeps its other filters and
can no longer produce a 400. Dropping both ends, not just one, avoids applying
a half-range the operator never chose.

## D3 — the column drift test reads the TypeScript source

`TestSavedViewColumns_MatchChooser` reads `OPTIONAL_COLUMNS` out of
`web/app/(authed)/risks/server-query.ts` and compares it, in order, with
`savedViewColumns`. A shared JSON fixture would mean changing the web
constant's `as const` type. Go contract tests already read files under `web/`
(for example `internal/api/dashboard/handler_contract_test.go`), so this
follows that pattern. The test was checked against a mutated Go list and
failed with both lists in its message.
