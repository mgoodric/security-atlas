# 094 — Compliance calendar — decisions log

Slice 094 is `Type: AFK` in its frontmatter — its 20 acceptance
criteria are mechanically verifiable. The slice file's "Notes for the
implementing agent" section explicitly flagged four design questions
the maintainer wanted the Engineer to resolve in-flight and capture
here so the maintainer can iterate post-deployment. This log records
those four plus a handful of build-time calls the implementation
surfaced.

The slice is read-only over four existing source tables
(`audit_periods`, `exceptions`, `policies`, `controls` +
`control_evaluations`). No new write path. No change to existing
write paths or RLS policies on the four sources (anti-criterion
P0-A4). The only schema change in this slice is a single column
addition on `policies` (`next_review_at`) — see decision D4.

---

## Decisions made

### D1. Cadence encoding: read from `controls.freshness_class`, no derived view, no column migration

**Question (slice file AC-2a, "Notes for the implementing agent"):** Is
the periodic-review cadence encoded in `controls/soc2/<id>/control.yaml`
or in the `controls` table — and if only in the bundle, do we surface
it via a derived view or a new column?

**Investigation (read at build time):**

- `controls/soc2/*/control.yaml` declares `freshness_class:` (one of
  `hourly` / `daily` / `weekly` / `monthly` / `quarterly` / `annual`)
  and `implementation_type:` (one of `automated` / `manual_periodic` /
  `manual_attested`).
- The `controls` table already has `freshness_class TEXT NULL` and
  `implementation_type` enum columns (see
  `internal/db/dbx/models.go` lines 940 + 955). The slice-009 control
  bundle importer populates them — the cadence is **already
  queryable**.
- The `evidence_freshness_class` enum in `migrations/sql/20260511000000_init.sql`
  matches the bundle vocabulary minus `hourly` (the `eval` package
  falls back to monthly for unknown classes, per
  `internal/eval/state.go:freshnessMaxAge`).
- "Last evaluation" timestamp is **not** on the `controls` table — it
  derives from `MAX(evaluated_at)` over the append-only
  `control_evaluations` ledger (slice 012).

**Options considered:**

- **(A)** Add a `review_cadence_days` column migration to
  `controls`. Migrate every existing control. Risk a write-path
  regression in the slice-009 bundle importer.
- **(B)** Build a derived view that JOINs bundle metadata against
  control state.
- **(C)** Read from the existing `freshness_class` column directly,
  translating class → cadence-duration with the same
  `eval.FreshnessMaxAge` mapping the evaluation engine already uses.

**Chosen: (C).**

**Rationale.** The cadence is already encoded queryably on the
controls table. The bundle importer writes it. The eval engine reads
it. Adding a `review_cadence_days` column would duplicate the same
information in a second representation and introduce drift between
the two. A derived view is unnecessary because the JOIN is two tables
(`controls` + a `MAX(evaluated_at) GROUP BY control_id` over
`control_evaluations`) — straightforward to express in a single query
with a LATERAL subselect. Option (C) makes the calendar a pure
read-only consumer of existing state, consistent with the spirit of
P0-A4 (no changes to existing write paths) and P0-A9 (no
modifications to the `controls` table beyond surfacing cadence — and
this surfaces nothing new because it is already surfaced).

The class → duration translation uses the SAME
`eval.FreshnessMaxAge(class)` mapping the evaluation engine uses for
freshness checks. One source of truth, one place to revisit if the
table evolves.

**Cadence filter for "is this a periodic-review control":** The slice
narrative defines periodic-review controls as the ones engineering /
IT / business-process owners must perform on a fixed schedule. The
implementation filters to controls where
`implementation_type IN ('manual_periodic', 'manual_attested')` AND
`freshness_class IS NOT NULL`. Automated controls (whose evidence
arrives continuously from a connector) are excluded — they have
freshness but no scheduled human activity to block time for. This
matches the IN-vs-OUT distinction in the slice narrative.

**Revisit once in use:** If a future control bundle wants a
cadence that does NOT match the freshness-class vocabulary (e.g.
"biennial" for board attestations, "semi-annual" for vendor reviews
that are not in `vendors`), we add the enum value to
`evidence_freshness_class` AND extend `eval.freshnessMaxAgeTable` —
the calendar inherits the new value automatically.

**Confidence: high.** The DB columns + the eval mapping were read
directly; the bundle importer behavior was verified against the
control bundle yaml.

---

### D2. Rolling-window cadence is the default

**Question (slice file AC-2b):** Compute `next_due_at` from
`last_evaluated_at + cadence` (rolling-window) OR from the next
calendar-aligned period (fixed-quarter Q1/Q2/Q3/Q4)?

**Chosen: rolling-window only in v1.**

**Rationale.** The slice file authorizes the default. The bundle
schema does not have a `cadence_alignment: rolling | fixed_quarter`
field today, so there is nothing to switch on. Rolling-window
matches the freshness model the eval engine already enforces (a
manual-periodic control with `freshness_class: quarterly` becomes
stale 120d after its most recent evidence, not 120d after the start
of the next calendar quarter). Picking rolling-window keeps the
calendar's `next_due_at` and the freshness model's `stale_at`
referring to the same clock — operators will not see one date in the
calendar and a different date on the control detail page.

**Migration path for fixed-quarter (revisit):** A future control
bundle that wants fixed-quarter alignment adds
`cadence_alignment: fixed_quarter` to its `control.yaml`. The
slice-009 importer surfaces it as a new nullable column on
`controls`. The calendar query branches on the column. The eval
engine inherits the same field. None of this requires breaking the
v1 contract.

**Confidence: high.** The decision is documented and the migration
path is explicit.

---

### D3. ICS auth: per-user opaque token in URL, hashed in `api_keys`, scope-restricted

**Question (slice file AC-8, "Notes for the implementing agent"):**
Calendar clients (Google / Apple / Outlook) do not carry cookies.
How does the ICS endpoint authenticate?

**Chosen: per-user opaque URL token, hashed at rest in `api_keys`,
scope-restricted to a calendar-only allowed-kind.**

**Mechanism:**

1. The user visits `/calendar` (cookie-auth via slice 034 session) and
   clicks "Subscribe in your calendar." The frontend POSTs to a new
   `POST /v1/calendar/subscription` endpoint that mints a random 32-byte
   URL-safe token, stores `sha256(token)` in `api_keys` with
   `allowed_kinds = ['calendar.read.v1']` and a long TTL (default 1
   year), and returns the plaintext token ONCE.
2. The frontend builds the URL `/v1/calendar.ics?token=<plaintext>` and
   copies it to the clipboard.
3. Calendar clients fetch the URL with no cookie. The handler hashes
   the URL `token` query param with `sha256` and looks it up in
   `api_keys` via `credstore.Authenticate`. The credential's
   `AllowedKinds` MUST contain `calendar.read.v1` — otherwise the
   handler returns 403. This prevents a leaked calendar token from
   being used as a general bearer for the rest of the platform API.
4. A future "rotate calendar URL" action revokes the existing
   `api_keys` row and re-mints. POST `/v1/calendar/subscription` is
   idempotent within a user: if the user already has an active
   calendar token, the existing one is reused (the plaintext is NOT
   recoverable — the user has to rotate to get a new URL).

**Why URL token, not header bearer:** Calendar clients are not
configurable for custom headers. A URL token is the standard pattern
(Google Calendar's own iCal URLs work this way). The token is in a
`https://` URL — TLS protects it in flight; the `api_keys` storage
hashes it at rest.

**Why scope-restrict to `calendar.read.v1`:** Existing `api_keys`
allowed-kinds entries restrict what `evidence_kind` rows the
credential can ingest. Adding a non-evidence-kind value
(`calendar.read.v1`) repurposes the same column as the calendar
authorization gate. A leaked calendar URL cannot ingest evidence,
cannot read the catalog, cannot do anything else — the only handler
that accepts `calendar.read.v1` is the ICS export.

**Cache:** `Cache-Control: private, max-age=300` per AC-7. The
handler computes the ICS body fresh on cache miss. A 5-min server
cache (not in v1 — the calendar query is cheap enough that
recomputing per request beats invalidation logic) is a future
optimization.

**Revisit once in use:** If a calendar client misbehaves and polls
faster than 5 min, surface request-rate metrics and consider a
server-side memoization keyed on `(tenant_id, user_id, from, to,
types)`. If the calendar query becomes expensive at scale (many
events × many tenants on a shared host), revisit caching strategy.

**Confidence: high.** The token pattern matches industry standard
(Google Calendar's iCal URL); the `api_keys` reuse keeps token
storage in one place with one hashing pattern.

---

### D4. `policies.next_review_at` column: scope-expand to add via migration

**Question (slice file AC-2, "Notes for the implementing agent"):**
Does the policies table have a `next_review_at` column or similar?
If not, the slice expands to include a migration.

**Investigation:** Read `migrations/sql/20260511000000_init.sql` and
`migrations/sql/20260511000016_policies.sql`. The policies table
has `effective_date`, `version`, `status`, `published_at`,
`superseded_at` — but **NO** `next_review_at` or `review_cycle`
column. Searched `internal/db/dbx/models.go` and confirmed no such
column on `type Policy`.

**Chosen: scope-expand. Add `next_review_at TIMESTAMPTZ NULL` to
the `policies` table via a new migration (`20260516000002_policies_next_review.sql`)
in this slice.**

**Rationale.** The slice file explicitly authorizes the
scope-expansion in AC-2 ("verify exists or add via migration in
this slice"). Without the column, the calendar cannot surface
policy-review events — which is one of the four event types the
slice ships. Punting the column to a follow-up slice would leave
the calendar with only 3 of 4 promised event types on its first
release.

**Migration shape:**

- Single ALTER TABLE adding `next_review_at TIMESTAMPTZ NULL` with
  no default. Default NULL means "no review scheduled" — the
  calendar omits the policy from the calendar feed for those rows.
- Idempotent with `IF NOT EXISTS`.
- Reversible via a sibling `.down.sql` that drops the column.
- NO change to RLS policies. The existing four-policy split
  (read/write/update/delete) on `policies` is the authorization
  surface — the new column inherits it automatically.
- NO backfill. Existing policy rows get NULL. Operators populate
  `next_review_at` going forward via the policy admin UI (separate
  slice — the slice file's P0-A4 explicitly says no changes to
  existing write paths in this slice).

**Future slice to track:** A short follow-up slice should add the
`next_review_at` setter to the policies admin UI (PATCH
`/v1/policies/{id}` field) — the calendar will start surfacing the
events as soon as operators set the date. This is a UX
follow-up, not a blocker — the calendar handles missing-data
gracefully (the policy simply doesn't appear).

**Revisit once in use:** If operators want a review-cadence
_interval_ (e.g. "annual" → recompute `next_review_at` automatically
when a new version publishes), graduate the column to a
`review_cadence` enum + `last_reviewed_at` timestamp and derive
`next_review_at` in the handler. v1 ships the simpler explicit-date
column.

**Confidence: high.** The slice file authorized the expansion; the
migration is minimal and reversible.

---

### D5. Mount calendar routes onto root router (not Mount /), matching slice 020/021 pattern

**Build-time question:** Slice 003's chi router does not accept two
`Mount("/")` calls (panics). Existing slices append routes
individually onto root (`root.Get("/v1/exceptions", ...)`).

**Chosen: Each `/v1/calendar*` route appends to root via
`root.Get` / `root.Post`. The calendar handler exposes a
`RegisterRoutes(r chi.Router)` method that the platform server calls
once.**

**Rationale.** Matches slice 011/020/021/028/064 pattern. No new
router-construction wart.

**Confidence: high.** Verified against existing route registration
in `internal/api/httpserver.go`.

---

### D6. Hand-rolled month grid + agenda — no calendar library (FullCalendar etc.)

**Build-time question (P0-A6):** Slice forbids a calendar library
unless there's a documented tradeoff.

**Chosen: Hand-roll both views.**

**Rationale.** The two views fit on a couple hundred lines of
Tailwind:

- **Agenda view** is a flat list grouped by `YYYY-MM` header. No
  date math beyond `format(date, 'MMM yyyy')` and the existing
  `Intl.DateTimeFormat` for the per-row label.
- **Month grid** is a 7-column CSS grid. The day cells are computed
  once per month with two helpers: `startOfMonth(date)` and a loop
  that fills 35 or 42 cells from the Sunday before the 1st to the
  Saturday after the last day. Events are joined to cells by
  `format(date, 'yyyy-MM-dd')`.

A library like FullCalendar adds ~50KB gzipped, demands its own
event model, and introduces a styling surface that fights Tailwind.
Hand-rolling keeps the bundle slim and the styling consistent with
the rest of the app.

**Revisit once in use:** If operators want recurring-event display
("every Monday at 9 AM"), drag-to-reschedule, or week/day views,
those are real features a library buys. None of them are in v1
scope.

**Confidence: high.** Pattern matches slice 040 (program dashboard
table-as-grid).

---

### D7. Truncation cursor returns `next_from` only — no `cursor` opaque blob

**Build-time question:** Slice 094 AC-5 says pagination is
"date-range only, no offset/limit cursoring." When the result
exceeds 500 events, return `truncated: true` plus a `next_from`
suggestion.

**Chosen: `next_from` is the ISO date of the 500th event's
`starts_at`. The client re-issues
`GET /v1/calendar?from=<next_from>&to=<original_to>` to fetch the
next slice. No opaque cursor blob.**

**Rationale.** Matches the spirit of "date-range only" — the
client's next request is still a date-range query, not a cursor
follow. Events on the boundary date get duplicated in the next
page if a single day has more than the threshold; in practice no
tenant has 500 events on one day, so the duplication risk is
hypothetical. We log a warning when truncation fires so we know if
real tenants hit it.

**Confidence: medium.** If the hypothetical 500-events-on-one-day
case materializes, we'll need a real cursor — but the slice
explicitly forbids that in v1.

---

### D8. Nav placement: "Calendar" between "Dashboard" and "Catalog · SCF" — NOT between Dashboard and Controls

**Build-time question (AC-15):** Slice file says "between Dashboard
and Controls." Existing nav order (from
`web/components/shell/sidebar.tsx`) is:
`Dashboard / Metrics / Catalog · SCF / Controls / …`. There are
three entries between Dashboard and Controls already.

**Chosen: Insert "Calendar" immediately after "Dashboard,"
before "Metrics."**

**Rationale.** The slice's stated intent is "high-visibility cross-
business surface" — placing it right after Dashboard puts it on the
shortest scanning path. The slice file phrase "between Dashboard and
Controls" was clearly written before slice 097's Metrics nav entry
landed. The maintainer can move it later if "right after Dashboard"
proves wrong; a sidebar nav reorder is a one-line change.

**Confidence: medium.** Minor cosmetic call; easy to revert.

---

## Summary table

| ID  | Decision                                                                 | Revisit once in use                                                             | Confidence |
| --- | ------------------------------------------------------------------------ | ------------------------------------------------------------------------------- | ---------- |
| D1  | Cadence read from `controls.freshness_class`, no view / no new column    | When a bundle wants a class outside the freshness-class enum                    | high       |
| D2  | Rolling-window default; fixed-quarter is a future opt-in                 | When the first control bundle requests calendar-quarter alignment               | high       |
| D3  | Per-user opaque URL token; hashed in `api_keys`; `calendar.read.v1` only | When a client polls faster than 5 min OR the query grows expensive at scale     | high       |
| D4  | Scope-expand: add `policies.next_review_at` column in this slice         | When operators want auto-derived review dates from a review_cadence enum        | high       |
| D5  | Routes append to root (chi double-Mount avoidance)                       | Never — pattern-matched against 6+ prior slices                                 | high       |
| D6  | Hand-rolled month grid + agenda; no calendar library                     | If recurring-events / drag-reschedule / week-day views become real requirements | high       |
| D7  | `next_from` truncation cursor (no opaque blob)                           | If single-day event count exceeds 500 in practice                               | medium     |
| D8  | Nav: Calendar slot immediately after Dashboard                           | Trivial sidebar reorder if maintainer wants different placement                 | medium     |
