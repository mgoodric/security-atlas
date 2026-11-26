-- security-atlas -- #1880: document the ledger pointer columns as #1867
-- left them, and correct that migration's note on its locks.
--
-- Migration 026 described window_start and meta_risk_id in SQL comments
-- that still say "Set only when outcome = 'fired'". Since #1867 a
-- near_miss / no_match row that refreshed a meta-risk names it too. Those
-- comments are in an applied file, so the current meaning goes into the
-- catalog here instead.
--
-- Correction to 20260811000000_aggregation_evaluation_meta_risk_pointer:
-- its header says adding the CHECK NOT VALID and validating it separately
-- keeps the validation scan off ACCESS EXCLUSIVE. Only partly true. The
-- DROP CONSTRAINT and the ADD CONSTRAINT ... NOT VALID both take ACCESS
-- EXCLUSIVE on aggregation_rule_evaluations. The split helps only when
-- each statement commits on its own (psql -f without --single-transaction,
-- as `just migrate-up` runs it): the ACCESS EXCLUSIVE is then held for
-- the catalog change, not for the scan. Under a runner that wraps the
-- file in one transaction, the DROP's ACCESS EXCLUSIVE is held through
-- VALIDATE and the split buys nothing. The table is small and append-only,
-- so either way the lock is brief.

COMMENT ON COLUMN aggregation_rule_evaluations.window_start IS
    'Window boundary of the meta-risk this evaluation created, updated or refreshed. '
    'Always set on fired; set on near_miss / no_match only when the evaluation '
    'refreshed an existing meta-risk (#1867). Set together with meta_risk_id.';

COMMENT ON COLUMN aggregation_rule_evaluations.meta_risk_id IS
    'Meta-risk this evaluation created, updated or refreshed. Always set on fired; '
    'set on near_miss / no_match only when the evaluation refreshed an existing '
    'meta-risk (#1867). Set together with window_start. Not a FK: the audit trail '
    'must survive meta-risk deletion.';
