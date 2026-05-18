-- Slice 124 — unified audit-log aggregation query.
--
-- One SQL statement: UNION ALL across the nine per-domain audit-log tables,
-- each projected to the canonical Entry shape
-- (occurred_at, actor_id, tenant_id, kind, target_type, target_id, action, payload_json),
-- with WHERE filters applied at each branch.
--
-- RLS-aware: the query runs under `tenancy.ApplyTenant` as `atlas_app`. Every
-- branch's source-table tenant_read policy fires; rows from other tenants are
-- silently filtered out at the source-table policy layer. The aggregator never
-- receives or accepts a tenant_id parameter — the implicit session context
-- is the only carrier of tenant identity (slice 124 anti-criterion P0-A5).
--
-- Pagination is cursor-based on (occurred_at, target_id, kind). The cursor is
-- opaque at the API layer; the handler base64-encodes a `{occurred_at, target_id, kind}`
-- tuple. Ordering is `occurred_at DESC, kind ASC, target_id ASC` so the cursor's
-- next-page condition is a single tuple comparison.
--
-- Filters:
--   from / to       — required RFC3339 window (handler enforces <= 90 days)
--   actor_filter    — optional exact match on actor_id
--   kind_filter     — optional comma-joined kinds; '' means "all kinds"
--   cursor_*        — optional opaque cursor tuple from a prior response
--
-- Limit:
--   limit_n is set to 1001 by the caller for a 1000-row page so the handler can
--   detect "more available" without an extra round-trip (slice-062 pattern).

-- name: ListUnifiedAuditLog :many
WITH unified AS (
    -- 1. decision_audit_log
    SELECT
        occurred_at,
        user_id        AS actor_id,
        tenant_id,
        'decision'::text AS kind,
        resource_type  AS target_type,
        resource_id    AS target_id,
        action,
        decision_id    AS row_id,
        (to_jsonb(d) - 'tenant_id' - 'occurred_at' - 'user_id'
            - 'action' - 'resource_type' - 'resource_id')::jsonb AS payload_json
    FROM decision_audit_log d

    UNION ALL

    -- 2. evidence_audit_log (uses received_at + credential_id)
    SELECT
        received_at    AS occurred_at,
        credential_id  AS actor_id,
        tenant_id,
        'evidence'::text AS kind,
        'evidence_record'::text AS target_type,
        COALESCE(record_id::text, '') AS target_id,
        decision       AS action,
        id             AS row_id,
        (to_jsonb(e) - 'tenant_id' - 'received_at' - 'credential_id'
            - 'decision' - 'record_id')::jsonb AS payload_json
    FROM evidence_audit_log e

    UNION ALL

    -- 3. exception_audit_log
    SELECT
        occurred_at,
        actor          AS actor_id,
        tenant_id,
        'exception'::text AS kind,
        'exception'::text AS target_type,
        exception_id::text AS target_id,
        action,
        id             AS row_id,
        (to_jsonb(x) - 'tenant_id' - 'occurred_at' - 'actor'
            - 'action' - 'exception_id')::jsonb AS payload_json
    FROM exception_audit_log x

    UNION ALL

    -- 4. sample_audit_log
    SELECT
        occurred_at,
        actor          AS actor_id,
        tenant_id,
        'sample'::text AS kind,
        'audit_sample'::text AS target_type,
        COALESCE(sample_id::text, '') AS target_id,
        action,
        id             AS row_id,
        (to_jsonb(s) - 'tenant_id' - 'occurred_at' - 'actor'
            - 'action' - 'sample_id')::jsonb AS payload_json
    FROM sample_audit_log s

    UNION ALL

    -- 5. audit_period_audit_log
    SELECT
        occurred_at,
        actor          AS actor_id,
        tenant_id,
        'audit_period'::text AS kind,
        'audit_period'::text AS target_type,
        audit_period_id::text AS target_id,
        action,
        id             AS row_id,
        (to_jsonb(p) - 'tenant_id' - 'occurred_at' - 'actor'
            - 'action' - 'audit_period_id')::jsonb AS payload_json
    FROM audit_period_audit_log p

    UNION ALL

    -- 6. aggregation_rule_audit_log (uses created_at + event, not occurred_at + action)
    SELECT
        created_at     AS occurred_at,
        actor          AS actor_id,
        tenant_id,
        'aggregation_rule'::text AS kind,
        'aggregation_rule'::text AS target_type,
        rule_id::text  AS target_id,
        event          AS action,
        id             AS row_id,
        (to_jsonb(r) - 'tenant_id' - 'created_at' - 'actor'
            - 'event' - 'rule_id')::jsonb AS payload_json
    FROM aggregation_rule_audit_log r

    UNION ALL

    -- 7. feature_flag_audit_log (synthesizes action since the underlying table only carries from/to booleans)
    SELECT
        occurred_at,
        actor          AS actor_id,
        tenant_id,
        'feature_flag'::text AS kind,
        'feature_flag'::text AS target_type,
        flag_key       AS target_id,
        CASE
            WHEN from_enabled = false AND to_enabled = true  THEN 'feature_flag.enable'
            WHEN from_enabled = true  AND to_enabled = false THEN 'feature_flag.disable'
            ELSE 'feature_flag.flip'
        END            AS action,
        id             AS row_id,
        (to_jsonb(f) - 'tenant_id' - 'occurred_at' - 'actor'
            - 'flag_key')::jsonb AS payload_json
    FROM feature_flag_audit_log f

    UNION ALL

    -- 8. me_audit_log
    SELECT
        occurred_at,
        user_id::text  AS actor_id,
        tenant_id,
        'me'::text AS kind,
        'user'::text AS target_type,
        user_id::text  AS target_id,
        action,
        id             AS row_id,
        (to_jsonb(m) - 'tenant_id' - 'occurred_at' - 'user_id'
            - 'action')::jsonb AS payload_json
    FROM me_audit_log m

    UNION ALL

    -- 9. walkthrough_audit_log
    SELECT
        occurred_at,
        actor          AS actor_id,
        tenant_id,
        'walkthrough'::text AS kind,
        'walkthrough'::text AS target_type,
        walkthrough_id::text AS target_id,
        action,
        id             AS row_id,
        (to_jsonb(w) - 'tenant_id' - 'occurred_at' - 'actor'
            - 'action' - 'walkthrough_id')::jsonb AS payload_json
    FROM walkthrough_audit_log w
)
-- Slice 129 — LEFT JOIN onto users to resolve human-readable actor_name.
--
-- The JOIN is guarded two ways:
--   1. ON u.tenant_id = unified.tenant_id  — defense-in-depth tenant scope on
--      the JOIN itself, on top of the users-table RLS policy. RLS is the
--      load-bearing contract (atlas_app role); the explicit predicate is a
--      second leg so an accidental ROLE elevation cannot leak cross-tenant
--      names through this query.
--   2. unified.actor_id matches a UUID literal — many actor_id values are NOT
--      UUIDs (evidence kind uses credential_id like 'key_foo'; me kind uses
--      user_id::text which IS a UUID; system actors use 'seeder' etc.).
--      Casting a non-UUID string to ::uuid raises an error inside Postgres,
--      so the JOIN predicate first rejects non-UUID actor_ids via a regex.
--
-- Rows whose actor_id does not resolve to a users row emit actor_name=NULL
-- (the wire shape tolerates null — bootstrap-key + credential-only callers
-- have no users row).
SELECT
    unified.occurred_at,
    unified.actor_id,
    unified.tenant_id,
    unified.kind,
    unified.target_type,
    unified.target_id,
    unified.action,
    unified.row_id,
    unified.payload_json,
    u.display_name AS actor_name
FROM unified
LEFT JOIN users u
  ON u.tenant_id = unified.tenant_id
 AND unified.actor_id ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
 AND u.id = unified.actor_id::uuid
WHERE unified.occurred_at >= sqlc.arg('from_ts')::timestamptz
  AND unified.occurred_at <  sqlc.arg('to_ts')::timestamptz
  AND (sqlc.arg('actor_filter')::text = '' OR unified.actor_id = sqlc.arg('actor_filter')::text)
  AND (sqlc.arg('kind_filter_csv')::text = ''
       OR unified.kind = ANY(string_to_array(sqlc.arg('kind_filter_csv')::text, ',')))
  -- Cursor: occurred_at strictly less, OR same occurred_at and a strictly-greater
  -- (kind, row_id) tuple. row_id is the underlying audit-log row's UUID PK and
  -- is GUARANTEED unique per row across the UNION (because each base table's PK
  -- is independently unique and the kind discriminator separates branches).
  AND (sqlc.arg('cursor_ts')::timestamptz IS NULL
       OR unified.occurred_at < sqlc.arg('cursor_ts')::timestamptz
       OR (unified.occurred_at = sqlc.arg('cursor_ts')::timestamptz
           AND (unified.kind, unified.row_id::text) > (sqlc.arg('cursor_kind')::text, sqlc.arg('cursor_row_id')::text)))
ORDER BY unified.occurred_at DESC, unified.kind ASC, unified.row_id ASC
LIMIT sqlc.arg('limit_n')::integer;
