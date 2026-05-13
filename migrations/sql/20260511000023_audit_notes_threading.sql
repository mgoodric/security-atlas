-- security-atlas — Audit Hub threaded comments + visibility extension (slice 029).
--
-- Extends slice 025's audit_notes table to activate the canvas §8.5 Audit Hub
-- pattern -- the auditor↔auditee shared-thread workflow that slice 025
-- explicitly deferred. Four schema-level changes plus an append-only conversion:
--
--   1. parent_note_id (self-FK) -- replies thread to their parent. The FK is
--      composite (tenant_id, parent_note_id) -> audit_notes(tenant_id, id)
--      so cross-tenant linkage is impossible. ON DELETE RESTRICT preserves
--      the thread integrity that AC-6 (immutability) implies.
--
--   2. visibility CHECK relaxed -- the slice 025 pin to 'auditor_only' is
--      lifted so 'shared' is now permitted. Existing slice-025 data already
--      uses 'auditor_only' and the column DEFAULT stays 'auditor_only', so
--      slice-025 callers that omit the field continue to write the private
--      values they always have. (Terminology note: slice 029's doc uses
--      'auditor_private' but slice 025 shipped 'auditor_only'; we keep the
--      existing value rather than renaming and breaking slice-025 data +
--      tests. The PR body documents this deliberate doc-vs-code drift.)
--
--   3. scope_type CHECK extended with 'walkthrough' -- canvas §8.3 walkthrough
--      recordings can now be the anchor of a comment. The slice 025 enum
--      ('control','finding','sample','period') is preserved; this slice
--      adds one value. The `period` scope from slice 025 stays because
--      auditor-level commentary on an entire period without an object
--      anchor is a legitimate use case.
--
--   4. Append-only conversion (AC-6) -- the slice 025 tenant_update and
--      tenant_delete RLS policies are dropped, leaving only tenant_read +
--      tenant_write. Under FORCE ROW LEVEL SECURITY, the absence of
--      UPDATE/DELETE policies prevents atlas_app from mutating posted
--      notes -- edits create reply chains, not in-place mutation. The
--      GRANTs are also narrowed to SELECT,INSERT. Same pattern as
--      feature_flag_audit_log (slice 019), exception_audit_log (slice 011),
--      evidence_audit_log (slice 013), sample_audit_log (slice 026),
--      decision_audit_log (slice 035).
--
-- Constitutional invariants honored:
--   #6  Tenant isolation at the DB layer. The append-only policy split
--       preserves FORCE ROW LEVEL SECURITY; tenant_read + tenant_write keep
--       the tenant boundary.
--   #10 Audit-period freezing -- audit_notes continue to pin to a specific
--       audit_period_id via the slice-025 composite FK. Replies cannot
--       cross periods (the new parent_note_id self-FK only matches rows in
--       the same tenant; parent's audit_period_id must equal child's,
--       enforced by the application layer at insertion time).
--
-- Anti-criteria honored at the schema layer (P0):
--   - Append-only: no UPDATE/DELETE path for atlas_app on audit_notes.
--   - Visibility leak: the CHECK only governs the column domain; the
--     row-level visibility filter that prevents auditees from reading
--     auditor_only rows lives at the query layer (visibility = 'shared' OR
--     author_user_id = $caller).
--   - No chat-product features: no edit history, no typing indicators, no
--     presence; this migration only adds threading + visibility.
--
-- Migration is reversible via 20260511000023_audit_notes_threading.down.sql.

-- ===== 1. parent_note_id self-FK =====

ALTER TABLE audit_notes
    ADD COLUMN parent_note_id UUID NULL;

-- Composite self-FK keeps a reply in the same tenant as its parent. The
-- referenced (tenant_id, id) column pair is the table's PRIMARY KEY (id)
-- plus tenant_id; we need a UNIQUE on the pair for the FK to target it.
ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_tenant_id_unique UNIQUE (tenant_id, id);

ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_parent_fk
    FOREIGN KEY (tenant_id, parent_note_id)
    REFERENCES audit_notes(tenant_id, id) ON DELETE RESTRICT;

-- Lookup index for the thread-retrieval recursive CTE -- a reply walks
-- from leaf to root via parent_note_id, then the root walks back down
-- via children. Index by (tenant, parent) drives the descending half.
CREATE INDEX idx_audit_notes_tenant_parent
    ON audit_notes (tenant_id, parent_note_id)
    WHERE parent_note_id IS NOT NULL;

-- Scope-driven thread lookup -- the canonical GET /v1/audit-notes path
-- queries by (tenant, scope_type, scope_id, audit_period_id) to find the
-- thread root(s).
CREATE INDEX idx_audit_notes_tenant_scope_period
    ON audit_notes (tenant_id, scope_type, scope_id, audit_period_id);

-- ===== 2. Relax visibility CHECK to allow 'shared' =====

ALTER TABLE audit_notes
    DROP CONSTRAINT audit_notes_visibility_chk;

ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_visibility_chk
    CHECK (visibility IN ('auditor_only', 'shared'));

-- ===== 3. Extend scope_type CHECK with 'walkthrough' =====

ALTER TABLE audit_notes
    DROP CONSTRAINT audit_notes_scope_type_chk;

ALTER TABLE audit_notes
    ADD CONSTRAINT audit_notes_scope_type_chk
    CHECK (scope_type IN ('control', 'finding', 'sample', 'period', 'walkthrough'));

-- ===== 4. Append-only conversion (drop UPDATE + DELETE policies) =====

-- Slice 025 created the standard four-policy split (tenant_read,
-- tenant_write, tenant_update, tenant_delete). For AC-6 (immutability),
-- this slice drops the two mutation policies. The table remains under
-- FORCE ROW LEVEL SECURITY; the absence of a matching policy for an
-- UPDATE/DELETE statement causes Postgres to reject it. No SELECT/INSERT
-- path is affected.
DROP POLICY IF EXISTS tenant_update ON audit_notes;
DROP POLICY IF EXISTS tenant_delete ON audit_notes;

-- Narrow GRANTs to match. atlas_app retains SELECT + INSERT only on
-- audit_notes from this point forward.
REVOKE UPDATE, DELETE ON audit_notes FROM atlas_app;
