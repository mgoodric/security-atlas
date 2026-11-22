-- security-atlas — #2881: the uploader of a risk import batch, as a person.
--
-- Matt's decision on #2881: the uploader never approves or rejects their
-- own batch. uploaded_by records credstore.PrincipalOf, which is a bare
-- users(id) uuid for a person and a credential name ("key_...",
-- "oauth_client:<id>", "bootstrap-admin") otherwise, so it cannot be
-- compared to the deciding users(id) without parsing. uploader_user_id is
-- that users(id), NULL when no person uploaded.
--
-- Backfill: an existing batch's uploaded_by is a person's users(id)
-- exactly when it parses as a uuid (PrincipalOf writes nothing else that
-- does). No foreign key, as on risk_import_rows.owner_user_id: the column
-- records who uploaded, and outlives the user.

ALTER TABLE risk_import_batches
    ADD COLUMN IF NOT EXISTS uploader_user_id UUID NULL;

UPDATE risk_import_batches
SET uploader_user_id = uploaded_by::uuid
WHERE uploader_user_id IS NULL
  AND uploaded_by ~* '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$';
