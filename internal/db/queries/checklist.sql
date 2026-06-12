-- Slice 471 — role-scoped control-implementation checklist generator v0.
--
-- Queries for the deterministic role-split + the cited, non-binding checklist
-- draft. Every query is tenant-bound via the leading $1 parameter
-- (defense-in-depth behind the four-policy FORCE RLS on checklist_sections /
-- checklist_items, invariant #6). Model output (task_text, citations) is bound
-- as PARAMETERIZED values only — never interpolated (P0-498-7).

-- name: ListInScopeControlsForChecklist :many
-- AC-1/AC-2: the in-scope control set for a generation. Every ACTIVE
-- (non-superseded) control for the caller's tenant, with the deterministic
-- role-split inputs (owner_role + applicability_expr), its scf linkage (scf_id
-- for the SCF-anchor citation), and a `has_evidence` flag (AC-6: a control with
-- zero evidence rows is rendered as a "no evidence yet" gap, never as
-- satisfied). RLS scopes the read to the tenant; the WHERE tenant_id clause is
-- belt-and-suspenders. Ordered for stable rendering.
SELECT
    c.id,
    c.title,
    c.description,
    c.owner_role,
    c.applicability_expr,
    c.scf_id,
    c.control_family,
    EXISTS (
        SELECT 1 FROM evidence_records e
        WHERE e.tenant_id = c.tenant_id AND e.control_id = c.id
    ) AS has_evidence
FROM controls c
WHERE c.tenant_id = $1
  AND c.superseded_by IS NULL
ORDER BY c.control_family ASC, c.title ASC, c.id ASC;

-- name: ResolveChecklistControl :one
-- Citation-resolution gate (AC-5, P0-471-2): does this id name a tenant-owned
-- ACTIVE control? A cross-tenant id is RLS-invisible, so this returns no row
-- (the AC-8 mechanism). Returns the control id when it resolves.
SELECT c.id
FROM controls c
WHERE c.tenant_id = $1 AND c.id = $2 AND c.superseded_by IS NULL;

-- name: ResolveChecklistPolicy :one
-- Citation-resolution gate: does this id name a tenant-owned policy? Used when
-- a generated item cites a linked policy id. Cross-tenant ids are RLS-invisible.
SELECT p.id
FROM policies p
WHERE p.tenant_id = $1 AND p.id = $2;

-- name: ListPolicyIDsLinkedToControl :many
-- The policy ids linked to one control via slice-022's
-- policies.linked_control_ids UUID[] array (same predicate as slice-064's
-- ListPoliciesLinkedToControl). Lets the generator offer a linked-policy id as
-- a citable reference for a control's tasks. Tenant-scoped.
SELECT p.id
FROM policies p
WHERE p.tenant_id = $1
  AND p.linked_control_ids @> ARRAY[sqlc.arg(control_id)::uuid]
ORDER BY p.id ASC;

-- name: InsertChecklistSection :one
-- Persist one approvable role-section of a generation (ai_assisted, UNAPPROVED).
-- A real role-section is ai_assisted=TRUE with full model provenance; the
-- unassigned bucket is ai_assisted=FALSE with empty provenance. The shared
-- ai_assist_human_approver_guard CHECK forbids the approved-without-approver
-- shape at INSERT time too.
INSERT INTO checklist_sections (
    tenant_id,
    generation_id,
    role,
    ai_assisted,
    prompt_version,
    model_name,
    model_version,
    model_provider
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;

-- name: InsertChecklistItem :one
-- Persist one cited task statement in a section. citations is the validated,
-- tenant-resolved JSONB array (the service guarantees every cited id resolves
-- BEFORE this write — P0-471-2); the CHECK guarantees the array is non-empty.
-- no_evidence marks a control with no evidence backing as an explicit gap
-- (AC-6). task_text is bound as a parameter (model output never interpolated).
INSERT INTO checklist_items (
    tenant_id,
    section_id,
    control_id,
    task_text,
    citations,
    no_evidence,
    sort_order
)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListChecklistSectionsByGeneration :many
-- Load all sections of one generation for the caller's tenant, role order
-- stable. Powers the role-grouped review view (AC-9).
SELECT *
FROM checklist_sections
WHERE tenant_id = $1 AND generation_id = $2
ORDER BY
    CASE role
        WHEN 'infra' THEN 0
        WHEN 'engineering' THEN 1
        WHEN 'security' THEN 2
        WHEN 'unassigned' THEN 3
        ELSE 4
    END,
    id ASC;

-- name: ListChecklistItemsBySection :many
-- The cited task items in one section, render order. Tenant-scoped.
SELECT *
FROM checklist_items
WHERE tenant_id = $1 AND section_id = $2
ORDER BY sort_order ASC, id ASC;

-- name: GetChecklistSectionByID :one
-- Fetch one section by id within the caller's tenant. Used by the approval flow
-- + tests. A cross-tenant id returns ErrNoRows.
SELECT *
FROM checklist_sections
WHERE tenant_id = $1 AND id = $2;

-- name: ApproveChecklistSection :one
-- One-click per-section approval (AC-10): flip human_approved=TRUE + record the
-- approver on an AI-assisted, currently-unapproved section. The
-- ai_assisted=TRUE AND human_approver IS NOT NULL guard in the WHERE means an
-- attempt to "approve" the unassigned bucket (ai_assisted=FALSE) or supply a
-- blank approver matches no row (ErrNoRows) — there is NO auto-approve path.
-- The DB CHECK is the authoritative backstop. updated_at is refreshed.
UPDATE checklist_sections
SET human_approved = TRUE,
    human_approver = sqlc.arg(human_approver),
    updated_at = now()
WHERE tenant_id = $1
  AND id = sqlc.arg(id)
  AND ai_assisted = TRUE
  AND human_approved = FALSE
RETURNING *;

-- name: CountChecklistSectionsForTenant :one
-- Count of all checklist sections visible to the caller's tenant. Used by the
-- cross-tenant isolation integration test to prove tenant B sees zero of
-- tenant A's rows (AC-8).
SELECT count(*) AS section_count
FROM checklist_sections
WHERE tenant_id = $1;
