-- security-atlas -- OE-599 drift/freshness push-alert producer.
--
-- This slice does not add a delivery transport. It adds the tenant-scoped
-- producer state that lets the existing notifications spine feed the existing
-- Slack and webhook/PagerDuty delivery scheduler.

CREATE TABLE drift_freshness_alert_config (
    tenant_id                 UUID PRIMARY KEY,
    enabled                   BOOLEAN NOT NULL DEFAULT false,
    slack_enabled             BOOLEAN NOT NULL DEFAULT false,
    pagerduty_enabled         BOOLEAN NOT NULL DEFAULT false,
    control_drift_enabled     BOOLEAN NOT NULL DEFAULT true,
    evidence_staleness_enabled BOOLEAN NOT NULL DEFAULT true,
    min_drifted_controls      INTEGER NOT NULL DEFAULT 1,
    min_stale_age             INTERVAL NOT NULL DEFAULT '0 seconds',
    debounce_interval         INTERVAL NOT NULL DEFAULT '15 minutes',
    updated_at                TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT drift_freshness_alert_config_min_drift_nonnegative
        CHECK (min_drifted_controls >= 1),
    CONSTRAINT drift_freshness_alert_config_min_stale_nonnegative
        CHECK (min_stale_age >= interval '0 seconds'),
    CONSTRAINT drift_freshness_alert_config_debounce_positive
        CHECK (debounce_interval > interval '0 seconds')
);

ALTER TABLE drift_freshness_alert_config ENABLE ROW LEVEL SECURITY;
ALTER TABLE drift_freshness_alert_config FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON drift_freshness_alert_config
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON drift_freshness_alert_config
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON drift_freshness_alert_config
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON drift_freshness_alert_config
    FOR DELETE USING (current_tenant_matches(tenant_id));

CREATE TABLE drift_freshness_alert_log (
    id                 UUID PRIMARY KEY,
    tenant_id          UUID NOT NULL,
    recipient_user_id  TEXT NOT NULL,
    event_type         TEXT NOT NULL,
    control_id         UUID NOT NULL,
    state_key          TEXT NOT NULL,
    notification_id    UUID NULL,
    delivered_at       TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT drift_freshness_alert_log_recipient_nonempty
        CHECK (length(recipient_user_id) > 0),
    CONSTRAINT drift_freshness_alert_log_event_type_check
        CHECK (event_type IN ('control.drift', 'evidence.staleness')),
    CONSTRAINT drift_freshness_alert_log_state_key_nonempty
        CHECK (length(state_key) > 0),
    CONSTRAINT drift_freshness_alert_log_dedup_unique
        UNIQUE (tenant_id, recipient_user_id, event_type, control_id, state_key)
);

CREATE INDEX idx_drift_freshness_alert_log_recent
    ON drift_freshness_alert_log (tenant_id, recipient_user_id, delivered_at DESC);

ALTER TABLE drift_freshness_alert_log ENABLE ROW LEVEL SECURITY;
ALTER TABLE drift_freshness_alert_log FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON drift_freshness_alert_log
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON drift_freshness_alert_log
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON drift_freshness_alert_log
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON drift_freshness_alert_log
    FOR DELETE USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON
    drift_freshness_alert_config,
    drift_freshness_alert_log
TO atlas_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON
    drift_freshness_alert_config,
    drift_freshness_alert_log
TO atlas_migrate;
