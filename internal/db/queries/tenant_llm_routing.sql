-- tenant_llm_routing — slice 499 per-tenant cloud-LLM opt-in routing config.
--
-- One row per tenant. Absence of a row => local-ollama (the off-by-default
-- posture). Every query is tenant-scoped via the leading tenant_id predicate;
-- four-policy RLS under FORCE keeps the cross-tenant boundary safe even on a
-- misconfigured query (P0-499-5). The provider API key is stored ONLY as
-- AES-256-GCM ciphertext (api_key_ciphertext); the plaintext is bound as a
-- parameter by the encrypting store and never appears in a query, a log, or an
-- API response (P0-499-4).

-- name: GetTenantLLMRouting :one
-- Fetch the current routing config for the tenant. Returns no rows when the
-- tenant has never opted in (=> the router treats that as local-ollama).
SELECT *
FROM tenant_llm_routing
WHERE tenant_id = $1;

-- name: UpsertTenantLLMRouting :one
-- Set or replace the tenant's routing config (the tenant-admin "switch
-- provider / rotate key" action). The closed-enum provider CHECK and the
-- key-presence CHECK are enforced at the DB; the ciphertext is a bound
-- parameter (never interpolated). updated_at is bumped on every write
-- (app-layer now(), no trigger — matches the repo convention).
INSERT INTO tenant_llm_routing (
    tenant_id,
    provider,
    api_key_ciphertext,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, now(), now())
ON CONFLICT (tenant_id) DO UPDATE
SET provider           = EXCLUDED.provider,
    api_key_ciphertext = EXCLUDED.api_key_ciphertext,
    updated_at         = now()
RETURNING *;

-- name: DeleteTenantLLMRouting :execrows
-- Clear the tenant's routing config (revert to the local-ollama default). The
-- key ciphertext is removed with the row. Returns rows affected so the caller
-- can distinguish "cleared" from "was already absent".
DELETE FROM tenant_llm_routing
WHERE tenant_id = $1;
