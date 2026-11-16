-- Reverse #2026: drop the points_of_focus array CHECK.

ALTER TABLE framework_requirements
    DROP CONSTRAINT IF EXISTS framework_requirements_points_of_focus_array;
