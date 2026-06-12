# 732 — Calendar/dashboard exception label: SCF code + name, not raw UUID (decisions log)

**Slice type:** JUDGMENT (copy authorship + a backend query JOIN)
**Surface:** `internal/db/queries/{calendar,dashboard}.sql` (backend SQL) → ICS export + `web/` agenda / month-grid / "Upcoming" panel.

- detection_tier_actual: manual_review
- detection_tier_target: integration

(The raw-UUID label was caught by the 2026-06-10 empty-tenant browser audit
— `manual_review`. The right earlier tier is `integration`: the label is built
in backend SQL and rendered through the HTTP handler, so a real-Postgres
integration assertion on the exception-event title is where a regression of this
copy belongs — and is exactly the tier this slice now adds, on both the calendar
and dashboard surfaces. A pure unit tier cannot reach it: the title is a SQL
expression over a JOIN, not Go string-building. The companion ICS unit tests add
a fast render-tier guard on top.)

## Context

`ATLAS-035` from the pre-GA UI audit: the `/calendar` agenda + month-grid and the
`/dashboard` "Upcoming" panel rendered an expiring exception as
**"Exception on control 32e55da9-…"** — the raw `controls.id` UUID. Parent slice
670 fixed every `web/`-side copy defect from the same audit but was scoped
`web/`-only with an explicit "no backend/Go changes" anti-criterion; this label
is constructed in **backend SQL** (`('Exception on control ' || e.control_id::text)`),
so it spilled out as a backend slice.

The frontend components (`web/components/calendar/{agenda-view,month-grid-view}.tsx`,
`web/components/dashboard/upcoming-panel.tsx`) render `event.title` / `item.title`
verbatim — they carry no control metadata to substitute — so the fix MUST happen
in the query, and the components needed no change.

Two queries carry the identical defect because they share the slice-675
upcoming-event vocabulary (the calendar agenda and the dashboard "Upcoming"
rollup answer the same "what's coming up?" question and must read identically):

- `internal/db/queries/calendar.sql` — exceptions branch of `ListCalendarEvents`.
- `internal/db/queries/dashboard.sql` — exceptions branch of `ListUpcomingItems`.

Both are fixed in this slice so the exception label cannot drift between the two
surfaces (the SHARED UPCOMING-EVENT VOCABULARY note in `dashboard.sql` requires the
matching change).

## Decisions

### D1 — Title template + two-source SCF-code resolution (AC-1, AC-6)

**Chosen format:** `Exception on <code> — <control name>` — e.g.
`Exception on AAA-01 — Access Control Policy`.

This deliberately mirrors the slice-670 **ATLAS-012** control-detail empty-state
wording (`AAA-01` style code, em-dash separator, human control name) so the
exception label and the control-detail label read as one consistent house voice
across surfaces. The separator is the em-dash `—` (U+2014), the same glyph
ATLAS-012 uses.

**Code resolution is two-source, COALESCEd:**

```sql
COALESCE(NULLIF(c.scf_id, ''), a.scf_id)
```

A control links to its SCF anchor two ways, and a control may carry one but not
the other:

1. `controls.scf_id` — the free-form SCF code stamped directly on the control row
   (init migration).
2. `controls.scf_anchor_id` → `scf_anchors.scf_id` — the structured UUID FK to the
   global SCF catalog (slice 009). The demo seed (slice 682) and the posture spine
   populate `scf_anchor_id` but may leave `scf_id` NULL, so resolving only `scf_id`
   would have hit the fallback for anchor-linked controls that DO have a code.

COALESCE prefers the on-row code, then the catalog code reached through the FK.
`NULLIF(c.scf_id, '')` collapses an empty-string code to NULL so an empty `scf_id`
does not win over a real anchor code.

The `<control name>` is `controls.title` (always NOT NULL), NOT
`scf_anchors.title` — we name the operator's own control, not the SCF anchor's
canonical title, consistent with how the control-detail surface names the control.

**Rejected alternative:** building the label in the Go handler (`rowToWire`) after
fetching the control. Rejected per the anti-criterion and for parity with the
audit/policy/vendor branches — every other calendar branch builds its title in
SQL, so the SQL stays the single source of truth and the ICS export inherits the
label for free (it reads `row.Title`).

### D2 — Graceful no-code fallback (AC-2)

When NEITHER code resolves (a data edge case: a control with no `scf_id` and no
`scf_anchor_id`), the `CASE` falls back to the control name alone:
`Exception on <control name>`. It NEVER prints a bare UUID. The control row is
guaranteed present (the exceptions composite FK `(tenant_id, control_id)` →
`controls(tenant_id, id)` with `ON DELETE RESTRICT`), so `c.title` is always
available as the fallback.

### D3 — Tenant safety of the JOINs (invariant #6)

- `JOIN controls c ON c.tenant_id = e.tenant_id AND c.id = e.control_id` — the
  control is tenant-scoped; the join key includes `tenant_id` and the branch's
  existing `e.tenant_id = $1` predicate + RLS bound both sides to the caller's
  tenant. No cross-tenant control row is reachable.
- `LEFT JOIN scf_anchors a ON a.id = c.scf_anchor_id` — `scf_anchors` is the
  platform-global catalog (no `tenant_id`, no RLS — slice 006), readable identically
  by every authenticated caller. Joining it leaks nothing tenant-specific; it only
  resolves a public catalog code. LEFT (not INNER) so a control with a NULL/dangling
  `scf_anchor_id` still produces a row (the D2 fallback then applies).

The existing cross-tenant RLS isolation tests
(`TestCalendar_RLSIsolatesExceptionsAcrossTenants` + the dashboard RLS suite) stay
green, confirming the new JOINs introduce no leak.

## Tests (AC-3, AC-4, AC-5)

- **ICS unit (pure-Go, fast):** `TestRenderICS_ExceptionSummaryUsesSCFCodeNotUUID`
  and `TestRenderICS_ExceptionSummaryFallbackNoSCFCode` assert the rendered
  `SUMMARY` carries the resolved label (code+name, and the name-only fallback) and
  contains no raw UUID. The renderer reads `row.Title` verbatim, so these pin the
  contract the query now satisfies (AC-4).
- **Calendar integration (real Postgres):**
  `TestCalendar_ExceptionTitleUsesSCFCodeNotUUID` seeds a control with
  `scf_id='AAA-01'` + title `Access Control Policy` and an active exception, then
  asserts the returned event title is exactly `Exception on AAA-01 — Access
Control Policy` and contains no control UUID (AC-5).
  `TestCalendar_ExceptionTitleFallsBackWhenNoSCFCode` seeds a control with NULL
  `scf_id` and asserts the name-only fallback, no UUID (AC-2).
- **Dashboard integration (real Postgres):**
  `TestUpcoming_ExceptionTitleUsesSCFCodeNotUUID` exercises the anchor-FK
  resolution path (the dashboard seed links the control via `scf_anchor_id` whose
  anchor `scf_id` is `SCF-<code>`) and asserts the "Upcoming" title starts with the
  resolved SCF code, contains the control name, and contains no UUID — proving the
  COALESCE second branch and the two-surface parity.

Coverage: `internal/api/calendar` measured **81.3%** with `-tags=integration`,
above its **79** floor (`cmd/scripts/coverage-thresholds.json`). `internal/api/dashboard`
is on the integration-tested exclude list (no hard unit floor) — the new test is a
correctness regression guard, not a floor lift.

## Anti-criteria honored

- Calendar event wire shape unchanged — `title` stays a single `string` field; only
  its CONTENT improved.
- Only the exception branch of each UNION changed; the audit / policy / vendor /
  control branches are untouched.
- The title is built in SQL, not Go (the handler + ICS pass `row.Title` through
  unchanged).
- No migration, no proto change (`git diff --stat origin/main...HEAD --
migrations/ proto/` is empty). The `scf_id` / `scf_anchor_id` / `title` columns
  and `scf_anchors` table all pre-exist.

## Cross-surface consistency note (AC-6)

The label composes with the slice-670 ATLAS-012 control-detail empty-state wording:
both name the SCF code in `<code>` form (e.g. `AAA-01`) and the human control name,
with an em-dash separator. An operator who sees `Exception on AAA-01 — Access
Control Policy` on the calendar and `AAA-01` on the control-detail breadcrumb reads
one consistent vocabulary, not two competing renderings of the same control.
