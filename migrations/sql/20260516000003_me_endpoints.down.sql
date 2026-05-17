-- Reverse slice 108 schema additions.
--
-- Restores the admin_audit_log_v view to its slice 062 seven-branch form (drops the
-- me_audit_log branch), drops the me_audit_log + user_notification_preferences tables,
-- and removes the users.time_zone column. The slice 034 INSERT path is unaffected by
-- the column drop since slice 034 never references time_zone.

-- ===== restore slice 062 view (seven branches, no me_audit_log) =====
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

GRANT SELECT ON admin_audit_log_v TO atlas_app;

DROP TABLE IF EXISTS me_audit_log;
DROP TABLE IF EXISTS user_notification_preferences;
ALTER TABLE users DROP COLUMN IF EXISTS time_zone;
