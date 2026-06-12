-- migrations/sql/20260612010000_role_scoped_checklist.sql
--
-- Slice 471 — Role-scoped control-implementation checklist generator v0 (cited,
-- non-binding). The deterministic role-split (owner_role + applicability_expr ->
-- {infra, engineering, security, unassigned}) decides WHICH control belongs to
-- WHICH role; the local-Ollama task-breakdown turns each in-scope control's text
-- into 1..N cited, role-appropriate task statements. The generated checklist is
-- a DRAFT the operator approves one section (one role) at a time.
--
-- ----------------------------------------------------------------------------
-- WHY.
--
-- This is the 5th AI-assist v0 surface, alongside slice 440 (board narrative),
-- 441 (questionnaire answers), 444 (gap explanation). It is governed by the
-- CLAUDE.md "AI-assist boundary (hard)". The schema-level leg of that boundary
-- is the slice-498 shared `ai_assist_human_approver_guard(...)` CHECK and the
-- snapshot-at-generation provenance columns; this migration ADOPTS the shared
-- guard (it does NOT re-author the predicate — P0-498-4 discipline), exactly as
-- slice 441's questionnaire_answers migration does.
--
-- ----------------------------------------------------------------------------
-- SHAPE (JUDGMENT slice — full rationale in
-- docs/audit-log/471-role-scoped-checklist-decisions.md):
--
--   Two new tenant-scoped tables:
--
--   * checklist_sections — one row per (generation, role). This is the
--     APPROVABLE unit (per-section approval, AC-10): it carries the AI-assist
--     boundary column set (ai_assisted / human_approved / human_approver +
--     prompt_version / model_name / model_version / model_provider) and ADOPTS
--     the shared guard. A section is one role's slice of one generation run; the
--     operator approves / edits / rejects it independently. The `unassigned`
--     bucket (controls matching no role, AC-1) is itself a section so it is
--     surfaced honestly, never silently dropped — but it is NON-approvable
--     scaffolding (it carries no AI tasks; see the items table).
--
--   * checklist_items — the individual cited task statements belonging to a
--     section. Each item carries its task text + a mandatory citation
--     (control / scf-anchor / policy id, JSONB so the shape can evolve, mirrors
--     questionnaire_answers.citations) + a `no_evidence` flag (AC-6: a control
--     with no evidence backing is rendered as an explicit gap item, never as
--     satisfied — the no-fabricated-coverage guardrail). Items are immutable
--     once written (the section is the mutable approval unit); regeneration
--     replaces the whole generation.
--
--   Why a section carries the provenance and the guard, not each item: the
--   approval granularity is per-role (slice-182 D2 "per-section"), and the model
--   provenance is one generation run shared across a section's items. Putting
--   the boundary columns on the section keeps one approval row per role and
--   avoids per-item provenance duplication while still enforcing the invariant
--   at the granularity the operator acts on.
--
--   The full forensic record (system prompt + assembled context + raw draft)
--   lives in the slice-498 `ai_generations` ledger (surface='checklist',
--   surface_subject=<section id>), written by the service. These tables carry
--   the CURRENT checklist state + the approval columns; the immutable generation
--   history is the ledger's job (R-mitigation, append-only).
--
-- ----------------------------------------------------------------------------
-- Constitutional invariants honored:
--
--   AI-assist boundary (hard): the shared CHECK is the schema-level
--     ai_assisted <-> human_approver enforcement; human_approved=TRUE without
--     human_approver is impossible at the DB layer (P0-471-6, AC-7). No
--     auto-approve column or path — approval is a separate operator UPDATE
--     recording the approver. The model NEVER fabricates coverage: the
--     no_evidence flag + the citation-resolution gate (in the service) are the
--     two legs of that guarantee.
--   #6  Tenant isolation: both tables get the canonical four-policy FORCE RLS
--     (deny on missing context). A generation under tenant A can never read or
--     write tenant B's rows.
--   #9  Manual evidence is first-class: the no_evidence flag treats a control's
--     manual and automated evidence uniformly when deciding "no evidence yet".
--
-- Idempotency / reversibility: paired
-- 20260612010000_role_scoped_checklist.down.sql drops both tables (children
-- first) for a clean up->down->up round-trip. No TYPE created.
-- ----------------------------------------------------------------------------

-- ===== checklist_sections — one approvable row per (generation, role) =====
CREATE TABLE checklist_sections (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,

    -- The generation run this section belongs to. One generation produces one
    -- section per role (incl. the unassigned bucket). Free-form UUID grouping
    -- key, set by the service per run (not an FK — a generation is not a row of
    -- its own in v0; the sections ARE the generation).
    generation_id   UUID NOT NULL,

    -- The fixed v0 role this section is for: infra | engineering | security |
    -- unassigned. The role-split that produced it is DETERMINISTIC (owner_role +
    -- applicability_expr normalization, internal/checklist) — never LLM-guessed.
    role            TEXT NOT NULL,

    -- AI-assist boundary columns. A real role-section (infra/engineering/
    -- security) carrying model-authored task text is ai_assisted=TRUE. The
    -- unassigned bucket carries no AI text, so it is ai_assisted=FALSE and is
    -- never approvable (the guard permits any approval state for FALSE).
    ai_assisted     BOOLEAN NOT NULL DEFAULT FALSE,

    -- One-click per-section approval (AC-10). FALSE until an operator approves.
    human_approved  BOOLEAN NOT NULL DEFAULT FALSE,

    -- The operator id that approved the section. NULL until approved. The shared
    -- guard forbids ai_assisted=TRUE + human_approved=TRUE with this NULL/blank
    -- (P0-471-6).
    human_approver  TEXT NULL,

    -- Snapshot-at-generation model provenance (slice-182 schema contract,
    -- mirrors ai_generations + questionnaire_answers). Empty for the unassigned
    -- bucket (no model ran for it); populated for an AI-authored section.
    prompt_version  TEXT NOT NULL DEFAULT '',
    model_name      TEXT NOT NULL DEFAULT '',
    model_version   TEXT NOT NULL DEFAULT '',
    model_provider  TEXT NOT NULL DEFAULT '',

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- One section per (generation, role).
    UNIQUE (tenant_id, generation_id, role),

    -- Composite uniqueness supports the tenant-safe composite FK target from
    -- checklist_items (tenant_id, section_id) -> (tenant_id, id) — the slice-002
    -- cross-tenant-safe FK pattern.
    UNIQUE (tenant_id, id),

    -- Role enum kept tight (the FIXED v0 taxonomy — P0-471-5 no configurable
    -- taxonomy). A new role requires an explicit migration.
    CONSTRAINT checklist_sections_role_chk
        CHECK (role IN ('infra', 'engineering', 'security', 'unassigned')),

    -- The AI-assist boundary CHECK, ADOPTED from the slice-498 reusable function
    -- (NOT re-authored). Fails ONLY for the forbidden shape: ai_assisted=TRUE
    -- AND human_approved=TRUE AND a missing-or-empty human_approver. The
    -- schema-level mirror of internal/llm's EnforceApproval (belt-and-suspenders:
    -- Go rejects early, the DB is authoritative).
    CONSTRAINT checklist_sections_ai_assist_invariant
        CHECK (ai_assist_human_approver_guard(
            ai_assisted, human_approved, human_approver)),

    -- Provenance completeness, conditional on ai_assisted. An AI-authored
    -- section must carry a reconstructable model (R-mitigation); the unassigned
    -- bucket (ai_assisted=FALSE) is exempt because no model ran.
    CONSTRAINT checklist_sections_ai_provenance_nonempty
        CHECK (
            NOT ai_assisted
            OR (
                length(prompt_version) > 0
                AND length(model_name) > 0
                AND length(model_version) > 0
                AND length(model_provider) > 0
            )
        )
);

-- Per-tenant generation lookup (load a generation's sections) + recency feed.
CREATE INDEX idx_checklist_sections_tenant_generation
    ON checklist_sections (tenant_id, generation_id);
CREATE INDEX idx_checklist_sections_tenant_created
    ON checklist_sections (tenant_id, created_at DESC);

ALTER TABLE checklist_sections ENABLE ROW LEVEL SECURITY;
ALTER TABLE checklist_sections FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON checklist_sections
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON checklist_sections
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON checklist_sections
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON checklist_sections
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON checklist_sections TO atlas_app;

-- ===== checklist_items — the cited task statements in a section =====
CREATE TABLE checklist_items (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id       UUID NOT NULL,

    -- The approvable section this item belongs to. Composite FK keeps the
    -- linkage tenant-safe (slice-002 pattern) and CASCADE so regeneration of a
    -- section's items is clean.
    section_id      UUID NOT NULL,

    -- The in-scope control this task derives from. Composite FK to controls
    -- proves the item is grounded in a real tenant-owned control before any
    -- task text is written.
    control_id      UUID NOT NULL,

    -- The model-authored task statement (opaque text — never executed; bound as
    -- a parameter, never interpolated). One control yields 1..N items.
    task_text       TEXT NOT NULL,

    -- The mandatory citation set (control / scf-anchor / policy ids), JSONB so
    -- the shape can evolve. Every item MUST cite at least its control; the
    -- service validates every cited id resolves to a tenant-owned row BEFORE the
    -- operator sees the draft (P0-471-2). Empty array is never persisted.
    citations       JSONB NOT NULL DEFAULT '[]'::JSONB,

    -- TRUE when the source control has NO evidence backing — the item is an
    -- explicit gap ("no evidence yet"), never rendered as satisfied (AC-6,
    -- P0-471-4, the no-fabricated-coverage guardrail).
    no_evidence     BOOLEAN NOT NULL DEFAULT FALSE,

    -- Ordering within the section for stable rendering.
    sort_order      INTEGER NOT NULL DEFAULT 0,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),

    FOREIGN KEY (tenant_id, section_id)
        REFERENCES checklist_sections (tenant_id, id) ON DELETE CASCADE,
    FOREIGN KEY (tenant_id, control_id)
        REFERENCES controls (tenant_id, id) ON DELETE CASCADE,

    -- Citations are mandatory: a non-empty JSONB array per item (P0-471-2). The
    -- service guarantees resolution; this CHECK guarantees presence.
    CONSTRAINT checklist_items_citations_nonempty
        CHECK (jsonb_typeof(citations) = 'array' AND jsonb_array_length(citations) > 0)
);

CREATE INDEX idx_checklist_items_tenant_section
    ON checklist_items (tenant_id, section_id, sort_order);

ALTER TABLE checklist_items ENABLE ROW LEVEL SECURITY;
ALTER TABLE checklist_items FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON checklist_items
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON checklist_items
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON checklist_items
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON checklist_items
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON checklist_items TO atlas_app;
