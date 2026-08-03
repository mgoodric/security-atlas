-- Personnel-security onboarding/offboarding workflow queries.

-- name: InsertPersonnelChecklist :one
INSERT INTO personnel_security_checklists (
    id, tenant_id, workflow_kind, source, source_event_id,
    person_external_id, person_work_email, person_display_name,
    control_id, due_at, created_by
) VALUES (
    $1, $2, $3, $4, $5,
    $6, $7, $8,
    $9, $10, $11
)
RETURNING *;

-- name: GetPersonnelChecklistBySourceEvent :one
SELECT *
FROM personnel_security_checklists
WHERE tenant_id = $1
  AND source = $2
  AND source_event_id = $3;

-- name: InsertPersonnelChecklistItem :one
INSERT INTO personnel_security_checklist_items (
    id, tenant_id, checklist_id, slug, title, category, sort_order
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetPersonnelChecklist :one
SELECT *
FROM personnel_security_checklists
WHERE tenant_id = $1 AND id = $2;

-- name: ListPersonnelChecklistItems :many
SELECT *
FROM personnel_security_checklist_items
WHERE tenant_id = $1 AND checklist_id = $2
ORDER BY sort_order ASC, id ASC;

-- name: GetPersonnelChecklistItem :one
SELECT *
FROM personnel_security_checklist_items
WHERE tenant_id = $1 AND id = $2;

-- name: CompletePersonnelChecklistItem :one
UPDATE personnel_security_checklist_items
SET completed_at = $3,
    completed_by = $4,
    evidence_record_id = $5,
    evidence_uri = $6,
    notes = $7,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND completed_at IS NULL
RETURNING *;

-- name: CountOpenPersonnelChecklistItems :one
SELECT count(*)
FROM personnel_security_checklist_items
WHERE tenant_id = $1
  AND checklist_id = $2
  AND completed_at IS NULL;

-- name: MarkPersonnelChecklistCompleted :one
UPDATE personnel_security_checklists
SET status = 'completed',
    completed_at = $3,
    updated_at = now()
WHERE tenant_id = $1
  AND id = $2
  AND status = 'open'
RETURNING *;

-- name: ListOverdueOffboardingChecklists :many
SELECT *
FROM personnel_security_checklists
WHERE tenant_id = $1
  AND workflow_kind = 'offboarding'
  AND status = 'open'
  AND due_at < $2
ORDER BY due_at ASC, id ASC;

-- name: CountPersonnelChecklistsForTenant :one
SELECT count(*)
FROM personnel_security_checklists
WHERE tenant_id = $1;
