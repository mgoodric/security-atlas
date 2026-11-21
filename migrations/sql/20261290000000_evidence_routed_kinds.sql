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

ALTER TABLE evidence_records
    DROP CONSTRAINT evidence_records_control_ref_nonempty,
    ADD CONSTRAINT evidence_records_control_ref_nonempty
        CHECK (length(control_ref) > 0 OR control_id IS NULL);
