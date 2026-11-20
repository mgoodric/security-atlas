# 2590 — Switched-off modules in the remaining surfaces: decisions log

Issue: mgoodric/security-atlas#2590, from #2579 (#2352). #2173 and #2175
left four surfaces that could still show a switched-off module's data, and
the review of #2579 found that `moduleaccess.Viewer.Sees` failed open on a
tied owner.

## Detection-tier classification

- **detection_tier_actual:** manual_review. The coverage comments on #2173
  and #2175 and the #2579 review named every gap here.
- **detection_tier_target:** integration. Each surface now has a test with
  the module switched off for one tenant and on for another, against real
  Postgres.

## Decisions

### D1: Activity cuts the page after hiding rows

`/v1/activity/unified` already left out rows from hidden modules (#2173),
but it filtered after cutting the page. A page could come back short, or
empty, with a next cursor. `activityPage` now skips hidden rows first and
reads again from the last row it saw until the page is full. The page
size, the next cursor and the meta-audit `returned` count all describe
the rows the caller sees. Every page except the last is full, and only the
last page has no cursor.

The reads are capped at `maxActivityScans` (10) per page. If a run of
hidden rows is longer than that, the page ends early with a cursor past
the rows already read. The next page then starts after that run instead
of scanning it again, so one request does no more than 10 aggregator
reads.

### D2: the admin audit log marks rows, it does not hide them

`GET /v1/admin/audit-log/unified` keeps every row and sets
`module_switched_off: true` on a row whose module is switched off for the
tenant now. It uses the same placement as Activity: the row's kind, or for
an authorization decision the route it was made on.

It marks rows rather than hiding them because the admin log is the
forensic record. ADR 0025 decision 2 keeps audit-binding output whole, and
an auditor reviewing a period needs to see what happened in a module
before it was switched off. The mark comes from the switches alone
(`Views.Switches`), not the caller's module access, so an admin and a
narrowed grc_engineer see the same marks.

The CSV/JSON/xlsx export and the legacy `GET /v1/admin/audit-log` stay
unmarked. They are forensic dumps, and adding a column changes their
shape. Neither one hides rows.

### D3: the strict switch read everywhere a view decides

`Viewer.Modules` (Activity, its actor picker, Metrics) now reads switches
with `featureflag.EnabledStrict`, the read `RouteOn` and the gate use. A
flag-store error fails the request (500) instead of falling back to the
seed default, which could show a module the tenant switched off.
`Views.Switches` (the admin log mark) uses the same read.

### D4: tied owners fail closed in Sees

`Viewer.Sees` returns true only for `featureflag.ErrNoModule`. Any other
`Resolve` error, such as tied owners, is returned, as it is from `RouteOn`
and the gate. `ModuleSet.Sees` returns only a bool, so it hides a tied
path instead.

### D5: gap explanation and coverage were already clean; the tests pin it

`GET /v1/controls/{id}/gap-explanation` reads and cites only the control
and its evidence records. A citation resolves only against those two
(`gapexplain.Store.Resolve`). `GET /v1/controls/{id}/coverage` returns the
control, its anchor, requirements and an effectiveness number. Neither
names a risk, exception, vendor or policy. No code change was needed. The
new tests pin that with Exceptions switched off for one tenant and on for
another: an exception on the control never resolves as a citation and
never appears in the coverage body.

Control evaluation still counts an active exception (`ResultExcepted`)
when the Exceptions module is switched off, and that reaches coverage's
effectiveness number. That is by design. ADR 0025 decision 2 keeps
evaluation and the ledger out of module filtering, so switching a module
off changes what is displayed, not what is true of the control.

### D6: the demo seed switches the modules on

The ticket allowed either skipping a switched-off module's data or
switching the module on. The seed switches on, in its own transaction,
each module and feature switch that owns data it writes (`seededSwitches`:
risk, decisions log, policies, policy acknowledgments, questionnaires,
vendors, exceptions, board reporting, audit sampling). A switch that is
already on is left alone. Each change writes a `feature_flag_audit_log`
row with actor `demo-seed:<admin id>`, and the keys come back as
`switched_on` in the seed response.

Switching on fits the seed's purpose. A demo tenant exists to show the
product, and skipping would have dropped board packs from every new demo
tenant, because `board.reporting` is off by default. Skipping would also
have had to follow the seed's foreign keys: decisions link to risks, and
acknowledgments link to policies.

Teardown: a whole-tenant teardown now sweeps `feature_flags` and
`feature_flag_audit_log` for the demo tenant, since no foreign key ties
them to the tenant row. An in-place teardown removes only the rows the
seed recorded. A switch row the seed created is removed, so the switch
returns to its default. A row the seed updated from off to on stays on,
and the flag audit log records who switched it.
