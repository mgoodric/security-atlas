-- Reverses 20261248000000_saved_views_risks_surface.sql.
--
-- Risk register views have no home under the narrower CHECK, so they are
-- deleted first.

DELETE FROM saved_views WHERE surface = 'risks';

ALTER TABLE saved_views
    DROP CONSTRAINT saved_views_surface_check;

ALTER TABLE saved_views
    ADD CONSTRAINT saved_views_surface_check
        CHECK (surface IN ('controls'));
