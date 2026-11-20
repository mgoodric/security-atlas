-- security-atlas -- #2618: Idempotency-Key on POST /v1/exceptions.
--
-- A client that times out after the server committed a request used to
-- file a duplicate on retry. A request carrying an Idempotency-Key now
-- records the key here, against the exception it created; a repeat of the
-- same key by the same person inside the dedupe window replays that
-- exception instead of filing another.
--
-- Its own table rather than columns on `exceptions`: the key is request
-- plumbing, not part of the exception, and it is forgotten after the
-- window (a later reuse of the key overwrites the row).
--
--   request_hash  sha256 of the canonical request body. The same key with
--                 a different body is refused (422), not replayed.
--   created_at    when the key was recorded; the window (24h, in
--                 internal/exception) is measured from it.

CREATE TABLE exception_idempotency_keys (
    tenant_id        UUID        NOT NULL,
    requested_by     TEXT        NOT NULL,
    idempotency_key  TEXT        NOT NULL,
    request_hash     TEXT        NOT NULL,
    exception_id     UUID        NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (tenant_id, requested_by, idempotency_key),
    CONSTRAINT exception_idempotency_keys_key_chk
        CHECK (char_length(idempotency_key) BETWEEN 1 AND 255),
    FOREIGN KEY (tenant_id, exception_id)
        REFERENCES exceptions(tenant_id, id) ON DELETE CASCADE
);

ALTER TABLE exception_idempotency_keys ENABLE ROW LEVEL SECURITY;
ALTER TABLE exception_idempotency_keys FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_read ON exception_idempotency_keys
    FOR SELECT USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON exception_idempotency_keys
    FOR INSERT WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON exception_idempotency_keys
    FOR UPDATE USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE ON exception_idempotency_keys TO atlas_app;

COMMENT ON TABLE exception_idempotency_keys IS
    'Idempotency-Key -> exception for POST /v1/exceptions (#2618). '
    'A repeat inside the window replays the recorded exception.';
