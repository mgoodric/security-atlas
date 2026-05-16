-- Slice 070 — fixture for `schema-registry-seed-and-validate.md`.
--
-- Pre-loads ONE evidence_kind schema (mirroring what `DefaultSeed()` +
-- the schemas embed.FS would install on platform boot) so the
-- walkthrough's `LoadFromDB` block has something to find. The schema is
-- a minimal `demo.encryption_state.v1` with an `encrypted` boolean
-- and a `bucket` string — just enough to demonstrate the validation
-- path accept/reject.

BEGIN;

-- Global rows have tenant_id = NULL.
INSERT INTO evidence_kind_schemas (
    id, tenant_id, kind, semver, major, minor, patch, schema_json,
    owner, default_scf_anchors, created_by
)
VALUES (
    '44444444-4444-4444-4444-444444440001',
    NULL,
    'demo.encryption_state.v1',
    '1.0.0',
    1, 0, 0,
    $json${
      "$schema": "https://json-schema.org/draft/2020-12/schema",
      "$id": "demo.encryption_state.v1",
      "type": "object",
      "required": ["bucket", "encrypted"],
      "additionalProperties": false,
      "properties": {
        "bucket": { "type": "string", "minLength": 1 },
        "encrypted": { "type": "boolean" }
      },
      "x-evidence-kind": "demo.encryption_state.v1"
    }$json$::jsonb,
    'platform',
    ARRAY['CRY-05'],
    'demo-operator@example.invalid'
)
ON CONFLICT DO NOTHING;

COMMIT;
