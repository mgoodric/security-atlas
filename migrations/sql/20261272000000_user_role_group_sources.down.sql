-- Reverse #2760: drop user_role_group_sources. The group-derived user_roles
-- rows it attributed stay; the resolver before #2760 reads those unscoped.

DROP TABLE IF EXISTS user_role_group_sources;
