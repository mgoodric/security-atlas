-- security-atlas — slice 108: /v1/me/* profile + preferences + sessions schema.
--
-- Adds the storage backing for three new endpoint families:
--   GET  /v1/me                       caller profile (display_name, email, time_zone, idp_subject, role, owner_roles)
--   PATCH /v1/me                      mutate display_name + time_zone
--   GET  /v1/me/preferences           per-event-per-channel notification matrix
--   PATCH /v1/me/preferences          partial-merge upsert
--   GET  /v1/me/sessions              caller's currently-valid sessions
--   DELETE /v1/me/sessions/{id}       revoke a named session
--   DELETE /v1/me/sessions            revoke all OTHER sessions ("sign out other devices")
--
-- JUDGMENT decisions (see docs/audit-log/108-me-endpoints-decisions.md):
--   D1: Reuse the existing users table (display_name + email + idp_*) instead of creating a
--       sibling user_profiles table. Only time_zone is missing — add it as an additive column.
--       Deviation from slice file AC-2 which proposed user_profiles; rationale recorded.
--   D2: time_zone defaults to '' (empty = browser-derived), validated by Go time.LoadLocation
--       on PATCH. Backend never invents a timezone.
--   D3: Preferences = per-event-per-channel rows (NOT JSONB blob). Matches the
--       policy_acknowledgments / evidence_freshness per-row pattern. CHECK constraints
--       enumerate the allowed (event, channel) tuples so a typo at the API layer fails at
--       the DB layer.
--   D4: New me_audit_log table follows slice 062's append-only invariant (SELECT + INSERT
--       policies only; no UPDATE / DELETE). admin_audit_log_v view extended via CREATE OR
--       REPLACE to include the new branch.
--
-- Constitutional invariants honored:
--   #6  Tenant isolation via RLS on every new tenant-scoped table. Four-policy split on
--       user_notification_preferences (read/write/update/delete). Two-policy split on
--       me_audit_log (read + write only — append-only).
--   §4.6.5  Every PATCH and DELETE in the slice writes a me_audit_log entry (gated on
--           non-empty diff per anti-criterion ISC-A5; empty-diff PATCH skips the row).
--
-- Reversible via 20260516000003_me_endpoints.down.sql.

-- ===== users.time_zone (additive) =====
--
-- IANA timezone name (e.g. 'America/Los_Angeles') or '' (empty = "browser-derived").
-- Validated by Go time.LoadLocation on PATCH. The default empty string keeps the slice
-- 034 INSERT path unchanged.
ALTER TABLE users
    ADD COLUMN IF NOT EXISTS time_zone TEXT NOT NULL DEFAULT '';

-- ===== user_notification_preferences =====
--
-- One row per (tenant, user, event, channel). Default-on-missing-row policy at the
-- application layer: a fresh user with zero rows reads as all-enabled.
--
-- The CHECK constraints intentionally tightly couple the schema to the event taxonomy.
-- When a future slice adds a new notification event, that slice MUST also ship the
-- migration extending the CHECK constraint + the corresponding handler validation. This
-- prevents the case where a UI ships a toggle for an event the backend ignores.
CREATE TABLE user_notification_preferences (
    tenant_id   UUID NOT NULL,
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    event       TEXT NOT NULL,
    channel     TEXT NOT NULL,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT user_notification_preferences_pk
        PRIMARY KEY (tenant_id, user_id, event, channel),
    CONSTRAINT user_notification_preferences_event_check
        CHECK (event IN (
            'audit_period_assignment',
            'policy_ack_due',
            'risk_review_overdue',
            'control_drift'
        )),
    CONSTRAINT user_notification_preferences_channel_check
        CHECK (channel IN ('in_app', 'email'))
);

CREATE INDEX user_notification_preferences_user
    ON user_notification_preferences (tenant_id, user_id);

ALTER TABLE user_notification_preferences ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_notification_preferences FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON user_notification_preferences
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON user_notification_preferences
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON user_notification_preferences
    FOR UPDATE USING (current_tenant_matches(tenant_id))
               WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON user_notification_preferences
    FOR DELETE USING (current_tenant_matches(tenant_id));

-- ===== me_audit_log =====
--
-- Append-only audit ledger for /v1/me/* mutations. Mirrors the slice 062 per-domain
-- audit-log invariant: SELECT + INSERT policies only. No UPDATE or DELETE policy means
-- rows are immutable once written, even by the application role.
--
-- action enumerates the three mutation surfaces:
--   profile.update      PATCH /v1/me with non-empty diff
--   preferences.update  PATCH /v1/me/preferences
--   session.revoke      DELETE /v1/me/sessions/{id} OR DELETE /v1/me/sessions
--
-- before / after carry the per-action shape; the wire-layer is responsible for
-- redacting any field that should not be reproduced. The admin /v1/admin/audit-log
-- endpoint surfaces this via the admin_audit_log_v view extension below.
CREATE TABLE me_audit_log (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id    UUID NOT NULL,
    occurred_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_id      UUID NOT NULL,
    action       TEXT NOT NULL
                 CHECK (action IN ('profile.update', 'preferences.update', 'session.revoke')),
    before       JSONB NOT NULL DEFAULT '{}'::jsonb,
    after        JSONB NOT NULL DEFAULT '{}'::jsonb
);

CREATE INDEX me_audit_log_tenant_occurred
    ON me_audit_log (tenant_id, occurred_at DESC);
CREATE INDEX me_audit_log_user
    ON me_audit_log (tenant_id, user_id, occurred_at DESC);

ALTER TABLE me_audit_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE me_audit_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON me_audit_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON me_audit_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
-- Intentionally NO update/delete policies — append-only.

GRANT SELECT, INSERT, UPDATE, DELETE ON
    user_notification_preferences
TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    user_notification_preferences
TO atlas_migrate;
-- me_audit_log: writers + readers only; no UPDATE/DELETE grant either.
GRANT SELECT, INSERT ON me_audit_log TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON me_audit_log TO atlas_migrate;

-- ===== admin_audit_log_v branch extension =====
--
-- CREATE OR REPLACE preserves the existing seven branches verbatim (per slice 062
-- migration _022) and appends an eighth branch for me_audit_log. The branch projects
-- to the uniform (tenant_id, ts, source_table, event_type, actor, resource_type,
-- resource_id, summary) shape. resource_id = user_id (the /me/* surface acts on the
-- caller themselves).
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
    FROM audit_period_audit_log

    UNION ALL

    -- Slice 108 branch.
    SELECT
        tenant_id,
        occurred_at AS ts,
        'me_audit_log'::text AS source_table,
        action AS event_type,
        user_id::text AS actor,
        'user'::text AS resource_type,
        user_id::text AS resource_id,
        (to_jsonb(me_audit_log) - 'tenant_id' - 'occurred_at'
            - 'action' - 'user_id')::jsonb AS summary
    FROM me_audit_log;

GRANT SELECT ON admin_audit_log_v TO atlas_app;
