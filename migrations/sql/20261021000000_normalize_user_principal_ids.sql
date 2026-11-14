-- #2183: rewrite "user:<uuid>" principal ids to the bare users(id).
--
-- A local-login / PKCE / device-grant JWT carries Subject "user:<uuid>", and
-- jwtmw bridges it verbatim into cred.UserID. Two TEXT columns were written
-- from that raw value and are now read by the bare users(id)
-- (credstore.CallerUserID / identity.UserID):
--
--   notifications.recipient_user_id     audit-note reply notifications
--   auditor_assignments.user_id         auditor -> audit period grants
--
-- Without this rewrite, rows written in the prefixed form before #2183 would
-- disappear from their recipient's /v1/me/notifications, from the digest
-- channels, and from /v1/me/audit-period(s) and the authz attrs resolver.
--
-- Only a canonical uuid after the "user:" prefix is rewritten, lower-cased
-- to match uuid.UUID.String(). The nil uuid is left alone, as identity.UserID
-- does not resolve it. Every other value (key_..., bootstrap-admin,
-- oauth_client:..., the e2e fixture's email id) is untouched.
--
-- Idempotent: a second run finds no "user:"-prefixed uuid rows.

-- auditor_assignments' primary key is (tenant_id, user_id, audit_period_id).
-- If one person holds the same grant in both forms, keep the bare row and
-- drop the prefixed duplicate before rewriting, so the UPDATE cannot collide.
DELETE FROM auditor_assignments AS prefixed
USING auditor_assignments AS bare
WHERE prefixed.user_id ~ '^user:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
  AND prefixed.user_id <> 'user:00000000-0000-0000-0000-000000000000'
  AND bare.tenant_id = prefixed.tenant_id
  AND bare.audit_period_id = prefixed.audit_period_id
  AND bare.user_id = lower(substr(prefixed.user_id, 6));

UPDATE auditor_assignments
SET user_id = lower(substr(user_id, 6))
WHERE user_id ~ '^user:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
  AND user_id <> 'user:00000000-0000-0000-0000-000000000000';

-- notifications has no uniqueness on recipient_user_id; a plain rewrite.
UPDATE notifications
SET recipient_user_id = lower(substr(recipient_user_id, 6))
WHERE recipient_user_id ~ '^user:[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$'
  AND recipient_user_id <> 'user:00000000-0000-0000-0000-000000000000';
