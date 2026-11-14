# Runbook — recovery keys minted before #1893

**Scope:** admin API keys that `reset-bootstrap` minted on a build that had
#1886 but not #1893. On `main` that window is 2026-11-13 12:51 to 14:48
(-0800). What matters is when your install ran such a build, not the merge
times.

**Audience:** the self-host operator who ran `atlas-cli credentials
--reset-bootstrap` (or `POST /v1/install/reset-bootstrap`) on such a build.

## Why these keys need a look

#1886 made `reset-bootstrap` mint an admin key. #1893 then gave that key a
TTL (24h by default) and the `api_keys.purpose = 'bootstrap-recovery'`
marker, so the next reset or the first sign-in revokes it (ADR 0003,
"Recovery key bounds"). A key minted between the two has neither. It never
expires, and nothing revokes it automatically.

## List the candidates

Run as the migrate/owner role (`$DATABASE_URL`). The app role sees one
tenant at a time under RLS.

```sql
SELECT id, tenant_id, issued_at, last_used_at, last4
  FROM api_keys
 WHERE is_admin
   AND issued_by IS NULL
   AND purpose IS NULL
   AND ttl_seconds = 0
   AND revoked_at IS NULL
   AND issued_at BETWEEN :'window_start' AND :'window_end'
 ORDER BY issued_at;
```

Set `window_start` and `window_end` to when the install ran the #1886
build (deploy of #1886 to deploy of #1893), for example
`psql "$DATABASE_URL" -v window_start='2026-11-13 12:51-08' -v window_end='2026-11-13 14:48-08'`.
The quoted `:'window_start'` form makes psql pass each value as a string
literal. If you know when you ran the reset, narrow the window to a few
minutes around it.

The columns alone cannot tell a recovery key from the admin key the platform
mints at boot for `ATLAS_BOOTSTRAP_TENANT`: both are admin, have no
`issued_by`, no purpose and no TTL. Tell them apart by time:

- **Boot key: do not revoke.** A row whose `issued_at` matches a server
  start is the boot key. That includes the restart that deployed #1893.
  Check the platform log for every boot time in the window before you
  revoke anything.
- **Recovery key.** Its `issued_at` matches the reset call. If the
  bootstrap-token file the reset wrote is still on disk, its bearer's last
  four characters match the row's `last4`. The file may be gone, though:
  first sign-in deletes it, and a later reset overwrites it. Without the
  file, timing is the only signal.

## Revoke

For each row you have confirmed is a recovery key and no longer need:

```bash
atlas-cli credentials revoke --id <id>
```

If you are signed in with that key, sign in another way first. Revoking it
ends that session.

## No automatic change

There is no migration or CLI step that marks or revokes these keys. The
reasons are in
[`docs/audit-log/1906-recovery-key-follow-ups-decisions.md`](../audit-log/1906-recovery-key-follow-ups-decisions.md)
D3.
