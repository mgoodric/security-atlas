-- security-atlas — slice 498: shared local-inference (`internal/llm`)
-- foundation — the `ai_generations` append-only audit record + the reusable
-- `ai_assisted <-> human_approver` enforcement CHECK template.
--
-- ----------------------------------------------------------------------------
-- WHY (the gap today).
--
-- The AI-assist boundary (CLAUDE.md §"AI-assist boundary (hard)", canvas
-- §4.6.5) is documented as a SCHEMA-LEVEL guarantee, but on `main` the only
-- table that carries the columns + the CHECK is `mcp_write_proposals` (slice
-- 173, `mcp_wp_ai_assist_invariant`). There is no SHARED, reusable audit
-- record every AI-assist surface can write to, and no reusable enforcement
-- template the four ready v0 surfaces (440 / 441 / 444 / 471) can adopt
-- identically. This slice adds both:
--
--   1. `ai_generations` — one tenant-scoped, APPEND-ONLY row per LLM
--      generation across ALL AI-assist surfaces (the slice-182 audit
--      discipline made concrete + shared). Forensically reconstructable:
--      which model (name + version + provider), which prompt version, the
--      full system prompt, the full assembled context inputs, and the raw
--      draft that came back.
--
--   2. The `ai_assist_human_approver_guard(...)` reusable CHECK template
--      (a SQL function returning a boolean) so 440 / 441 / 471's eventual
--      approvable records adopt the EXACT same row-level invariant
--      `mcp_write_proposals` already enforces, with no per-table re-authoring
--      of the predicate. The Go-side guard helper (`internal/llm`) mirrors
--      it for early, friendly rejection before the DB round-trip.
--
-- ----------------------------------------------------------------------------
-- DESIGN CALLS (JUDGMENT slice — full rationale in
-- docs/audit-log/498-llm-foundation-decisions.md):
--
--   * Append-only by construction. `ai_generations` is a snapshot-at-
--     generation forensic record. SELECT + INSERT RLS policies ONLY, under
--     FORCE ROW LEVEL SECURITY; the deliberate ABSENCE of UPDATE/DELETE
--     policies makes the captured fields immutable (P0-498-5). Same pattern
--     as slice 013 evidence_audit_log, slice 030/055 decisions_audit, slice
--     036 artifact_access_log. atlas_app gets SELECT + INSERT only.
--
--   * CHECK template, not a trigger, for the enforcement (P0-498-4). The
--     constitution says "schema-level enforcement"; a CHECK calling a
--     same-row IMMUTABLE function satisfies that at the DB layer, references
--     only same-row columns (no sibling-row lookup), and is cheaper +
--     simpler to reason about than a trigger. Identical conclusion to slice
--     173 (`mcp_wp_ai_assist_invariant`). `ai_generations` itself is a DRAFT
--     ledger and carries no approval columns (it never holds an
--     audit-binding artifact), so the template is shipped as a REUSABLE
--     function for the approvable consumer records, not applied to this
--     table.
--
-- ----------------------------------------------------------------------------
-- Constitutional invariants honored:
--
--   AI-assist boundary (hard): the reusable CHECK template is the schema-
--     level `ai_assisted=true => (human_approved=true => human_approver
--     present)` enforcement the boundary documents; `ai_generations` is the
--     model name+version+provider+prompt audit record the boundary requires.
--     The substrate writes DRAFTS only — there is no self-approve column or
--     code path here.
--   #6  Tenant isolation at the DB layer — `ai_generations` is four-policy-
--     shaped (append-only => two policies) RLS-scoped on app.current_tenant
--     under FORCE; cross-tenant reads denied by the DB (P0-498-8, AC-12).
--   #2  Ingestion/evaluation separation — this table is the substrate's OWN
--     audit ledger; it never writes the evidence ledger.
--
-- Idempotency / reversibility (AC-5): paired
-- 20260607000000_ai_generations.down.sql drops the table (CASCADE removes
-- its policies + indexes) and the function for a byte-clean up->down->up
-- round-trip. No enum TYPE is created.
-- ----------------------------------------------------------------------------

-- ===== Reusable enforcement: ai_assist_human_approver_guard =====
--
-- The canonical AI-assist boundary predicate, factored into one IMMUTABLE
-- function so every approvable AI-assist record adopts the identical
-- invariant via a one-line CHECK:
--
--     CONSTRAINT <tbl>_ai_assist_invariant
--         CHECK (ai_assist_human_approver_guard(
--             ai_assisted, human_approved, human_approver))
--
-- Returns FALSE (constraint fails) ONLY for the forbidden row shape:
-- ai_assisted=TRUE AND human_approved=TRUE AND a missing-or-empty
-- human_approver. `length(human_approver) > 0` defends against a confused-
-- deputy supplying '' instead of NULL — same hardening as slice 173.
--
-- The predicate is byte-identical in meaning to slice 173's inlined
-- `mcp_wp_ai_assist_invariant`; that table keeps its own inlined CHECK
-- (no behavior change — AC-8), and new adopters use this function so the
-- predicate is authored exactly once going forward.
CREATE OR REPLACE FUNCTION ai_assist_human_approver_guard(
    p_ai_assisted     BOOLEAN,
    p_human_approved  BOOLEAN,
    p_human_approver  TEXT
) RETURNS BOOLEAN
    LANGUAGE sql
    IMMUTABLE
    PARALLEL SAFE
AS $$
    SELECT
        NOT (p_ai_assisted = TRUE AND p_human_approved = TRUE)
        OR (p_human_approver IS NOT NULL AND length(p_human_approver) > 0);
$$;

COMMENT ON FUNCTION ai_assist_human_approver_guard(BOOLEAN, BOOLEAN, TEXT) IS
    'Slice 498 reusable AI-assist boundary CHECK template. TRUE unless the '
    'forbidden shape (ai_assisted AND human_approved AND no human_approver) '
    'is present. Adopt via: CHECK (ai_assist_human_approver_guard('
    'ai_assisted, human_approved, human_approver)).';

-- ===== ai_generations =====
--
-- One row per LLM generation, across every AI-assist surface. Append-only:
-- the row is the immutable snapshot of WHAT was generated and HOW. Approval
-- state lives on the surface-specific consumer record (which adopts the
-- guard above), NOT here — this table is a draft ledger and intentionally
-- carries no human_approved / human_approver columns.
CREATE TABLE ai_generations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,

    -- Which AI-assist surface produced this generation. CHECK keeps the
    -- enumeration tight; a new surface requires an explicit migration.
    -- Mirrors the surface list in the slice narrative + canvas §4.6.5.
    surface         TEXT NOT NULL,

    -- Snapshot-at-generation model provenance (slice-182 schema contract:
    -- prompt_version / model_name / model_version / model_provider). These
    -- are the resolved values the client returned, NOT a config lookup, so
    -- a later config change never rewrites history.
    prompt_version  TEXT NOT NULL,
    model_name      TEXT NOT NULL,
    model_version   TEXT NOT NULL,
    model_provider  TEXT NOT NULL,

    -- The full forensic record (R-mitigation): the exact system prompt sent,
    -- the exact assembled context inputs (JSONB so structure survives), and
    -- the raw model draft. Bound as PARAMETERIZED values by the writer
    -- (P0-498-7) — model text is never interpolated into SQL.
    system_prompt   TEXT NOT NULL,
    context_inputs  JSONB NOT NULL DEFAULT '{}'::JSONB,
    raw_draft       TEXT NOT NULL,

    -- Surface-specific linkage: the answer / section / control / risk id the
    -- generation pertains to. Free-form TEXT (not an FK) so one shared table
    -- serves heterogeneous surfaces without a per-surface FK; empty string
    -- when a surface has no single subject. The (tenant_id, surface,
    -- surface_subject) index serves per-subject forensic lookup.
    surface_subject TEXT NOT NULL DEFAULT '',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Surface enum. The five surfaces named in the slice narrative + canvas
    -- §4.6.5. Adding a surface requires a follow-on migration extending this
    -- list (same discipline as mcp_wp_tool_name_check).
    CONSTRAINT ai_generations_surface_chk
        CHECK (surface IN (
            'questionnaire',
            'board_narrative',
            'gap_explanation',
            'checklist',
            'summary'
        )),

    -- Provenance completeness: the four model-metadata columns must be
    -- non-empty so the forensic record is always reconstructable (no row
    -- whose model is unknown). Defends the R-mitigation at the DB layer.
    CONSTRAINT ai_generations_provenance_nonempty
        CHECK (
            length(prompt_version) > 0
            AND length(model_name) > 0
            AND length(model_version) > 0
            AND length(model_provider) > 0
        )
);

-- Per-tenant recency feed (the surface's "recent generations" rail) and
-- per-subject forensic lookup.
CREATE INDEX idx_ai_generations_tenant_created
    ON ai_generations (tenant_id, created_at DESC);
CREATE INDEX idx_ai_generations_tenant_surface_subject
    ON ai_generations (tenant_id, surface, surface_subject);

-- ===== Row-Level Security =====
--
-- Append-only by construction: SELECT + INSERT policies only. The explicit
-- absence of UPDATE/DELETE policies under FORCE ROW LEVEL SECURITY makes
-- the captured fields immutable (P0-498-5). atlas_app gets SELECT + INSERT
-- only. Same pattern as slice 013 evidence_audit_log / slice 030
-- decisions_audit / slice 036 artifact_access_log.
ALTER TABLE ai_generations ENABLE ROW LEVEL SECURITY;
ALTER TABLE ai_generations FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON ai_generations
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON ai_generations
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT ON ai_generations TO atlas_app;
GRANT EXECUTE ON FUNCTION ai_assist_human_approver_guard(BOOLEAN, BOOLEAN, TEXT) TO atlas_app;
