-- Reverse #2661.

ALTER TABLE user_roles
    DROP CONSTRAINT IF EXISTS user_roles_user_id_canonical_uuid;
