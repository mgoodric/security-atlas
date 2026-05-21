-- security-atlas — slice 173: MCP write tools + HITL approval.
--
-- Adds the `mcp_write_proposals` table that captures every AI-driven write
-- proposed via the MCP server. The lifecycle is Pattern A (draft-then-confirm)
-- locked by the maintainer 2026-05-20:
--
--   1. Write tool handler INSERTs a proposal with state='ai_proposed'.
--   2. Operator (or operator-driven MCP confirm_write tool) flips the row to
--      state='applied'; the platform applies the change through its canonical
--      write paths.
--   3. Operator may instead reject the proposal; state='rejected' is terminal.
--
-- Schema-level AI-assist boundary invariant (constitutional, CLAUDE.md §"AI-
-- assist boundary (hard)"):
--
--   (ai_assisted=true AND human_approved=true) -> human_approver IS NOT NULL.
--
-- Enforced via the `mcp_wp_ai_assist_invariant` CHECK constraint. CHECK is
-- preferred to a trigger because the constraint only references columns on
-- the same row; no sibling-row lookup is required and the table has a simple
-- single-axis state machine (ai_proposed -> applied | rejected).
--
-- Tenancy: every proposal is tenant-scoped via tenant_id NOT NULL + the
-- canonical four-policy RLS split (slice 014 pattern). Cross-tenant proposal
-- access is denied at the database layer.
--
-- Idempotency / reversibility: CREATE TABLE IF NOT EXISTS is NOT used here
-- because slices commit a fresh schema and the down-migration handles the
-- reverse. Per-policy and per-index DROPs in the .down.sql restore the
-- pre-slice state.
--
-- Constitutional invariants honored:
--
--   #6 Tenant isolation enforced at the DB layer via PostgreSQL RLS.
--   AI-assist boundary (hard): the row-level CHECK is the schema-level
--   enforcement the boundary documents.

CREATE TABLE mcp_write_proposals (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id           UUID NOT NULL,

    -- Which MCP write tool produced this proposal. The CHECK keeps the
    -- enumeration tight; new tools require an explicit migration.
    tool_name           TEXT NOT NULL,

    -- The full tool input as supplied by the MCP caller. Bounded by the
    -- per-tool input JSON schema (validated at the tool handler layer).
    -- Stored as JSONB so a future apply-step can refer to it forensically
    -- without losing structure.
    tool_input          JSONB NOT NULL,

    -- Lifecycle. 'ai_proposed' is the initial state; either 'applied' or
    -- 'rejected' is terminal. No edit-then-confirm in v1.
    state               TEXT NOT NULL DEFAULT 'ai_proposed',

    -- AI-assist provenance. ai_assisted defaults TRUE because every write
    -- arriving via this table originated from the MCP server (constitutional
    -- by design); the column is explicit so the schema-level invariant has
    -- the column to reference.
    ai_assisted         BOOLEAN NOT NULL DEFAULT TRUE,
    ai_model_name       TEXT NOT NULL,
    ai_model_version    TEXT NOT NULL,

    -- HITL approval fields. human_approved defaults FALSE; the confirm path
    -- flips both human_approved=TRUE and human_approver=<user_id>. Stored as
    -- TEXT so v1's credstore.Credential.ID (token-shaped string like
    -- "key_...") fits without coercion; slice 034's UserID will populate this
    -- with a real OIDC-issued user id once the IdP lands. Schema-level
    -- enforcement is at the row-level CHECK below, not via FK.
    human_approved      BOOLEAN NOT NULL DEFAULT FALSE,
    human_approver      TEXT NULL,

    -- Terminal-state timestamps + free-text reject reason.
    applied_at          TIMESTAMPTZ NULL,
    applied_subject     TEXT NULL,  -- id of the canonical row created/updated on apply
    rejected_at         TIMESTAMPTZ NULL,
    reject_reason       TEXT NULL,

    -- Operator credential that the MCP server authenticated as when the
    -- proposal was created. Stored as TEXT to accept v1's credstore key id
    -- ("key_..."); slice 034's OIDC user id will land here once the IdP
    -- lands. The meta-audit trail attributes the proposal to the human who
    -- ran the LLM session.
    created_by          TEXT NOT NULL,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Tool name enum: the four documented write tools. Adding a new tool
    -- requires a follow-on migration that extends this list. P0-A2 forbids
    -- admin-tier writes here.
    CONSTRAINT mcp_wp_tool_name_check
        CHECK (tool_name IN (
            'create_risk',
            'update_control_state',
            'push_evidence',
            'update_risk_treatment'
        )),

    -- State machine. ai_proposed is initial; applied and rejected are
    -- terminal. No `edited` state — operators reject and the LLM re-files.
    CONSTRAINT mcp_wp_state_check
        CHECK (state IN ('ai_proposed', 'applied', 'rejected')),

    -- Constitutional invariant (CLAUDE.md §"AI-assist boundary (hard)"):
    -- ai_assisted=true AND human_approved=true REQUIRES human_approver
    -- present AND non-empty. The bracket of conditions is "any row that
    -- is BOTH AI-assisted AND marked human-approved MUST attribute the
    -- approval to a human user". `length(human_approver) > 0` defends
    -- against a confused-deputy that supplies an empty string instead
    -- of NULL.
    CONSTRAINT mcp_wp_ai_assist_invariant
        CHECK (
            NOT (ai_assisted = TRUE AND human_approved = TRUE)
            OR (human_approver IS NOT NULL AND length(human_approver) > 0)
        ),

    -- Terminal-state consistency: if state='applied' then human_approved=TRUE
    -- AND applied_at IS NOT NULL. If state='rejected' then rejected_at IS NOT
    -- NULL. ai_proposed forbids both terminal timestamps.
    CONSTRAINT mcp_wp_applied_consistency
        CHECK (
            state <> 'applied'
            OR (human_approved = TRUE AND applied_at IS NOT NULL)
        ),
    CONSTRAINT mcp_wp_rejected_consistency
        CHECK (state <> 'rejected' OR rejected_at IS NOT NULL),
    CONSTRAINT mcp_wp_ai_proposed_consistency
        CHECK (
            state <> 'ai_proposed'
            OR (applied_at IS NULL AND rejected_at IS NULL AND human_approved = FALSE)
        )
);

-- Indexes for the operator's approval queue (state='ai_proposed' rows for
-- the current tenant, newest first) and for forensic lookup by tool_name.
CREATE INDEX idx_mcp_wp_tenant_state_created ON mcp_write_proposals
    (tenant_id, state, created_at DESC);
CREATE INDEX idx_mcp_wp_tenant_tool ON mcp_write_proposals
    (tenant_id, tool_name);

-- RLS: four-policy split (slice 014 canonical pattern). atlas_app cannot
-- read or write rows for any tenant other than the one named in the GUC.
ALTER TABLE mcp_write_proposals ENABLE ROW LEVEL SECURITY;
ALTER TABLE mcp_write_proposals FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON mcp_write_proposals
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON mcp_write_proposals
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON mcp_write_proposals
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON mcp_write_proposals
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON mcp_write_proposals TO atlas_app;
