-- Slice 094 — compliance calendar backend read query.
--
-- ONE UNION ALL across five event sources:
--
--   1. audit_periods           — period_end is the audit's "report due" date
--   2. exceptions              — expires_at is the waiver-lapse date
--   3. policies                — next_review_at is the next review date
--   4. vendors                 — last_review_date + review_cadence interval is
--      the next vendor-review date. Mirrors the dashboard "Upcoming" rollup's
--      vendor branch so the two surfaces cannot drift (slice 675).
--   5. controls + control_evaluations — periodic-review controls whose
--      cadence (derived from freshness_class) places their next review
--      between $from and $to. last_evaluated_at = MAX(evaluated_at) over
--      the append-only control_evaluations ledger.
--
-- All four are tenant-scoped; RLS fires on each underlying SELECT, and the
-- explicit tenant_id predicates are the primary guarantee.
--
-- Date filter is a half-open window [from, to). The window is computed in
-- application code (default 90d forward from now()) and passed in as
-- timestamptz bounds.
--
-- Type filter (`type_filter`) is a CSV string. Empty string ('') means
-- "all five sources." A non-empty filter narrows to the subset by checking
-- membership on the per-branch literal type discriminator.
--
-- Cadence math for the controls branch:
--   - The control's freshness_class is mapped to a max-acceptable-age
--     interval. The mapping is the same one `internal/eval/state.go`
--     `FreshnessMaxAge` exposes (realtime 24h, daily 7d, weekly 30d,
--     monthly 90d, quarterly 120d, annual 400d). We replicate the mapping
--     here as a CASE expression so the SQL is self-contained.
--   - next_due_at = last_evaluated_at + cadence, OR now() when the
--     control has never been evaluated (LEFT JOIN -> NULL).
--   - status: 'overdue' when next_due_at < now(); 'due-soon' when
--     next_due_at <= now() + 14d; otherwise 'upcoming'.
--   - Only `manual_periodic` and `manual_attested` controls are included
--     (the periodic-review IN-vs-OUT filter from the slice narrative).
--   - Controls with NULL freshness_class are excluded (no cadence ->
--     no scheduled review).
--
-- Result is ordered by starts_at ascending, then by event_id ascending for
-- a deterministic order.
--
-- Returns at most `row_limit` rows so the handler can detect the
-- truncation at the 500-event threshold with a +1 probe.

-- name: ListCalendarEvents :many
SELECT
    event_id,
    event_type,
    title,
    starts_at,
    ends_at,
    related_entity_id,
    related_entity_kind,
    summary,
    status,
    cadence
FROM (
    -- audit periods: period_end is when the audit's report is due.
    SELECT
        ap.id::text                                                   AS event_id,
        'audit'::text                                                 AS event_type,
        ('Audit period: ' || ap.name)::text                           AS title,
        ap.period_end::timestamptz                                    AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        ap.id::text                                                   AS related_entity_id,
        'audit_period'::text                                          AS related_entity_kind,
        ap.status                                                     AS summary,
        ap.status                                                     AS status,
        NULL::text                                                    AS cadence
    FROM audit_periods ap
    WHERE ap.tenant_id = $1
      AND ap.period_end::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND ap.period_end::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR position('audit' IN sqlc.arg(type_filter)::text) > 0)

    UNION ALL

    -- exception expirations: active waivers ordered by when they lapse.
    -- Slice 732: the title resolves the control UUID to its human SCF code
    -- + control name (controls.title) so the agenda / month-grid / ICS read
    -- "Exception on AAA-01 — <name>" instead of the raw
    -- "Exception on control <uuid>". The SCF code is resolved two ways and
    -- COALESCEd: (1) controls.scf_id, the code stamped directly on the
    -- control row; (2) the global scf_anchors.scf_id reached through the
    -- structured controls.scf_anchor_id FK (slice 009 linkage). The control
    -- JOIN is on the composite (tenant_id, control_id) FK target — the row
    -- is guaranteed to exist and is tenant-scoped (controls(tenant_id, id)),
    -- so it crosses no tenant boundary (canvas invariant #6). scf_anchors is
    -- the global platform catalog (no tenant_id, no RLS — slice 006), so the
    -- LEFT JOIN to it leaks nothing tenant-specific. When NEITHER code
    -- resolves (a data edge case), the title falls back to the control name
    -- alone; it NEVER prints a bare UUID (AC-2).
    SELECT
        e.id::text                                                    AS event_id,
        'exception'::text                                             AS event_type,
        ('Exception on ' || CASE
            WHEN COALESCE(NULLIF(c.scf_id, ''), a.scf_id) IS NOT NULL
                THEN COALESCE(NULLIF(c.scf_id, ''), a.scf_id) || ' — ' || c.title
            ELSE c.title
        END)::text                                                    AS title,
        e.expires_at::timestamptz                                     AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        e.id::text                                                    AS related_entity_id,
        'exception'::text                                             AS related_entity_kind,
        e.justification                                               AS summary,
        e.status                                                      AS status,
        NULL::text                                                    AS cadence
    FROM exceptions e
    JOIN controls c
      ON c.tenant_id = e.tenant_id
     AND c.id = e.control_id
    LEFT JOIN scf_anchors a
      ON a.id = c.scf_anchor_id
    WHERE e.tenant_id = $1
      AND e.status = 'active'
      AND e.expires_at IS NOT NULL
      AND e.expires_at::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND e.expires_at::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR position('exception' IN sqlc.arg(type_filter)::text) > 0)

    UNION ALL

    -- policy review cycles: policies with next_review_at set.
    SELECT
        p.id::text                                                    AS event_id,
        'policy'::text                                                AS event_type,
        ('Policy review: ' || p.title)::text                          AS title,
        p.next_review_at::timestamptz                                 AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        p.id::text                                                    AS related_entity_id,
        'policy'::text                                                AS related_entity_kind,
        p.status                                                      AS summary,
        p.status                                                      AS status,
        NULL::text                                                    AS cadence
    FROM policies p
    WHERE p.tenant_id = $1
      AND p.next_review_at IS NOT NULL
      AND p.next_review_at::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND p.next_review_at::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR position('policy' IN sqlc.arg(type_filter)::text) > 0)

    UNION ALL

    -- vendor reviews: last_review_date + cadence interval is the next-review
    -- date. Mirrors the dashboard "Upcoming" rollup's vendor branch
    -- (internal/db/queries/dashboard.sql ListUpcomingItems) so the two
    -- surfaces source the SAME vendor-review events (slice 675 AC-1/AC-3).
    -- Vendors with no last_review_date are excluded — there is no anchor to
    -- project a next-review date from. The cadence -> interval mapping is the
    -- vendor_review_cadence enum (monthly/quarterly/biannual/annual); the
    -- ELSE-less CASE is total over the enum so next_due_at is never NULL.
    SELECT
        v.id::text                                                    AS event_id,
        'vendor'::text                                                AS event_type,
        ('Vendor review: ' || v.name)::text                           AS title,
        (v.last_review_date + CASE v.review_cadence
            WHEN 'monthly'   THEN INTERVAL '1 month'
            WHEN 'quarterly' THEN INTERVAL '3 months'
            WHEN 'biannual'  THEN INTERVAL '6 months'
            WHEN 'annual'    THEN INTERVAL '12 months'
         END)::timestamptz                                            AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        v.id::text                                                    AS related_entity_id,
        'vendor'::text                                                AS related_entity_kind,
        v.criticality::text                                           AS summary,
        v.review_cadence::text                                        AS status,
        v.review_cadence::text                                        AS cadence
    FROM vendors v
    WHERE v.tenant_id = $1
      AND v.last_review_date IS NOT NULL
      AND (v.last_review_date + CASE v.review_cadence
            WHEN 'monthly'   THEN INTERVAL '1 month'
            WHEN 'quarterly' THEN INTERVAL '3 months'
            WHEN 'biannual'  THEN INTERVAL '6 months'
            WHEN 'annual'    THEN INTERVAL '12 months'
           END)::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND (v.last_review_date + CASE v.review_cadence
            WHEN 'monthly'   THEN INTERVAL '1 month'
            WHEN 'quarterly' THEN INTERVAL '3 months'
            WHEN 'biannual'  THEN INTERVAL '6 months'
            WHEN 'annual'    THEN INTERVAL '12 months'
           END)::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR position('vendor' IN sqlc.arg(type_filter)::text) > 0)

    UNION ALL

    -- periodic control reviews: manual_periodic / manual_attested controls
    -- whose next_due_at falls in the window. Two cases:
    --   (a) never evaluated   -> next_due_at = now()        status=overdue
    --   (b) has evaluation    -> next_due_at = last + cadence
    -- AC-2c says a never-evaluated control with a defined cadence is the
    -- highest-priority signal — emit it AT now() with status=overdue so
    -- it sits at the top of the agenda regardless of from/to bounds (as
    -- long as the window includes now()).
    SELECT
        c.id::text                                                    AS event_id,
        'control'::text                                               AS event_type,
        ('Control review: ' || c.title)::text                         AS title,
        CASE
            WHEN le.last_evaluated_at IS NULL THEN sqlc.arg(now_ts)::timestamptz
            ELSE le.last_evaluated_at +
                CASE c.freshness_class
                    WHEN 'realtime'  THEN INTERVAL '1 day'
                    WHEN 'daily'     THEN INTERVAL '7 days'
                    WHEN 'weekly'    THEN INTERVAL '30 days'
                    WHEN 'monthly'   THEN INTERVAL '90 days'
                    WHEN 'quarterly' THEN INTERVAL '120 days'
                    WHEN 'annual'    THEN INTERVAL '400 days'
                    ELSE                 INTERVAL '90 days'
                END
        END                                                           AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        c.id::text                                                    AS related_entity_id,
        'control'::text                                               AS related_entity_kind,
        c.freshness_class::text                                       AS summary,
        CASE
            WHEN le.last_evaluated_at IS NULL                              THEN 'overdue'
            WHEN (le.last_evaluated_at +
                  CASE c.freshness_class
                      WHEN 'realtime'  THEN INTERVAL '1 day'
                      WHEN 'daily'     THEN INTERVAL '7 days'
                      WHEN 'weekly'    THEN INTERVAL '30 days'
                      WHEN 'monthly'   THEN INTERVAL '90 days'
                      WHEN 'quarterly' THEN INTERVAL '120 days'
                      WHEN 'annual'    THEN INTERVAL '400 days'
                      ELSE                 INTERVAL '90 days'
                  END) <  sqlc.arg(now_ts)::timestamptz                    THEN 'overdue'
            WHEN (le.last_evaluated_at +
                  CASE c.freshness_class
                      WHEN 'realtime'  THEN INTERVAL '1 day'
                      WHEN 'daily'     THEN INTERVAL '7 days'
                      WHEN 'weekly'    THEN INTERVAL '30 days'
                      WHEN 'monthly'   THEN INTERVAL '90 days'
                      WHEN 'quarterly' THEN INTERVAL '120 days'
                      WHEN 'annual'    THEN INTERVAL '400 days'
                      ELSE                 INTERVAL '90 days'
                  END) <= sqlc.arg(now_ts)::timestamptz + INTERVAL '14 days' THEN 'due-soon'
            ELSE                                                            'upcoming'
        END                                                           AS status,
        c.freshness_class::text                                       AS cadence
    FROM controls c
    LEFT JOIN LATERAL (
        SELECT MAX(ce.evaluated_at)::timestamptz AS last_evaluated_at
        FROM control_evaluations ce
        WHERE ce.tenant_id = c.tenant_id
          AND ce.control_id = c.id
    ) le ON TRUE
    WHERE c.tenant_id = $1
      AND c.superseded_by IS NULL
      AND c.lifecycle_state = 'active'
      AND c.freshness_class IS NOT NULL
      AND c.implementation_type IN ('manual_periodic', 'manual_attested')
      AND (
            CASE
                WHEN le.last_evaluated_at IS NULL THEN sqlc.arg(now_ts)::timestamptz
                ELSE le.last_evaluated_at +
                    CASE c.freshness_class
                        WHEN 'realtime'  THEN INTERVAL '1 day'
                        WHEN 'daily'     THEN INTERVAL '7 days'
                        WHEN 'weekly'    THEN INTERVAL '30 days'
                        WHEN 'monthly'   THEN INTERVAL '90 days'
                        WHEN 'quarterly' THEN INTERVAL '120 days'
                        WHEN 'annual'    THEN INTERVAL '400 days'
                        ELSE                 INTERVAL '90 days'
                    END
            END
          ) >= sqlc.arg(from_ts)::timestamptz
      AND (
            CASE
                WHEN le.last_evaluated_at IS NULL THEN sqlc.arg(now_ts)::timestamptz
                ELSE le.last_evaluated_at +
                    CASE c.freshness_class
                        WHEN 'realtime'  THEN INTERVAL '1 day'
                        WHEN 'daily'     THEN INTERVAL '7 days'
                        WHEN 'weekly'    THEN INTERVAL '30 days'
                        WHEN 'monthly'   THEN INTERVAL '90 days'
                        WHEN 'quarterly' THEN INTERVAL '120 days'
                        WHEN 'annual'    THEN INTERVAL '400 days'
                        ELSE                 INTERVAL '90 days'
                    END
            END
          ) <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR position('control' IN sqlc.arg(type_filter)::text) > 0)
) calendar_events
ORDER BY starts_at ASC, event_id ASC
LIMIT sqlc.arg(row_limit)::int;
