-- security-atlas -- #2384: an optional label on an API key.
--
-- An admin names a key at mint time (POST /v1/admin/credentials) so a page
-- can say which key acted: <Person> reads "API key '<label>' (owner:
-- <user>)". The label is free text the admin chose; it names nothing
-- secret, and the token, its prefix and last4 stay out of every person
-- rendering.
--
-- NULL is an unlabelled key, the shape of every key minted before this.
-- The CHECK bounds the length and refuses an empty string, so "no label"
-- has one spelling. A rotate successor inherits its predecessor's label.

ALTER TABLE api_keys ADD COLUMN label TEXT NULL;

ALTER TABLE api_keys ADD CONSTRAINT api_keys_label_chk
    CHECK (label IS NULL OR char_length(label) BETWEEN 1 AND 100);

COMMENT ON COLUMN api_keys.label IS
    'Admin-chosen name for the key, shown in place of its id (#2384). '
    'Never the token, its prefix or last4.';
