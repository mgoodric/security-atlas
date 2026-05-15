-- Reverse of 20260511000000_extensions.sql (slice 065).
--
-- Drops the pgcrypto extension. Safe because no schema object depends on
-- the extension at DDL time: `digest()` and `gen_random_uuid()` are only
-- ever invoked at DML time (seed.sql's hash computation, column DEFAULTs),
-- never referenced in a stored-object definition that Postgres would track
-- as a dependency. `IF EXISTS` keeps the down migration a no-op on a
-- database where the extension was never created.
--
-- This runs LAST in the CI down-then-up round-trip (down migrations are
-- applied in reverse-lexical order, so `_extensions.down.sql` is the final
-- step). By then every table and enum from later migrations is already
-- gone, so nothing can be holding a dependency on pgcrypto.

DROP EXTENSION IF EXISTS pgcrypto;
