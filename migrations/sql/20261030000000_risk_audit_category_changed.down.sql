-- Reverse #1812. The category_changed rows are part of the append-only
-- trail; the trigger that guards it refuses DELETE, so the down step
-- cannot remove them and refuses while any exist (#2233).

DO $$
DECLARE
    n bigint;
BEGIN
    SELECT count(*) INTO n FROM risk_audit_log
    WHERE action_type = 'category_changed';
    IF n > 0 THEN
        RAISE EXCEPTION 'risk_audit_log holds % category_changed row(s); refusing to delete audit evidence', n
            USING HINT = 'Archive and remove those rows deliberately before reversing 20261030000000.';
    END IF;
END
$$;

ALTER TABLE risk_audit_log
    DROP CONSTRAINT risk_audit_log_action_type_check;

ALTER TABLE risk_audit_log
    ADD CONSTRAINT risk_audit_log_action_type_check
    CHECK (action_type IN ('placement_changed'));
