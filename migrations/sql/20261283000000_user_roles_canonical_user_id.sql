-- #2661: a user_roles.user_id that reads as a uuid is stored in canonical
-- form (lowercase, hyphenated 8-4-4-4-12). Values that are not uuids
-- (key_..., bootstrap-admin, oauth_client:<id>, user:<uuid>, email-shaped
-- fixture ids) are left alone.
--
-- Why: the deactivation guard joins users on u.id::text = ur.user_id
-- (internal/moduleaccess/owners.go liveRoles), and u.id::text is always
-- canonical. A row stored as an upper-case, braced, urn:uuid: or 32-hex
-- spelling of a user's id would never join, so a disabled user's role would
-- still be listed. authz.DBRolesResolver already fails closed on such keys
-- (#2483); this makes the row impossible to write. Every write path stores
-- identity.RolesKey, which is canonical, so no row violates it today.
--
-- "Reads as a uuid" is the union of what Go's uuid.Parse and Postgres's
-- uuid input accept: an optional urn:uuid: prefix, optional braces, and 32
-- hex digits with hyphens anywhere between them, in any case.

DO $$
DECLARE
    bad BIGINT;
BEGIN
    SELECT count(*) INTO bad
      FROM user_roles
     WHERE regexp_replace(regexp_replace(lower(user_id), '^(urn:uuid:)?\{?', ''), '\}$', '')
               ~ '^(-*[0-9a-f]-*){32}$'
       AND user_id !~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
    IF bad > 0 THEN
        RAISE EXCEPTION 'user_roles: % row(s) store a non-canonical uuid user_id; see docs/runbooks/user-roles-canonical-user-id.md (#2661)', bad;
    END IF;
END
$$;

ALTER TABLE user_roles
    ADD CONSTRAINT user_roles_user_id_canonical_uuid
    CHECK (
        regexp_replace(regexp_replace(lower(user_id), '^(urn:uuid:)?\{?', ''), '\}$', '')
            !~ '^(-*[0-9a-f]-*){32}$'
        OR user_id ~ '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
    );
