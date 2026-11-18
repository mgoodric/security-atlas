-- security-atlas — #2406: carry a posture target forward to a new framework
-- version.
--
-- Posture targets are stored per framework version (#2136). Activating a
-- framework scope on version N+1 now copies the tenant's target on N, when
-- N+1 has none yet (internal/frameworkscope.Store.Activate). N is the
-- version the version-migration queue (ADR 0019) leads from, as for the
-- applicability carry-over (#1793). The copy is logged as its own action so
-- an auditor can tell it from a target set by hand; the row's `after` names
-- the source version and the person whose activation triggered it.
--
-- Widens the action CHECK only. Every existing value is kept.

ALTER TABLE framework_posture_target_audit_log
    DROP CONSTRAINT framework_posture_target_audit_log_action_check;
ALTER TABLE framework_posture_target_audit_log
    ADD CONSTRAINT framework_posture_target_audit_log_action_check
    CHECK (action IN ('set', 'carry_over'));
