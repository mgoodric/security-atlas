-- Reverse #1731: drop the points-of-focus metadata column.

ALTER TABLE framework_requirements DROP COLUMN IF EXISTS points_of_focus;
