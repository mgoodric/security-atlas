-- Reverses 20261259000000_posture_target_carry_over.sql (#2406): restores
-- the framework_posture_target_audit_log action CHECK of #2136.
--
-- 'carry_over' rows are audit evidence, and the table is append-only. They
-- are not deleted here; archive and remove them deliberately first, or this
-- refuses to apply.

DO $$
DECLARE
    n BIGINT;
BEGIN
    SELECT count(*) INTO n FROM framework_posture_target_audit_log WHERE action = 'carry_over';
    IF n > 0 THEN
        RAISE EXCEPTION 'framework_posture_target_audit_log holds % carry-over audit row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261259000000.';
    END IF;
END
$$;

ALTER TABLE framework_posture_target_audit_log
    DROP CONSTRAINT framework_posture_target_audit_log_action_check;
ALTER TABLE framework_posture_target_audit_log
    ADD CONSTRAINT framework_posture_target_audit_log_action_check
    CHECK (action IN ('set'));
