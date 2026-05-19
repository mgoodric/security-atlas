-- Slice 164 — Playwright e2e seed for `web/e2e/settings.spec.ts`.
--
-- Builds on fixtures/walkthroughs/00-seed.sql (applied first by the
-- harness). Establishes the preconditions named in the spec preamble:
--
--   - a real `users` row in the demo tenant so /v1/me resolves to a
--     non-synthetic profile (drives AC-1, AC-2, AC-8, AC-10)
--   - `user_roles` rows for `admin` AND `grc_engineer` so the
--     multi-role tail badge has a non-admin secondary to render
--     (drives AC-10)
--   - one `user_notification_preferences` row with `enabled=false`
--     so the preferences GET surfaces a non-default cell (drives
--     AC-3 and AC-7 — the latter only checks the row count, but the
--     PATCH round-trip in AC-3 needs an initial state to flip)
--   - two `sessions` rows: one with the slice 162 augmented
--     UA + IP + geo columns populated, one bare. Drives AC-5
--     metadata-line assertion + AC-5 honest-empty assertion (slice
--     162 P0-162-1)
--   - two `api_keys` rows (predecessor + successor with rotated_from
--     chain) so the table branch of AC-9 has rows AND the slice 062
--     / 063 muted "rotated → …last4" badge has data. AC-11
--     (slice 163 rotate-twice-in-a-row) exercises the action itself;
--     the fixture only needs starting rows.
--
-- The slice 164 decisions log
-- (docs/audit-log/164-settings-e2e-seed-decisions.md) captures the
-- JUDGMENT calls behind these choices.
--
-- Hard constraints (P0-164-1):
--   - NO vendor-prefixed token strings (no ghp_/sk_/eyJ/AKIA).
--   - All IDs deterministic so re-runs are byte-stable.
--   - Every INSERT uses ON CONFLICT DO NOTHING for idempotency.

\set ON_ERROR_STOP on

BEGIN;

SET LOCAL app.current_tenant = '00000000-0000-0000-0000-00000000d3a0';

-- ============================================================
-- users — the principal /v1/me resolves to
-- ============================================================
-- The user UUID is the value `seed.ts` sets as `api_keys.issued_by`
-- when `name === "settings"`. It is exported as `DEMO_USER_ID` from
-- seed.ts so specs can reference the row by symbolic name.
--
-- time_zone = 'America/New_York' is the AC-8 discriminator: the
-- select must reflect this saved value on mount and round-trip a
-- new value through PATCH /v1/me.
INSERT INTO users (
    id,
    tenant_id,
    email,
    display_name,
    status,
    idp_issuer,
    idp_subject,
    time_zone
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    '00000000-0000-0000-0000-00000000d3a0',
    'settings-e2e-user@example.invalid',
    'Settings E2E Operator',
    'active',
    '',
    '',
    'America/New_York'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- user_roles — admin + grc_engineer
-- ============================================================
-- user_id column is TEXT (slice 035 migration). The user_id literal
-- below is the stringified form of the users.id above.
--
-- Two rows so the slice 130 roles list returned by /v1/me has two
-- entries; the settings page renders one primary badge plus a tail
-- badge ("+ grc_engineer") off the secondary role. AC-10 asserts
-- the tail's presence + the "+ grc_engineer" text.
INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    'admin',
    'slice-164-fixture'
)
ON CONFLICT DO NOTHING;

INSERT INTO user_roles (tenant_id, user_id, role, granted_by)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    'grc_engineer',
    'slice-164-fixture'
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- user_notification_preferences — one non-default cell
-- ============================================================
-- enabled=false for (audit_period_assignment, email) gives AC-3 an
-- initial state to flip on (the test clicks the toggle, waits for
-- the PATCH, reloads, and asserts the new state holds). All other
-- (event, channel) tuples render as enabled=true via the
-- default-on-missing-row policy in userprefs.Get.
INSERT INTO user_notification_preferences (
    tenant_id, user_id, event, channel, enabled
)
VALUES (
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    'audit_period_assignment',
    'email',
    false
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- sessions — augmented row + bare row
-- ============================================================
-- The session id column is TEXT (the cookie's opaque value). The
-- augmented row carries slice 162's UA / IP / geo so the per-row
-- metadata line renders; the bare row leaves them all NULL so the
-- P0-162-1 honest-empty assertion has a target.
--
-- last_seen_at on the bare row is older so it sorts LAST under
-- ListSessionsForUser's `ORDER BY last_seen_at DESC` — the spec's
-- AC-5 bare-row selector uses `.last()` to pick it.
INSERT INTO sessions (
    id,
    tenant_id,
    user_id,
    idp_issuer,
    idp_subject,
    issued_at,
    expires_at,
    last_seen_at,
    user_agent,
    ip_address,
    geo_country,
    geo_city
)
VALUES (
    'e2e-settings-session-augmented-01',
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    '',
    '',
    now() - INTERVAL '2 hours',
    now() + INTERVAL '7 days',
    now(),
    'Mozilla/5.0 (slice-164-e2e fixture user-agent)',
    '192.0.2.18',
    'US',
    'San Francisco'
)
ON CONFLICT DO NOTHING;

INSERT INTO sessions (
    id,
    tenant_id,
    user_id,
    idp_issuer,
    idp_subject,
    issued_at,
    expires_at,
    last_seen_at,
    user_agent,
    ip_address,
    geo_country,
    geo_city
)
VALUES (
    'e2e-settings-session-bare-02',
    '00000000-0000-0000-0000-00000000d3a0',
    '44444444-4444-4444-4444-444444440001',
    '',
    '',
    now() - INTERVAL '3 hours',
    now() + INTERVAL '7 days',
    now() - INTERVAL '1 hour',
    NULL,
    NULL,
    NULL,
    NULL
)
ON CONFLICT DO NOTHING;

-- ============================================================
-- api_keys — predecessor + successor with rotated_from chain
-- ============================================================
-- Two non-bearer api_keys rows so:
--   - the table branch of AC-9 renders ≥ 1 row,
--   - the slice 062 / 063 muted "rotated → …last4" link on the
--     predecessor has a successor target to point at,
--   - AC-11 (slice 163 rotate-twice-in-a-row) has visible rows to
--     act on when it clicks the Rotate button on the SUCCESSOR.
--
-- token_hash is BYTEA with octet_length=32 enforced. The two values
-- below are deterministic single-byte-repeat patterns
-- (`aa…aa` / `bb…bb`) — they are NOT real HMAC outputs of any
-- bearer plaintext. The rows are therefore unauthenticable as
-- bearer credentials; only `test-bearer-e2e` (whose HMAC seed.ts
-- computes at runtime) authenticates the spec.
--
-- The predecessor's `retires_at` is in the future so the slice 062
-- grace-window check renders the muted-row state with a real ETA;
-- the successor is the current valid row.
INSERT INTO api_keys (
    id,
    tenant_id,
    token_hash,
    scope_predicate,
    allowed_kinds,
    issued_by,
    issued_at,
    expires_at,
    is_admin,
    owner_roles,
    last4,
    ttl_seconds,
    rotated_from,
    retires_at
)
VALUES (
    '55555555-5555-5555-5555-555555550001',
    '00000000-0000-0000-0000-00000000d3a0',
    decode(repeat('aa', 32), 'hex'),
    '{}'::jsonb,
    ARRAY['evidence.kind.v1']::TEXT[],
    '44444444-4444-4444-4444-444444440001',
    now() - INTERVAL '30 days',
    now() + INTERVAL '90 days',
    false,
    ARRAY['grc_engineer']::TEXT[],
    'rt01',
    7776000,
    NULL,
    now() + INTERVAL '7 days'
)
ON CONFLICT DO NOTHING;

INSERT INTO api_keys (
    id,
    tenant_id,
    token_hash,
    scope_predicate,
    allowed_kinds,
    issued_by,
    issued_at,
    expires_at,
    is_admin,
    owner_roles,
    last4,
    ttl_seconds,
    rotated_from,
    retires_at
)
VALUES (
    '55555555-5555-5555-5555-555555550002',
    '00000000-0000-0000-0000-00000000d3a0',
    decode(repeat('bb', 32), 'hex'),
    '{}'::jsonb,
    ARRAY['evidence.kind.v1']::TEXT[],
    '44444444-4444-4444-4444-444444440001',
    now() - INTERVAL '1 day',
    now() + INTERVAL '90 days',
    false,
    ARRAY['grc_engineer']::TEXT[],
    'rt02',
    7776000,
    '55555555-5555-5555-5555-555555550001',
    NULL
)
ON CONFLICT DO NOTHING;

COMMIT;
