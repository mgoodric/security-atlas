# Issue #2847 — Risk import web: batch review page with approve all and per-row decisions

Part of spec #2812. It builds on the tracer #2839
(`2839-risk-import-tracer-decisions.md`), whose API offered approve-all and
per-row reject but no per-row approve and no row flags. The dispatcher
confirmed on 2026-11-21 that this ticket adds both.

- `detection_tier_actual`: none
- `detection_tier_target`: none

## D1 — Per-row approve shares the batch approve's gate and row path

**Decision.** `POST /v1/risks/imports/{id}/rows/{row}/approve` takes no body.
It is gated by `decidingPerson`, the same function `Approve` and `Reject`
call, not a copy of it. It decides the row through `approveRowRetrying`,
which `ApprovePending` now also calls for each pending row. That path
covers the duplicate check, `risk.Store.CreateTx`, the `created` audit row,
the import audit row, and the one retry when a concurrent create takes the
ID.

**Why.** Matt decided #2881 on 2026-11-21. Risk-import approve and reject
will exclude GRC engineers and forbid self-approval, and both paths must
pick that up from one place. With a single gate, that change touches one
function. With a single row path, approving one row cannot drift from
approving all of them.

## D2 — A refused row is a 422 and stays pending

**Decision.** In approve-all, a row whose values the risk store refuses is
skipped with its reason. Examples are a category archived since upload, a
value the store rejects, or an ID taken meanwhile. The single-row approve
returns that refusal as a 422 instead. `IsRowRefusal` exports the
classification `skipReason` already makes. An already-decided row stays a
409, as on reject.

**Why.** A single-row caller asked about one row, so a 200 with a skip list
would hide the refusal. 422 separates "your row cannot become a risk as it
stands" from a 500. The approver can still reject the row.

## D3 — `flags text[]` with a CHECK on a fixed vocabulary

**Decision.** `risk_import_rows.flags TEXT[] NOT NULL DEFAULT '{}'` has the
CHECK `flags <@ ARRAY['owner_unlinked', 'treatment_downgraded',
'parent_link_failed']`. It is exposed as `flags: string[]`, never null. It
lands empty, and #2843, #2842 and #2846 set it.

**Why.** The page renders and filters on flags, so the contract has to exist
before the slices that produce them. A fixed vocabulary in the database
keeps a typo in one slice from becoming a flag the page cannot name. The
page still shows an unknown flag verbatim, so a future value is visible
before the web learns its label.

## D4 — Read-only from `/v1/me` is_approver

**Decision.** The page shows the decision controls only when `/v1/me` says
`is_approver`, which is `authz.IsTenantApprover`, the predicate
`decidingPerson` uses. Anyone else sees the rows, flags, unmapped values
and rejections with a read-only notice. While `/v1/me` loads, or if it
fails, the page is read-only.

**Why.** The platform gate is the authority, and the page only hides
buttons it would refuse. Failing closed means a slow `/v1/me` never shows
an approve button that returns 403. When #2881 narrows the gate (no GRC
engineers, no self-approval), the page will still show the buttons to a
GRC engineer, and the platform will refuse with its own message. A
uploader-aware hint belongs with that change.

## D5 — Client-side filtering

**Decision.** The status and flagged filters run in the browser over the
batch's `staged` list. No rows endpoint with query parameters is added.

**Why.** `GET /v1/risks/imports/{id}` already returns every staged row, and
the upload caps a batch at 5000 rows. The controls import has a separate
rows endpoint because its suggestions are computed asynchronously and
polled. The risk import has no such stage.

## D6 — Tests

**Decision.** The page's states are covered by vitest over the pure helpers
in `web/lib/api/risk-imports.ts`:

- progress tallies, including flagged;
- the status and flagged filters;
- read-only versus decide;
- the empty-list messages;
- mapped fields and unmapped values;
- the flag and reason labels;
- the approve-all toast.

The BFF routes have their own vitest. The API's per-row approve, its access
rules, the 422, and the flags column with its CHECK have integration tests
against real Postgres. The full upload-to-approve e2e lands with the upload
page (#2848), as the ticket says.

**Why.** `web/vitest.config.ts` is node-only by design (AGENTS.md Q-3), so a
component test is out of scope. Every branch the page renders is a helper
output.
