-- security-atlas -- #2112: the `excepted` evaluation result.
--
-- The evaluation engine now reads active exceptions. A control evaluated in a
-- scope cell that an active exception's scope_cell_predicate covers records
-- the result `excepted` instead of its normal result. `excepted` is never a
-- pass: it is displayed and counted as its own value.
--
-- control_evaluations.result reuses the slice-002 `evidence_result` enum, so
-- the value is added to that enum. Every existing value (pass, fail, na,
-- inconclusive) is kept.
--
-- The enum is shared with evidence_records.result, and an exception is a
-- property of the evaluation, never of a piece of evidence. A new CHECK keeps
-- `excepted` out of the evidence ledger: no connector can push it.
--
-- Postgres refuses to use an enum value inside the transaction that added it,
-- and deploy/docker/bootstrap/migrate.sh applies each file with
-- --single-transaction. Both CHECKs below therefore compare `result::text`,
-- which never parses 'excepted' as an enum literal.
--
-- The control_evaluations no-evidence CHECK is widened: an excepted cell with
-- no evidence records `excepted` + `no_evidence`. Every row it accepted
-- before it still accepts.
--
-- No .down.sql: Postgres cannot drop an enum value. The type itself is
-- dropped by 20260511000000_init.down.sql. IF NOT EXISTS keeps a re-run
-- idempotent.

ALTER TYPE evidence_result ADD VALUE IF NOT EXISTS 'excepted';

ALTER TABLE evidence_records
    ADD CONSTRAINT evidence_records_result_not_excepted
        CHECK (result::text <> 'excepted');

ALTER TABLE control_evaluations
    DROP CONSTRAINT control_evaluations_no_evidence_coherent;

ALTER TABLE control_evaluations
    ADD CONSTRAINT control_evaluations_no_evidence_coherent
        CHECK (
            freshness_status <> 'no_evidence'
            OR (evidence_count_in_window = 0
                AND result::text IN ('inconclusive', 'excepted'))
        );
