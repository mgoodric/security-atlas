-- Slice 094 — compliance calendar backend read query.
--
-- ONE UNION ALL across five event sources:
--
--   1. audit_periods           — period_end is the audit's "report due" date
--   2. exceptions              — expires_at is the waiver-lapse date
--   3. policies                — next_review_at is the next review date
--                                 (published only; overdue / due-soon, #1806)
--   4. vendors                 — vendor_review_due_date() is the next
--      vendor-review date, the one the vendor overdue list and burndown use
--      (#2232), never-reviewed vendors' first review included.
--      Also includes renewal_date as a separate vendor renewal event (OE-623).
--   5. controls + control_evaluations — periodic-review controls whose
--      cadence (derived from freshness_class) places their next review
--      between $from and $to. last_evaluated_at = MAX(evaluated_at) over
--      the append-only control_evaluations ledger.
--   6. personnel_security_checklists — open offboarding due dates, so overdue
--      leaver access-removal tasks surface on the compliance calendar.
--   7. risks                   — review_due_at is the next risk review (#2130)
--   8. action_plans            — due_date of an open plan (#2130)
--   9. policy_acknowledgments  — the latest ack per (policy version, user)
--      + 365 days, when the annual re-acknowledgement lapses (#2130; it
--      moved here from the retired dashboard upcoming rollup), grouped per
--      policy and lapse day (#2234)
--
-- Past-due items: every branch filters on its own date against [from, to),
-- so a window that starts in the past returns the items that fell due in
-- it. The dashboard's "Coming up" asks for today + 14 days.
--
-- All event sources are tenant-scoped; RLS fires on each underlying SELECT, and the
-- explicit tenant_id predicates are the primary guarantee.
--
-- Date filter is a half-open window [from, to). The window is computed in
-- application code (default 90d forward from now()) and passed in as
-- timestamptz bounds.
--
-- Type filter (`type_filter`) is a CSV string. Empty string ('') means
-- "every source." A non-empty filter narrows to the subset by exact
-- membership of the per-branch literal type in the CSV (#2130: it was a
-- substring match, which `policy_ack` would have tripped for `policy`).
-- The handler also narrows it by the caller's OPA access (risk,
-- action_plan) before it reaches this query.
--
-- Cadence math for the controls branch:
--   - The control's freshness_class is mapped to a max-acceptable-age
--     interval. The mapping is the same one `internal/eval/state.go`
--     `FreshnessMaxAge` exposes (realtime 24h, daily 7d, weekly 30d,
--     monthly 90d, quarterly 120d, annual 400d). We replicate the mapping
--     here as a CASE expression so the SQL is self-contained. Every
--     `CASE c.freshness_class` block below is pinned to the Go table by
--     internal/eval/calendar_freshness_sync_test.go (#2868): change both.
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
      AND (sqlc.arg(type_filter)::text = '' OR 'audit' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- exception expirations: active waivers ordered by when they lapse.
    -- Slice 732: the title resolves the control UUID to its human SCF code
    -- + control name (controls.title) so the agenda / month-grid / ICS read
    -- "Exception on MON-04 — <name>" instead of the raw
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
      AND (sqlc.arg(type_filter)::text = '' OR 'exception' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- policy review cycles: published policies with next_review_at set.
    -- #1806: status is the review state. #2232: it is '' here and the store
    -- fills it with duedate.Status and internal/policy ReviewDueSoonDays,
    -- the rule internal/policy ReviewStatus applies to the policy list, so
    -- the two cannot drift at the UTC day boundary. The policy lifecycle
    -- state moves to summary. Only the published chain tip has a
    -- review due; superseded rows keep their date as history.
    SELECT
        p.id::text                                                    AS event_id,
        'policy'::text                                                AS event_type,
        ('Policy review: ' || p.title)::text                          AS title,
        p.next_review_at::timestamptz                                 AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        p.id::text                                                    AS related_entity_id,
        'policy'::text                                                AS related_entity_kind,
        p.status                                                      AS summary,
        ''::text                                                      AS status,
        NULL::text                                                    AS cadence
    FROM policies p
    WHERE p.tenant_id = $1
      AND p.status = 'published'
      AND p.next_review_at IS NOT NULL
      AND p.next_review_at::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND p.next_review_at::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'policy' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- vendor reviews: vendor_review_due_date is the review due date (#2232),
    -- the one the vendor overdue list and burndown use: last_review_date +
    -- cadence, or for a vendor never reviewed its first-review date
    -- (created_at's UTC date + the tenant's initial-review window, #2061).
    -- The date is placed at 00:00 UTC. status is filled by the store with
    -- duedate.Status, so a vendor is overdue here exactly when it is in
    -- ListOverdueVendors.
    SELECT
        v.id::text                                                    AS event_id,
        'vendor'::text                                                AS event_type,
        ('Vendor review: ' || v.name)::text                           AS title,
        (vd.due_date::timestamp AT TIME ZONE 'UTC')::timestamptz      AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        v.id::text                                                    AS related_entity_id,
        'vendor'::text                                                AS related_entity_kind,
        v.criticality::text                                           AS summary,
        ''::text                                                      AS status,
        v.review_cadence::text                                        AS cadence
    FROM vendors v
    CROSS JOIN LATERAL (
        SELECT vendor_review_due_date(
                   v.last_review_date, v.review_cadence, v.created_at,
                   COALESCE((SELECT t.vendor_initial_review_days FROM tenants t WHERE t.id = v.tenant_id), 30)
               )::date AS due_date
    ) vd
    WHERE v.tenant_id = $1
      AND (vd.due_date::timestamp AT TIME ZONE 'UTC') >= sqlc.arg(from_ts)::timestamptz
      AND (vd.due_date::timestamp AT TIME ZONE 'UTC') <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'vendor' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- vendor contract renewals: renewal_date from the commercial tooling
    -- register. Event type stays `vendor` so existing calendar filters and
    -- links continue to work; the title distinguishes review vs renewal.
    SELECT
        ('renewal-' || v.id::text)                                  AS event_id,
        'vendor'::text                                               AS event_type,
        ('Vendor renewal: ' || v.name)::text                         AS title,
        v.renewal_date::timestamptz                                  AS starts_at,
        NULL::timestamptz                                            AS ends_at,
        v.id::text                                                   AS related_entity_id,
        'vendor'::text                                               AS related_entity_kind,
        COALESCE(v.currency || ' ' || v.annual_cost::text, v.commercial_status::text) AS summary,
        v.commercial_status::text                                    AS status,
        v.billing_cadence::text                                      AS cadence
    FROM vendors v
    WHERE v.tenant_id = $1
      AND v.renewal_date IS NOT NULL
      AND v.commercial_status <> 'churned'
      AND v.renewal_date::timestamptz >= sqlc.arg(from_ts)::timestamptz
      AND v.renewal_date::timestamptz <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'vendor' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

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
          -- #3071: a replay does not count as the control's last review.
          AND ce.evaluated_as_of IS NULL
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
      AND (sqlc.arg(type_filter)::text = '' OR 'control' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- risk reviews (#2130): every risk with a review_due_at, roll-ups
    -- included. A roll-up's review date is its own (#2059 sets one on every
    -- risk), and each risk is one row here, so a child listed under a
    -- roll-up is not listed twice. status is '' in SQL: the store fills it
    -- with duedate.Status (overdue after the UTC due day, due-soon within
    -- 14 days), the one rule the policy list shares.
    SELECT
        r.id::text                                                    AS event_id,
        'risk'::text                                                  AS event_type,
        ('Risk review: ' || r.title)::text                            AS title,
        r.review_due_at                                               AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        r.id::text                                                    AS related_entity_id,
        'risk'::text                                                  AS related_entity_kind,
        r.treatment::text                                             AS summary,
        ''::text                                                      AS status,
        NULL::text                                                    AS cadence
    FROM risks r
    WHERE r.tenant_id = $1
      AND r.status = 'open'
      AND r.review_due_at IS NOT NULL
      AND r.review_due_at >= sqlc.arg(from_ts)::timestamptz
      AND r.review_due_at <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'risk' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- action-plan due dates (#2130): open plans (not completed, verified or
    -- tombstoned) with a due_date. due_date is a DATE, placed at 00:00 UTC
    -- so the event's day does not move with the session time zone. status
    -- is filled by the store, as for risk reviews.
    SELECT
        ap.id::text                                                   AS event_id,
        'action_plan'::text                                           AS event_type,
        ('Action plan due: ' || ap.title)::text                       AS title,
        (ap.due_date::timestamp AT TIME ZONE 'UTC')                   AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        ap.id::text                                                   AS related_entity_id,
        'action_plan'::text                                           AS related_entity_kind,
        ap.status                                                     AS summary,
        ''::text                                                      AS status,
        NULL::text                                                    AS cadence
    FROM action_plans ap
    WHERE ap.tenant_id = $1
      AND ap.tombstoned_at IS NULL
      AND ap.status NOT IN ('completed', 'verified')
      AND ap.due_date IS NOT NULL
      AND (ap.due_date::timestamp AT TIME ZONE 'UTC') >= sqlc.arg(from_ts)::timestamptz
      AND (ap.due_date::timestamp AT TIME ZONE 'UTC') <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'action_plan' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    -- policy acknowledgement expiries (#2130, #2234): the freshest ack per
    -- (policy_version, user) plus 365 days, when that attestation lapses,
    -- grouped into one event per policy and lapse day so a tenant-wide ack
    -- campaign is one row, not one per user, under the 500-row cap and in
    -- "Coming up". Only acks of a published policy version by an active
    -- user count: a superseded version needs a fresh ack anyway (policies
    -- have no other retired state), and a deactivated user's lapse is not
    -- work anyone has to chase. The title names the policy and the count;
    -- who acknowledged is not shown to calendar readers or ICS subscribers.
    -- The related entity is the policy version. status is filled by the
    -- store with duedate.Status, as for risk reviews.
    SELECT
        (acks.policy_version_id::text || ':' ||
            to_char(acks.lapse_day, 'YYYY-MM-DD'))::text              AS event_id,
        'policy_ack'::text                                            AS event_type,
        (p.title || ': ' || acks.n::text ||
            CASE WHEN acks.n = 1 THEN ' acknowledgement lapses on '
                 ELSE ' acknowledgements lapse on ' END ||
            to_char(acks.lapse_day, 'YYYY-MM-DD'))::text              AS title,
        acks.first_lapse                                              AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        acks.policy_version_id::text                                  AS related_entity_id,
        'policy'::text                                                AS related_entity_kind,
        p.title::text                                                 AS summary,
        ''::text                                                      AS status,
        NULL::text                                                    AS cadence
    FROM (
        SELECT
            latest_ack.policy_version_id,
            (latest_ack.lapses_at AT TIME ZONE 'UTC')::date           AS lapse_day,
            min(latest_ack.lapses_at)                                 AS first_lapse,
            count(*)                                                  AS n
        FROM (
            SELECT DISTINCT ON (pa.policy_version_id, pa.user_id)
                pa.policy_version_id,
                (pa.acknowledged_at + INTERVAL '365 days')::timestamptz AS lapses_at
            FROM policy_acknowledgments pa
            JOIN policies pv
              ON pv.tenant_id = pa.tenant_id
             AND pv.id = pa.policy_version_id
             AND pv.status = 'published'
            JOIN users u
              ON u.tenant_id = pa.tenant_id
             AND u.id = pa.user_id
             AND u.status = 'active'
             AND u.active
            WHERE pa.tenant_id = $1
            ORDER BY pa.policy_version_id, pa.user_id, pa.acknowledged_at DESC
        ) latest_ack
        WHERE latest_ack.lapses_at >= sqlc.arg(from_ts)::timestamptz
          AND latest_ack.lapses_at <  sqlc.arg(to_ts)::timestamptz
        GROUP BY latest_ack.policy_version_id, (latest_ack.lapses_at AT TIME ZONE 'UTC')::date
    ) acks
    JOIN policies p
      ON p.tenant_id = $1
     AND p.id = acks.policy_version_id
    WHERE (sqlc.arg(type_filter)::text = '' OR 'policy_ack' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))

    UNION ALL

    SELECT
        psc.id::text                                                  AS event_id,
        'offboarding'::text                                           AS event_type,
        ('Offboarding access removal: ' ||
            COALESCE(NULLIF(psc.person_work_email, ''), psc.person_external_id))::text AS title,
        psc.due_at                                                    AS starts_at,
        NULL::timestamptz                                             AS ends_at,
        psc.id::text                                                  AS related_entity_id,
        'personnel_security_checklist'::text                          AS related_entity_kind,
        psc.source                                                    AS summary,
        CASE
            WHEN psc.due_at < sqlc.arg(now_ts)::timestamptz THEN 'overdue'
            WHEN psc.due_at <= sqlc.arg(now_ts)::timestamptz + INTERVAL '1 day' THEN 'due-soon'
            ELSE 'upcoming'
        END                                                           AS status,
        NULL::text                                                    AS cadence
    FROM personnel_security_checklists psc
    WHERE psc.tenant_id = $1
      AND psc.workflow_kind = 'offboarding'
      AND psc.status = 'open'
      AND psc.due_at >= sqlc.arg(from_ts)::timestamptz
      AND psc.due_at <  sqlc.arg(to_ts)::timestamptz
      AND (sqlc.arg(type_filter)::text = '' OR 'offboarding' = ANY(string_to_array(sqlc.arg(type_filter)::text, ',')))
) calendar_events
ORDER BY starts_at ASC, event_id ASC
LIMIT sqlc.arg(row_limit)::int;
