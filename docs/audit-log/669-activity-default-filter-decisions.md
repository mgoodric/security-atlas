# Slice 669 — Activity default-filter decisions log

**Type:** JUDGMENT
**Slice:** `docs/issues/669-activity-feed-business-events-default.md`
**Surface:** the tenant Activity feed (`GET /v1/activity/unified` + `web/app/(authed)/activity`).

- detection_tier_actual: none
- detection_tier_target: none

(No bug surfaced during the slice. The pure-Go parse unit + the SQL-layer
integration pin both went green on first run; the classification call was
the only subjective work.)

---

## The classification (the JUDGMENT call)

The "noise" the slice targets is a single, mechanically identifiable class:
the OPA authorization-decision log auditing the app's own **read** requests.
`internal/authz/input.go::actionFromMethodAndPath` maps `GET`/`HEAD`/`OPTIONS`
to `action='read'`; those rows land in `decision_audit_log` and surface in the
unified feed as `kind='decision', action='read'`. On an otherwise-empty tenant
these dominate the feed (201 rows observed, ATLAS-018).

Rather than author a positive "business event" allow-list across the nine
audit-log kinds, the slice ships a **one-tuple deny-list**:

> The default Activity view excludes exactly `(kind='decision' AND action='read')`.
> Everything else is a business event and surfaces by default.

This is the smallest, most defensible cut that satisfies the threat model.

---

## Decisions made

### D1 — Deny-list of one tuple, not an allow-list. **(confidence: high)**

- **Options considered:**
  - (a) A positive allow-list of "business" kinds/actions the default shows.
  - (b) A deny-list of the read-telemetry tuple the default hides.
- **Chosen:** (b). The noise is a single well-defined class (`decision`/`read`);
  an allow-list would have to enumerate every current AND future business
  action across nine kinds and would silently hide any new event type a later
  slice adds (fail-closed in the wrong direction for a _discovery_ surface).
  A deny-list fails open: a new kind/action is visible by default, which is the
  correct bias for an activity feed. It is also the minimal diff.
- **Rationale / pattern-match:** mirrors the slice 270 row-visibility predicate
  shape — one conjunctive boolean-gated WHERE clause that short-circuits when
  the flag is off. Composes with the existing `actor` / `kind` / privilege
  filters without interaction.

### D2 — Filter at the SQL layer, defaulted per-endpoint. **(confidence: high)**

- **Chosen:** a new optional `exclude_read_telemetry BOOLEAN` parameter on the
  shared slice-124 aggregator query. The activity handler sets it `true` by
  default (`!includeReads`); the admin `/v1/admin/audit-log/unified` forensic
  endpoint leaves it `false` (always shows every row — it is the
  show-everything surface).
- **Rationale:** keeps the cut in one place (the SQL the two endpoints share),
  honors invariant #2 (read-side view filter, ledger untouched), and keeps the
  admin forensic endpoint's behavior bit-identical to pre-slice-669.

### D3 — Opt-in via `?include_reads=true`, default-off. **(confidence: high)**

- **Chosen:** the full ledger stays reachable through an explicit
  `?include_reads=true` query param surfaced as a "Show read-telemetry" pill
  on the page (URL-driven, so back/forward + shared links restore it). Default
  (param absent / any non-`"true"` value) is the business-events view.
- **Rationale:** AC-2 requires read-telemetry remain inspectable, not removed.
  An opt-in toggle is the least-surprising affordance and matches the existing
  kind-chip filter idiom on the same page.

### D4 — Toggle is orthogonal to the privilege gate. **(confidence: medium)**

- **Chosen:** `exclude_read_telemetry` is independent of slice 270's
  `caller_is_privileged` row-visibility predicate. Even a privileged
  (`admin` / `auditor` / `grc_engineer`) caller gets the read-telemetry
  de-prioritized by default on the activity surface; the admin audit-log
  endpoint remains the show-everything forensic surface.
- **Rationale:** the activity feed is a human-attention surface for every
  role; the noise problem is the same regardless of privilege. Privilege
  governs which rows you may see, not which rows are worth showing by
  default. Kept separate so a future change to one cannot silently invert
  the other.
- **Why medium:** it is plausible an admin wants the full firehose by default
  on `/activity`. If post-deployment feedback says so, the default for
  privileged callers is a one-line flip — see revisit list.

### D5 — `decision`/`read` is the ONLY tuple denied; mutating decisions stay. **(confidence: high)**

- **Chosen:** `decision` rows whose action is a mutation verb
  (`write`/`approve`/`publish`/`submit`/`push`/…) are business events and are
  NOT denied. Only `action='read'` is dropped.
- **Rationale:** the threat model is explicit — the default must not hide
  security-relevant mutations. A `decision`/`write` row (e.g. an authz decision
  on a state-changing request) is exactly the kind of event a security leader
  wants surfaced. Pinned by `TestSlice669_DefaultExcludesReadTelemetry`
  (asserts a seeded `decision`/`write` + `exception` write both survive the
  default filter).

---

## Revisit once in use

1. **D4 default for privileged callers.** Once a real operator uses `/activity`
   daily, confirm the read-telemetry-hidden default is right for admins too. If
   admins want the firehose by default, flip the activity handler's default for
   `CallerIsPrivileged` callers (one line) — but keep the toggle.
2. **Other high-volume read classes.** `decision`/`read` is today's noise. If a
   future surface emits a different high-volume read-telemetry class (e.g. a
   `me`/`read` self-audit or a polling connector's read events), re-evaluate
   whether the deny-tuple should generalize to a small denied set rather than a
   single tuple. Do NOT pre-generalize now (YAGNI / canvas anti-pattern).
3. **`action` verb stability.** The deny predicate keys on the literal
   `action='read'` produced by `actionFromMethodAndPath`. If that mapping ever
   changes its read verb (e.g. to `'get'` or `'view'`), the deny predicate must
   track it. A guard test in `internal/authz` asserting GET→`'read'` would make
   that coupling explicit; deferred as out-of-scope here.
4. **Default-window interaction.** The page already defaults to a 7-day window;
   confirm the combined "7 days + business-events-only" default is the right
   first-paint for a busy tenant once real volume exists.

---

## Constitutional check

- **Invariant #2 (append-only ledger; ingestion/evaluation separated):**
  honored. This slice adds a read-side WHERE predicate only. No write path, no
  migration, no retention change. The show-all result set is a strict superset
  of the default set (pinned by `TestSlice669_ShowAllSupersetsDefault`); every
  row the default hides is still recorded and still retrievable.
- **Threat model:** the deny tuple is `decision`/`read` only; auth/role/tenant/
  exception/evidence mutations are never `decision`/`read` and always surface
  by default (pinned).
- **No new OPA resource type, no RLS change, no auth change.** The slice 270
  privilege predicate and tenant RLS are untouched and still bound the read.
