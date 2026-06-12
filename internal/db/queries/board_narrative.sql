-- Slice 440 — board-narrative AI v0 per-section record queries. Every query is
-- tenant-scoped (tenant_id = $1) and runs under the caller's RLS transaction
-- (app.current_tenant) so cross-tenant rows are invisible (invariant #6).

-- UpsertBoardNarrativeDraft persists a validated, UNAPPROVED draft section
-- (ai_assisted=TRUE, human_approved=FALSE, human_approver=NULL). Regeneration
-- for the same (tenant, period_end, section_key) replaces the prior unapproved
-- draft (the immutable history is the ai_generations ledger). The draft text +
-- model provenance are bound as parameters (P0-498-7 — model output is never
-- interpolated into SQL).
-- name: UpsertBoardNarrativeDraft :one
INSERT INTO board_narrative_sections (
    id, tenant_id, section_key, period_end, raw_draft, citations,
    authored_by, ai_assisted, human_approved, human_approver,
    prompt_version, model_name, model_version, model_provider
) VALUES (
    gen_random_uuid(), $1, $2, $3, $4, $5,
    $6, TRUE, FALSE, NULL,
    $7, $8, $9, $10
)
ON CONFLICT (tenant_id, period_end, section_key) DO UPDATE SET
    raw_draft      = EXCLUDED.raw_draft,
    citations      = EXCLUDED.citations,
    operator_edit  = '',
    final_text     = '',
    authored_by    = EXCLUDED.authored_by,
    ai_assisted    = TRUE,
    human_approved = FALSE,
    human_approver = NULL,
    prompt_version = EXCLUDED.prompt_version,
    model_name     = EXCLUDED.model_name,
    model_version  = EXCLUDED.model_version,
    model_provider = EXCLUDED.model_provider,
    updated_at     = now()
RETURNING *;

-- ApproveBoardNarrativeSection records the operator's edited final text +
-- approver and flips human_approved=TRUE (one-click per section). The DB CHECK
-- makes human_approved=TRUE with a blank approver impossible (P0-440-2); the
-- service rejects a blank approver before this call. Scoped to the tenant +
-- the AI-assisted draft; an absent/cross-tenant id returns no row.
-- name: ApproveBoardNarrativeSection :one
UPDATE board_narrative_sections
SET operator_edit  = $3,
    final_text     = $3,
    human_approved = TRUE,
    human_approver = $4,
    updated_at     = now()
WHERE tenant_id = $1
  AND id = $2
  AND ai_assisted = TRUE
RETURNING *;

-- GetBoardNarrativeSectionByID returns one section by id under the caller's
-- tenant (used by the approval flow + tests).
-- name: GetBoardNarrativeSectionByID :one
SELECT * FROM board_narrative_sections
WHERE tenant_id = $1 AND id = $2;
