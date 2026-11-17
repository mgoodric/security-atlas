# #2324 — audited-under follow-ups: unresolved references, cap, duplicates, CLI

Parent: #1735. Follows #1801 (`1801-audited-under-report-decisions.md`).
The web page and its "add overlay edge" row action shipped in #2349
(#1802); this change adds the data-quality note to that page.

detection_tier_actual: none
detection_tier_target: none

## What changed

- `GET /v1/mapping-overlays/audited-under` returns `total`, `limit`,
  `truncated`, `unresolved_references` and `unresolved_count`, and takes
  `?limit=`.
- The CSV export takes the same `?limit=`, is encoded straight onto the
  response, and sets `X-Atlas-Truncated` and
  `X-Atlas-Unresolved-References`.
- Duplicate tested-under references to one criterion on one control fold
  into one row.
- `atlas-cli mappings overlay audited-under` (`--framework`, `--limit`,
  `--json`, `--csv`, `-o`).
- `/catalog/audited-under` shows a truncation banner and an unresolved
  references note.

## Decisions

**D1 — Unresolved references are a list in the same response, not a
separate check.** #1801 D4 left them out of the report. They now come back
beside the rows as `unresolved_references` (control, `system`, `code`,
note) with `unresolved_count`. "Unresolved" uses the same rule the gaps
query resolves with: a global framework with that slug, its `current`
version, and a requirement with that code in it. Anything else is
unresolved, including an unknown framework slug. `?framework=` filters on
the reference's `system`, so a typo'd slug only shows unfiltered. That is
accepted: the unfiltered report is where an auditor looks for breakage.

**D2 — The cap is 5000, and `?limit=` may lower it but not raise it.**
5000 rows is well beyond a solo program's control set times its tested
criteria, and small enough to hold in memory. A value outside 1..5000 is a
400, not clamped, matching `GET /v1/risks/{id}/audit-log` (#2311). The cap
applies to the rows and to the unresolved list separately. `truncated` is
true when either list was cut. `total` and `unresolved_count` come from a
`count(*) OVER ()` window, so they are exact without a second query.
Pagination was not added: a cursor over a live, re-sorted report would
shift as overlay edges close rows, and the cap plus `?framework=` covers
the realistic case.

**D3 — "Stream the CSV" means encoding onto the response, not a DB
cursor.** The handler used to build the whole CSV in a buffer and then
write it. It now writes rows through the shared encoder straight to the
`ResponseWriter`. The store still reads at most 5000 rows inside its RLS
transaction. Holding that transaction open while a slow client drained
the body would trade a bounded allocation for an unbounded connection
hold, which is the worse trade. A write error after the 200 can only be
logged.

**D4 — The CSV signals truncation with headers.** A trailing marker row
would break consumers that read the file as a table. `X-Atlas-Truncated`
(`true`/`false`) and `X-Atlas-Unresolved-References` (a count) are set on
every export. The CSV does not list unresolved references; the JSON report
and the CLI do.

**D5 — Duplicates fold in SQL, with notes sorted.** The gaps query groups by
control and requirement (plus the joined rows' keys). The notes are
`string_agg(DISTINCT nullif(note, ''), '; ' ORDER BY ...)`, so the merged
note is the same on every read, an empty note adds nothing, and a repeated
note appears once. Folding in SQL means the cap counts folded rows.

**D6 — The CLI lives under `mappings overlay`.** The report is served under
`/v1/mapping-overlays` and its fix is `mappings overlay add`, so
`mappings overlay audited-under` keeps the two together. `--csv` reads the
export endpoint, like `mappings overlay export`. The table prints the
unresolved references and a truncation line after the rows.

**D7 — The page shows the note and the banner, with no row action.** An
unresolved reference names no criterion, so no overlay edge can close it.
The note links each to its control, where the reference is edited.
