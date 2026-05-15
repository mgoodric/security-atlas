-- Reverse of 20260511000032_controls_superseded_fk_deferrable.sql.
--
-- Restores `controls_superseded_by_fk` to its original slice-009 shape:
-- the same FOREIGN KEY (superseded_by -> controls.id) ON DELETE SET NULL,
-- but NON-deferrable (the Postgres default). Reversing this slice means
-- re-upload of control bundles is broken again, exactly as it was on
-- `main` before slice 068 — that is the intended round-trip behaviour.
--
-- Round-trip safe: up -> down -> up is byte-clean.

ALTER TABLE controls
    DROP CONSTRAINT controls_superseded_by_fk;

ALTER TABLE controls
    ADD CONSTRAINT controls_superseded_by_fk
        FOREIGN KEY (superseded_by) REFERENCES controls (id) ON DELETE SET NULL;
