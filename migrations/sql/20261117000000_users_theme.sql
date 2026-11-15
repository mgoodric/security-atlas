-- security-atlas — #2142: per-user theme preference.
--
-- The /settings theme choice (System / Light / Dark) was stored only in the
-- browser's localStorage, so it did not follow the user to another device.
-- This adds it to the users row beside time_zone (slice 108 D1: the users
-- table is the profile store; no sibling table) and PATCH /v1/me writes it.
--
-- '' (the default) means the user has never chosen: /v1/me reports theme as
-- null and the browser's cached choice stands. Defaulting to 'system' instead
-- would silently overwrite every existing user's cached Light/Dark choice the
-- first time they loaded a page after this ships. 'system' defers to the OS.
-- The CHECK mirrors the Go whitelist in internal/api/me (validThemes); the two
-- move together.
--
-- users already carries RLS (slice 034); an additive column needs no new
-- policy. Reversible via 20261117000000_users_theme.down.sql.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS theme TEXT NOT NULL DEFAULT ''
        CONSTRAINT users_theme_check CHECK (theme IN ('', 'system', 'light', 'dark'));
