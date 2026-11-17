# Risk edit follow-ups — decisions log (#2311)

Four follow-ups from the review of #2310 (#2104): a warning before a
methodology change resets the residual, a cap on
`GET /v1/risks/{id}/audit-log`, a bound on the `PATCH /v1/risks/{id}`
body, and optimistic concurrency on that PATCH.

detection_tier_actual: none
detection_tier_target: none

No bug surfaced in shipped code.

## Decisions

**D1: The ETag is the risk's `updated_at`, quoted; no version column.**
`risks.updated_at` already moves on every write path PATCH goes through
(`UpdateRiskPlacement`, `UpdateRiskCategory`, `UpdateRisk` each set
`updated_at = now()`), so it is a version without a migration. The ETag
is `"<updated_at RFC 3339>"`, and the server compares instants at
microsecond precision (Postgres's), not strings, so a client may quote
the `updated_at` from the JSON body in any zone. The web does exactly
that (`riskIfMatch`), which avoids reading a response header through the
BFF. Two writes in different transactions get different `now()` values;
two editors cannot share one.

**D2: A mismatch is 412, not 409.** `If-Match` is an HTTP precondition,
and RFC 9110 §13.1.1 names 412 for a failed one. 409 stays what it is
elsewhere on risks: a transition the state forbids (#2105).

**D3: If-Match is optional on the API and always sent by the web.** A
PATCH without it is last-write-wins, as before, so existing API clients
and scripts keep working. `*` is treated as absent. A header that is not
one quoted RFC 3339 timestamp (a list, a bare value, an opaque tag) is a
400 naming If-Match rather than a 412: a malformed precondition is a
client bug to surface, not a lost race. A `W/` prefix is tolerated.

**D4: The precondition is checked under the row lock.** `UpdatePlacement`
compares after `LockRiskByID` (`FOR NO KEY UPDATE`), before any write.
Of two editors from the same version the second blocks on the lock, then
sees the first's `updated_at` and is refused, writing no audit row.
`TestIfMatch_ConcurrentEditorsOneWins` sends both at once.

**D5: On 412 the web offers Reload, which drops the edits.** The page
shows "This risk changed since you opened it" with a Reload button that
refetches the risk; the form is keyed by `updated_at`, so it reopens on
the latest version. Merging the user's edits onto the new version was
considered and not done: a silent merge is what the precondition exists
to prevent.

**D6: The audit-log limit is a 400 outside 1..500, not a clamp.**
Default 200, max 500. A clamp would return fewer rows than asked without
saying so; `truncated` already tells a caller that more exist. The store
fetches `limit + 1` rows to set `truncated` without a count query. No
cursor yet (keyset pagination on `created_at, id` is the later step the
ticket names); the response carries `limit` and `truncated` so a cursor
can be added without changing them. The History section says "Showing
the latest N changes" when truncated.

**D7: The PATCH body cap is per-handler `http.MaxBytesReader`, 1 MiB.**
There is no global body-size middleware: `internal/api` routes each cap
their own body (`controls/handlers.go`, `policies/handlers.go`,
`me/profile.go` and others use `MaxBytesReader`). The PATCH reads the
body once (it decodes twice, for the refused-field check and the strict
decode), so the cap wraps that read; an `*http.MaxBytesError` is an
explicit 413, since `MaxBytesReader` itself does not set a status.

**D8: The residual-reset note shows only when the methodology differs
from the stored one.** It sits under the methodology select in the edit
form, says the residual resets because a residual on the old scale does
not read on the new one, and that the previous residual stays in the
history (#2104 D4 writes it to the `fields_changed` before-state).
