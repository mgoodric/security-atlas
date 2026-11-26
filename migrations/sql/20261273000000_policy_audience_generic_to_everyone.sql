-- security-atlas — #2588 (from #2524): the generic free-text policy
-- audiences become 'everyone'.
--
-- Before #2524 a policy's acknowledgment_required_roles was free text, and a
-- value such as 'employee' never matched a session, whose roles are the
-- canonical enum. This migration rewrites the four generic values Matt
-- approved, in every policy row whatever its status:
--
--   'employee', 'all', 'all_staff', 'contractor'  -> 'everyone'
--
-- Matching is exact (case-sensitive), as in
-- scripts/dry-run-2524-policy-audiences.sql, so the rows changed are the
-- rows that script counts. Every other value (a team name such as
-- 'engineer', or anything else) is left as it is; the UI flags it as an
-- unrecognised audience for an admin to re-target. Order is kept and a
-- value that would repeat is dropped, so {employee, contractor} becomes
-- {everyone} and {engineer, all} becomes {engineer, everyone}.
--
-- Each changed policy gets one policy_audit_log row in the same statement:
--
--   * action 'audience_mapped', actor 'system:migration-2588';
--   * previous_audience / new_audience carry the array before and after;
--   * policy_title / policy_version name the row as it was.
--
-- Safe to re-run: a second run finds no generic value left, so it changes
-- no row and writes no audit row. With nothing matching it is a no-op.
--
-- policy_audit_log stays append-only (its trigger is untouched) and keeps
-- its RLS policies. Runs as the migrate role, which bypasses RLS.
--
-- Reversible via 20261273000000_policy_audience_generic_to_everyone.down.sql
-- (schema only, and only while no 'audience_mapped' row exists).

ALTER TABLE policy_audit_log
    DROP CONSTRAINT IF EXISTS policy_audit_log_action_check;

ALTER TABLE policy_audit_log
    ADD CONSTRAINT policy_audit_log_action_check
        CHECK (action IN ('imported', 'review_date_updated', 'draft_discarded', 'self_approved',
                          'submitted', 'resubmitted', 'approved', 'draft_edited',
                          'audience_mapped'));

ALTER TABLE policy_audit_log
    ADD COLUMN IF NOT EXISTS previous_audience TEXT[] NULL,
    ADD COLUMN IF NOT EXISTS new_audience      TEXT[] NULL;

WITH mapped AS (
    SELECT p.id,
           p.tenant_id,
           p.title,
           p.version,
           p.acknowledgment_required_roles AS before,
           ARRAY(
               SELECT m.v
               FROM (
                   SELECT CASE WHEN u.r IN ('employee', 'all', 'all_staff', 'contractor')
                               THEN 'everyone' ELSE u.r END AS v,
                          min(u.ord) AS first_at
                   FROM unnest(p.acknowledgment_required_roles) WITH ORDINALITY AS u(r, ord)
                   GROUP BY 1
               ) m
               ORDER BY m.first_at
           ) AS after
    FROM policies p
    WHERE p.acknowledgment_required_roles && ARRAY['employee', 'all', 'all_staff', 'contractor']::text[]
    FOR UPDATE OF p
),
updated AS (
    UPDATE policies p
    SET acknowledgment_required_roles = mapped.after,
        updated_at = now()
    FROM mapped
    WHERE p.id = mapped.id AND p.tenant_id = mapped.tenant_id
    RETURNING p.id
)
INSERT INTO policy_audit_log (
    tenant_id, policy_id, action, actor,
    policy_title, policy_version,
    previous_audience, new_audience
)
SELECT mapped.tenant_id, mapped.id, 'audience_mapped', 'system:migration-2588',
       mapped.title, mapped.version,
       mapped.before, mapped.after
FROM mapped
JOIN updated ON updated.id = mapped.id;
