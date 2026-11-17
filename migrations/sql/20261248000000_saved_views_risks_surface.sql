-- security-atlas — #2133: saved views for the risk register.
--
-- saved_views.surface names the list a view belongs to. Slice 468 pinned it
-- to 'controls'; the risk register now saves views too, so the CHECK admits
-- 'risks'. Nothing else about the table changes: RLS still scopes the
-- tenant, the handler's user_id predicate still scopes the user, and the
-- per-(tenant, user, surface) name index already keeps each list's names
-- apart.
--
-- Reversible via 20261248000000_saved_views_risks_surface.down.sql.

ALTER TABLE saved_views
    DROP CONSTRAINT saved_views_surface_check;

ALTER TABLE saved_views
    ADD CONSTRAINT saved_views_surface_check
        CHECK (surface IN ('controls', 'risks'));
