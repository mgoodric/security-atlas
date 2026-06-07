-- ai_generations — slice 498 shared AI-assist audit ledger.
--
-- One row per LLM generation across every AI-assist surface. The table is
-- APPEND-ONLY by construction (SELECT + INSERT RLS policies only, no
-- UPDATE/DELETE policy under FORCE; atlas_app has no UPDATE/DELETE grant),
-- so there is deliberately no UpdateAIGeneration / DeleteAIGeneration query
-- -- the captured fields are immutable snapshots (P0-498-5).
--
-- All queries are tenant-scoped via the leading tenant_id predicate; RLS
-- under FORCE keeps the cross-tenant boundary safe even on a misconfigured
-- query. Model output (system_prompt / context_inputs / raw_draft) is bound
-- as PARAMETERIZED values only -- never interpolated (P0-498-7).

-- name: WriteAIGeneration :one
-- Append one generation record. The writer (internal/llm.AuditWriter) binds
-- every value, including the raw model draft, as a parameter -- the model
-- output is treated as opaque data, never SQL.
INSERT INTO ai_generations (
    tenant_id,
    surface,
    prompt_version,
    model_name,
    model_version,
    model_provider,
    system_prompt,
    context_inputs,
    raw_draft,
    surface_subject
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING *;

-- name: GetAIGeneration :one
-- Fetch a single generation by id within the current tenant. Used by the
-- smoke consumer to prove the round-trip + by future forensic lookups.
SELECT *
FROM ai_generations
WHERE tenant_id = $1 AND id = $2;

-- name: ListAIGenerationsBySubject :many
-- All generations for one surface subject, newest first. Powers the
-- per-subject "recent AI drafts" rail. Served by
-- idx_ai_generations_tenant_surface_subject.
SELECT *
FROM ai_generations
WHERE tenant_id = $1 AND surface = $2 AND surface_subject = $3
ORDER BY created_at DESC, id DESC;

-- name: CountAIGenerationsForTenant :one
-- Count of all generations for the current tenant. Used by the cross-tenant
-- isolation integration test to prove tenant B sees zero of tenant A's rows.
SELECT count(*) AS generation_count
FROM ai_generations
WHERE tenant_id = $1;
