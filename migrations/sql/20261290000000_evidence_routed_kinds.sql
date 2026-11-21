-- security-atlas -- #2924: connector evidence routed to controls by evidence_kind.
--
-- A record of a routed kind (internal/evidence/routed) is stored bound to no
-- control and is read by every control whose evidence_queries declare its
-- kind. A connector run with no --*-control flag pushes it with an empty
-- control_id, which ingest keeps verbatim in control_ref: verify rebuilds the
-- hashed wire record from control_ref, so it cannot be replaced by a
-- placeholder.
--
-- evidence_records_control_ref_nonempty therefore admits an empty control_ref
-- on a row bound to no control. A row bound to a control still needs one.
-- Which kinds may be pushed without a control is enforced at ingest, where
-- the routed list lives; repeating the list here would let the two drift.
--
-- The new CHECK is added NOT VALID and then validated. ADD CONSTRAINT with
-- validation holds ACCESS EXCLUSIVE on the ledger for the whole scan;
-- VALIDATE CONSTRAINT scans under SHARE UPDATE EXCLUSIVE, so pushes keep
-- landing while it runs. The new predicate is weaker than the old one, so
-- every existing row satisfies it.

ALTER TABLE evidence_records
    DROP CONSTRAINT evidence_records_control_ref_nonempty,
    ADD CONSTRAINT evidence_records_control_ref_nonempty
        CHECK (length(control_ref) > 0 OR control_id IS NULL) NOT VALID;

ALTER TABLE evidence_records
    VALIDATE CONSTRAINT evidence_records_control_ref_nonempty;

-- The routing cutover: when this migration applied. An audit period frozen,
-- or a population created or frozen, before it keeps reading exactly what it
-- read before routing existed: its control-free records of a routed kind are
-- excluded (internal/evidence/routed.KindsAsOf). Reads compare against this
-- recorded instant, never against the wall clock at read time.
--
-- One row, global: the cutover is a property of the deployment, not of a
-- tenant, so the table has no tenant_id and no RLS (scripts/audit-rls.sh
-- covers tenant-scoped tables only). The app role may read it, never write it.
CREATE TABLE evidence_routing_cutover (
    singleton boolean     PRIMARY KEY DEFAULT true CHECK (singleton),
    cut_at    timestamptz NOT NULL DEFAULT now()
);

INSERT INTO evidence_routing_cutover DEFAULT VALUES;

GRANT SELECT ON evidence_routing_cutover TO atlas_app;
