-- Reverse #1736: drop the control anchor-fit columns.

ALTER TABLE controls
    DROP CONSTRAINT IF EXISTS controls_anchor_fit_note_required,
    DROP CONSTRAINT IF EXISTS controls_anchor_fit_valid,
    DROP COLUMN IF EXISTS anchor_fit_note,
    DROP COLUMN IF EXISTS anchor_fit;
