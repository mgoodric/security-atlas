# 2141 — Controls list: one Export menu and useful bulk actions: decisions log

Issue: mgoodric/security-atlas#2141 (front door, parent #2072).

## Detection-tier classification

- **detection_tier_actual:** none. No bug surfaced while building.
- **detection_tier_target:** none.

## Decisions

### D1 — no new route; every bulk action rides an existing gated write

| Action            | Write                                                    | Failure shape                                                  |
| ----------------- | -------------------------------------------------------- | -------------------------------------------------------------- |
| Assign owner      | `POST /v1/controls:bulk-assign-owner` (slice 468)        | All or none: one refused id rolls back the batch; one message. |
| Evaluate now      | `POST /v1/controls/{id}/evaluate`, once per control      | Per item: n succeeded, m refused, each with its reason.        |
| Link to a plan    | `POST /v1/action-plans/{id}/controls/{cid}`, once per id | Per item, as above.                                            |
| Request exception | `POST /v1/exceptions`, once per control, from the form   | Per item, on the form; refused controls stay listed.           |

The bulk-assign endpoint already re-checks role and tenant for every id
(AC-11) and refuses the whole batch on any failure, so the UI says "No owner
was changed" on a refusal. The others have no bulk endpoint; adding one would
be a second, looser path to the same writes, so the client fans out over the
per-control endpoint and its gate. Fan-out is capped at four in flight
(`BULK_CONCURRENCY`).

### D2 — the cap is the server's, mentioned only above it

`SELECTION_CAP` (200) already mirrors the server's `BulkAssignCap`. It is not
a new number. The "(cap 200 per bulk action)" note that always showed is
gone; above the cap a message names it and every action is disabled.

Known limit: evaluation is rate-limited per principal
(`ATLAS_EVALUATE_RATE_PER_MIN`, default 6, all at once). A bulk evaluate
larger than that gets the overflow refused with the platform's 429 reason,
listed per control, and the refused controls stay selected for a retry. The
limit is the platform's to set; the UI does not throttle around it.

### D3 — gates from the live `/v1/me`, failing closed, matching #2140

`bulkActionGates` (pure, unit-tested): assign owner, link to a plan and
request exception need `is_approver`; evaluate also admits a `control_owner`,
whose assignment the handler checks per control. Link needs the module that
owns `/action-plans` (Risk) and request exception the Exceptions module, read
through `useSeesPage`. No profile yet (or a failed read) offers nothing. A
caller offered no action gets no selection column, rather than checkboxes
that lead nowhere.

### D4 — request exception opens the existing form for the selection

The form takes repeated `control_id` params. With more than one it lists the
controls and files one exception per control with the shared scope,
justification and expiry. One exception per control keeps the exception
record's shape (one control each) and its per-exception approval. On a
partial refusal the refused controls stay on the form with their reasons; on
full success it lands on `/exceptions` with a toast.

### D5 — the Export menu keeps every link as it was

The six links move into a base-ui `Menu` (the `RowActions` primitive), as
`Menu.LinkItem` anchors with `download`, the same hrefs and the same testids.

### D6 — refusals name the control, not its id

The selection outlives the page it was made on (#2131), so the page remembers
a readable label per control when it is selected and the result names each
refused control by its SCF code and title.

### D7 — e2e in a tenant of its own

The controls-list spec's tests run in parallel over the demo tenant's three
controls, and #2128 asserts their owner. The bulk round-trips run in
`fixtures/e2e/controls-bulk.sql`'s own tenant so they cannot race it. The
over-cap, viewer and partial-failure cases route-mock the list, `/api/me` and
the evaluate endpoint. The module-off case is in `module-links.spec.ts`,
which needed its control made `active` so the list shows it.
