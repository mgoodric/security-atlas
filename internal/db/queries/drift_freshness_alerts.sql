-- OE-599 drift/freshness push-alert producer queries.

-- name: GetDriftFreshnessAlertConfig :one
SELECT *
FROM drift_freshness_alert_config
WHERE tenant_id = $1;

-- name: UpsertDriftFreshnessAlertConfig :one
INSERT INTO drift_freshness_alert_config (
    tenant_id, enabled, slack_enabled, pagerduty_enabled,
    control_drift_enabled, evidence_staleness_enabled,
    min_drifted_controls, min_stale_age, debounce_interval, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, now())
ON CONFLICT (tenant_id) DO UPDATE SET
    enabled = EXCLUDED.enabled,
    slack_enabled = EXCLUDED.slack_enabled,
    pagerduty_enabled = EXCLUDED.pagerduty_enabled,
    control_drift_enabled = EXCLUDED.control_drift_enabled,
    evidence_staleness_enabled = EXCLUDED.evidence_staleness_enabled,
    min_drifted_controls = EXCLUDED.min_drifted_controls,
    min_stale_age = EXCLUDED.min_stale_age,
    debounce_interval = EXCLUDED.debounce_interval,
    updated_at = now()
RETURNING *;

-- name: ClaimDriftFreshnessAlert :one
INSERT INTO drift_freshness_alert_log (
    id, tenant_id, recipient_user_id, event_type, control_id, state_key, notification_id
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (tenant_id, recipient_user_id, event_type, control_id, state_key) DO NOTHING
RETURNING id;

-- name: CountDriftFreshnessAlertClaims :one
SELECT COUNT(*) AS claim_count
FROM drift_freshness_alert_log
WHERE tenant_id = $1
  AND recipient_user_id = $2;
