-- security-atlas -- #2714: sweep expired exception Idempotency-Keys.
--
-- exception_idempotency_keys (#2618) only overwrote a key when the same
-- requester reused it, so a key sent once stayed forever. The exception
-- package's IdempotencySweeper now deletes rows older than the 24h window,
-- one tenant at a time on the app role under that tenant's context. The
-- app role needs DELETE, bound to the tenant like the other policies, and
-- to keys already past the window: the app role can never delete a key
-- that could still replay. The 24 hours matches exception.IdempotencyWindow.

DO $$
BEGIN
    CREATE POLICY tenant_delete ON exception_idempotency_keys
        FOR DELETE USING (
            current_tenant_matches(tenant_id)
            AND created_at <= now() - interval '24 hours'
        );
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

GRANT DELETE ON exception_idempotency_keys TO atlas_app;
