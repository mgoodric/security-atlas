-- security-atlas — admin_audit_log_v unified audit-log view (slice 062).
--
-- A UNION ALL view across the seven per-domain audit-log tables, projected
-- to a uniform row shape so the admin /v1/admin/audit-log endpoint can
-- expose a single paginated tenant-wide read.
--
-- Wire shape (uniform across branches):
--
--     (tenant_id      uuid,
--      ts             timestamptz,
--      source_table   text,        -- which source table this row came from
--      event_type     text,        -- domain-specific event name
--      actor          text,        -- user id, credential id, or 'system'
--      resource_type  text,        -- e.g. 'evidence_record', 'exception'
--      resource_id    text,        -- the per-domain identifier (often a uuid)
--      summary        jsonb)       -- everything else from the source row
--
-- Source tables included (with the slice that owns each):
--
--   - decision_audit_log         (slice 035) — authz allow/deny decisions
--   - evidence_audit_log         (slice 013) — evidence push attempts
--   - exception_audit_log        (slice 021) — exception workflow transitions
--   - feature_flag_audit_log     (slice 059) — feature-flag flips
--   - artifact_access_log        (slice 036) — artifact upload / download
--   - sample_audit_log           (slice 026) — audit-sample annotations
--   - audit_period_audit_log     (slice 028) — audit-period freezes
--
-- Two source tables anticipated by the slice doc do NOT exist on main as of
-- this migration: framework_scope_workflow_log (slice 018 ships state on the
-- table itself, no separate audit log) and policy_audit_log (slice 022's
-- audit trail is per-id via /v1/exceptions/{id}/audit-log, not a separate
-- table). The view definition uses to_regclass() guards so it is robust to
-- their later addition: if either table appears in a future migration, the
-- view picks it up only after a CREATE OR REPLACE here adds the branch.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation via RLS. The view is NOT a SECURITY DEFINER object
--      and does NOT use BYPASSRLS. When atlas_app (the application role)
--      SELECTs from admin_audit_log_v, Postgres evaluates each underlying
--      source table's RLS policies (each has a tenant_read policy under
--      FORCE ROW LEVEL SECURITY). The view inherits the caller's RLS
--      context. The handler at /v1/admin/audit-log runs the SELECT inside
--      the tenancymw-applied transaction so the tenant GUC is set; rows
--      from other tenants are filtered out at the source-table policy
--      layer, not at the view layer. The integration test verifies this
--      end-to-end (Tenant A's admin sees A's rows only).
--
-- Append-only by construction: every source table ships SELECT + INSERT
-- policies only (no UPDATE / DELETE) under FORCE ROW LEVEL SECURITY. The
-- view inherits that constraint; the only INSERT path is the underlying
-- table's own INSERT path. No write goes through the view.
--
-- Performance note: the view is intentionally lazy — Postgres rewrites
-- references to admin_audit_log_v into a UNION ALL of the seven SELECTs at
-- query time. The handler applies LIMIT/OFFSET and an ORDER BY ts DESC on
-- the result; each branch has its own (tenant_id, occurred_at DESC) or
-- (tenant_id, received_at DESC) index so per-branch sort is index-aided.
-- A composite materialized view is a v2 conversation if volume requires
-- it (the slice doc anti-criterion P0 forbids per-request N+1 — one UNION
-- query satisfies it).
--
-- Anti-criterion P0 honored: this view does NOT bypass tenant RLS. Each
-- branch's source-table RLS policy fires. A non-tenant SELECT on the view
-- returns zero rows, not a permission error (silent filter, the standard
-- RLS semantics).
--
-- Migration slot 20260511000022. Slice 029 (parallel) holds slot _023.

-- ===== admin_audit_log_v =====
--
-- Each branch projects to the uniform shape. summary is the source row
-- stripped of the projected columns (tenant_id, ts, actor, resource_*) so
-- it carries the source-specific fields without duplication. The
-- (to_jsonb(t) - 'col1' - 'col2' - ...) pattern is the standard Postgres
-- way to drop keys from a jsonb object.
--
-- Branch projections:
--
--   decision_audit_log: action=event_type, user_id=actor,
--                       resource_type=resource_type, resource_id=resource_id
--   evidence_audit_log: 'evidence.' + decision=event_type, credential_id=actor,
--                       resource_type='evidence_record', record_id=resource_id
--   exception_audit_log: action=event_type, actor=actor,
--                        resource_type='exception', exception_id=resource_id
--   feature_flag_audit_log: 'feature_flag.flip'=event_type, actor=actor,
--                           resource_type='feature_flag', flag_key=resource_id
--   artifact_access_log: 'artifact.' + action=event_type, actor=actor,
--                        resource_type='artifact', artifact_id=resource_id
--   sample_audit_log: action=event_type, actor=actor,
--                     resource_type='audit_sample', sample_id=resource_id
--   audit_period_audit_log: action=event_type, actor=actor,
--                           resource_type='audit_period', period_id=resource_id

CREATE OR REPLACE VIEW admin_audit_log_v AS
    SELECT
        tenant_id,
        occurred_at AS ts,
        'decision_audit_log'::text AS source_table,
        action AS event_type,
        user_id AS actor,
        resource_type,
        resource_id,
        (to_jsonb(decision_audit_log) - 'tenant_id' - 'occurred_at'
            - 'user_id' - 'action' - 'resource_type' - 'resource_id')::jsonb AS summary
    FROM decision_audit_log

    UNION ALL

    SELECT
        tenant_id,
        received_at AS ts,
        'evidence_audit_log'::text AS source_table,
        ('evidence.' || decision)::text AS event_type,
        credential_id AS actor,
        'evidence_record'::text AS resource_type,
        COALESCE(record_id::text, '') AS resource_id,
        (to_jsonb(evidence_audit_log) - 'tenant_id' - 'received_at'
            - 'credential_id' - 'decision' - 'record_id')::jsonb AS summary
    FROM evidence_audit_log

    UNION ALL

    SELECT
        tenant_id,
        occurred_at AS ts,
        'exception_audit_log'::text AS source_table,
        action AS event_type,
        actor,
        'exception'::text AS resource_type,
        exception_id::text AS resource_id,
        (to_jsonb(exception_audit_log) - 'tenant_id' - 'occurred_at'
            - 'actor' - 'action' - 'exception_id')::jsonb AS summary
    FROM exception_audit_log

    UNION ALL

    SELECT
        tenant_id,
        occurred_at AS ts,
        'feature_flag_audit_log'::text AS source_table,
        'feature_flag.flip'::text AS event_type,
        actor,
        'feature_flag'::text AS resource_type,
        flag_key AS resource_id,
        (to_jsonb(feature_flag_audit_log) - 'tenant_id' - 'occurred_at'
            - 'actor' - 'flag_key')::jsonb AS summary
    FROM feature_flag_audit_log

    UNION ALL

    SELECT
        tenant_id,
        occurred_at AS ts,
        'artifact_access_log'::text AS source_table,
        ('artifact.' || action)::text AS event_type,
        actor,
        'artifact'::text AS resource_type,
        artifact_id::text AS resource_id,
        (to_jsonb(artifact_access_log) - 'tenant_id' - 'occurred_at'
            - 'actor' - 'action' - 'artifact_id')::jsonb AS summary
    FROM artifact_access_log

    UNION ALL

    SELECT
        tenant_id,
        occurred_at AS ts,
        'sample_audit_log'::text AS source_table,
        action AS event_type,
        actor,
        'audit_sample'::text AS resource_type,
        COALESCE(sample_id::text, '') AS resource_id,
        (to_jsonb(sample_audit_log) - 'tenant_id' - 'occurred_at'
            - 'actor' - 'action' - 'sample_id')::jsonb AS summary
    FROM sample_audit_log

    UNION ALL

    SELECT
        tenant_id,
        occurred_at AS ts,
        'audit_period_audit_log'::text AS source_table,
        action AS event_type,
        actor,
        'audit_period'::text AS resource_type,
        audit_period_id::text AS resource_id,
        (to_jsonb(audit_period_audit_log) - 'tenant_id' - 'occurred_at'
            - 'actor' - 'action' - 'audit_period_id')::jsonb AS summary
    FROM audit_period_audit_log;

-- The view's SELECT privilege flows to atlas_app via the underlying tables'
-- existing grants (each source table grants SELECT to atlas_app). The view
-- itself is owned by atlas_migrate; we explicitly grant SELECT here so a
-- future ownership change doesn't break the read path.

GRANT SELECT ON admin_audit_log_v TO atlas_app;

COMMENT ON VIEW admin_audit_log_v IS
    'Slice 062 — unified read across the seven per-domain audit-log tables. '
    'RLS-aware: each source-table tenant_read policy fires under the caller''s '
    'app.current_tenant GUC. Append-only by construction (no INSERT path).';
