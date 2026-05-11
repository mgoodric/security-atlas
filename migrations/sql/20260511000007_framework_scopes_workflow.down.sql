-- Reverse of 20260511000007_framework_scopes_workflow.sql.
--
-- Restores the slice-002 column shape on framework_scopes. Data preservation:
--   state (`activated` / `superseded` / etc) -> status ENUM: best-effort
--     mapping; rows in `review` collapse to `draft` because the ENUM has no
--     `review` value.
--   predicate (JSONB) -> predicate (TEXT): JSON serialized to text verbatim.
--   effective_from (TIMESTAMPTZ) -> effective_from (DATE): truncated to date.
--
-- The new approval / supersession columns are dropped (their values do not
-- map onto the slice-002 columns).

DROP TRIGGER IF EXISTS framework_scopes_bounce_on_predicate_change_trg ON framework_scopes;
DROP FUNCTION IF EXISTS framework_scopes_bounce_on_predicate_change();

DROP INDEX IF EXISTS framework_scopes_one_active;
DROP INDEX IF EXISTS idx_framework_scopes_tenant_fv_state;
DROP INDEX IF EXISTS idx_framework_scopes_effective_from;

DROP POLICY IF EXISTS tenant_read ON framework_scopes;
DROP POLICY IF EXISTS tenant_write ON framework_scopes;
DROP POLICY IF EXISTS tenant_update ON framework_scopes;
DROP POLICY IF EXISTS tenant_delete ON framework_scopes;

-- Re-create the slice-002 ENUM type so the column type-restore below works.
CREATE TYPE framework_scope_status AS ENUM (
    'draft',
    'approved',
    'active',
    'retired'
);

-- Stage: capture the slice-018 state column into a temporary text column so
-- we can map it onto the ENUM after dropping the new columns.
ALTER TABLE framework_scopes ADD COLUMN _state_tmp TEXT NULL;
UPDATE framework_scopes SET _state_tmp = state;

ALTER TABLE framework_scopes
    DROP CONSTRAINT IF EXISTS framework_scopes_superseded_by_fk;

ALTER TABLE framework_scopes
    DROP COLUMN IF EXISTS state,
    DROP COLUMN IF EXISTS predicate,
    DROP COLUMN IF EXISTS predicate_hash,
    DROP COLUMN IF EXISTS approver_user_id,
    DROP COLUMN IF EXISTS approved_at,
    DROP COLUMN IF EXISTS predicate_hash_at_approval,
    DROP COLUMN IF EXISTS approval_evidence_file_url,
    DROP COLUMN IF EXISTS approval_evidence_file_hash,
    DROP COLUMN IF EXISTS effective_from,
    DROP COLUMN IF EXISTS superseded_by,
    DROP COLUMN IF EXISTS superseded_at;

ALTER TABLE framework_scopes
    ADD COLUMN status framework_scope_status NOT NULL DEFAULT 'draft',
    ADD COLUMN predicate TEXT NOT NULL DEFAULT 'true',
    ADD COLUMN effective_from DATE NULL,
    ADD COLUMN effective_to DATE NULL,
    ADD COLUMN approved_by TEXT NULL,
    ADD COLUMN approval_evidence TEXT NULL;

-- Map the temporary state text onto the ENUM (best effort; unknown values
-- collapse to 'draft').
UPDATE framework_scopes
SET status = CASE
    WHEN _state_tmp = 'activated'  THEN 'active'::framework_scope_status
    WHEN _state_tmp = 'approved'   THEN 'approved'::framework_scope_status
    WHEN _state_tmp = 'superseded' THEN 'retired'::framework_scope_status
    ELSE 'draft'::framework_scope_status
END;

ALTER TABLE framework_scopes DROP COLUMN _state_tmp;

CREATE INDEX idx_framework_scopes_version_status
    ON framework_scopes (framework_version_id, status);

CREATE POLICY tenant_isolation ON framework_scopes
    USING (current_tenant_matches(tenant_id));
