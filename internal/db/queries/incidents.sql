-- OE-631 incident register queries.

-- name: CreateIncident :one
INSERT INTO incidents (
    id, tenant_id, title, description,
    operator_severity, severity, affected_system_tier,
    affected_systems, detected_by, detected_at
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7,
    $8, $9, $10
)
RETURNING *;

-- name: GetIncidentByID :one
SELECT *
FROM incidents
WHERE tenant_id = $1 AND id = $2;

-- name: ListIncidents :many
SELECT *
FROM incidents
WHERE tenant_id = $1
ORDER BY created_at DESC, id ASC;

-- name: TransitionIncident :one
UPDATE incidents
SET status = $3,
    triaged_by = CASE WHEN $3 = 'triaged' THEN $4 ELSE triaged_by END,
    triaged_at = CASE WHEN $3 = 'triaged' THEN now() ELSE triaged_at END,
    contained_by = CASE WHEN $3 = 'contained' THEN $4 ELSE contained_by END,
    contained_at = CASE WHEN $3 = 'contained' THEN now() ELSE contained_at END,
    resolved_by = CASE WHEN $3 = 'resolved' THEN $4 ELSE resolved_by END,
    resolved_at = CASE WHEN $3 = 'resolved' THEN now() ELSE resolved_at END,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = $5
RETURNING *;

-- name: CloseIncident :one
UPDATE incidents
SET status = 'closed',
    closed_by = $3,
    closed_at = now(),
    postmortem_summary = $4,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'resolved'
RETURNING *;

-- name: AddIncidentControlLink :one
INSERT INTO incident_controls (tenant_id, incident_id, control_id, linked_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, incident_id, control_id) DO NOTHING
RETURNING *;

-- name: AddIncidentRiskLink :one
INSERT INTO incident_risks (tenant_id, incident_id, risk_id, linked_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, incident_id, risk_id) DO NOTHING
RETURNING *;

-- name: AddIncidentVendorLink :one
INSERT INTO incident_vendors (tenant_id, incident_id, vendor_id, linked_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, incident_id, vendor_id) DO NOTHING
RETURNING *;

-- name: AddIncidentEvidenceLink :one
INSERT INTO incident_evidence_links (tenant_id, incident_id, evidence_id, linked_by)
VALUES ($1, $2, $3, $4)
ON CONFLICT (tenant_id, incident_id, evidence_id) DO NOTHING
RETURNING *;

-- name: ListIncidentControlLinks :many
SELECT *
FROM incident_controls
WHERE tenant_id = $1 AND incident_id = $2
ORDER BY linked_at ASC, control_id ASC;

-- name: ListIncidentRiskLinks :many
SELECT *
FROM incident_risks
WHERE tenant_id = $1 AND incident_id = $2
ORDER BY linked_at ASC, risk_id ASC;

-- name: ListIncidentVendorLinks :many
SELECT *
FROM incident_vendors
WHERE tenant_id = $1 AND incident_id = $2
ORDER BY linked_at ASC, vendor_id ASC;

-- name: ListIncidentEvidenceLinks :many
SELECT *
FROM incident_evidence_links
WHERE tenant_id = $1 AND incident_id = $2
ORDER BY linked_at ASC, evidence_id ASC;

-- name: WriteIncidentTimeline :one
INSERT INTO incident_timeline (
    id, tenant_id, incident_id,
    action, actor, from_state, to_state, summary, detail, subject_module
) VALUES (
    $1, $2, $3,
    $4, $5, $6, $7, $8, $9, 'core'
)
RETURNING *;

-- name: ListIncidentTimeline :many
SELECT *
FROM incident_timeline
WHERE tenant_id = $1 AND incident_id = $2
ORDER BY occurred_at ASC, id ASC;
