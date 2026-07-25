-- security-atlas — slice 499: cloud-LLM opt-in per-tenant routing.
--
-- ----------------------------------------------------------------------------
-- WHY (the gap today).
--
-- Slice 498 shipped the local-Ollama-default inference substrate
-- (internal/llm): every tenant's AI-assist draft is generated locally, so no
-- tenant data leaves the deployment. Canvas §4.6.5 + the CLAUDE.md "AI-assist
-- boundary (hard) / inference backend" section commit to an OPTIONAL cloud
-- opt-in (Anthropic / OpenAI / Bedrock) that is **off by default**, **opt-in
-- per tenant** (not per deployment — one tenant may be on cloud while the rest
-- stay local), and disclosed by a **visible banner** wherever a draft appears.
--
-- This migration adds the per-tenant routing config that backs that opt-in:
-- `tenant_llm_routing`. One row per tenant records the active provider and (for
-- a cloud provider) the encrypted-at-rest provider API key. The ABSENCE of a
-- row means local-ollama (the default) — so the migration leaves NO tenant on
-- cloud (P0-499-1 / AC-2): there is no backfill, no default cloud row.
--
-- ----------------------------------------------------------------------------
-- DESIGN CALLS (JUDGMENT slice — full rationale in
-- docs/audit-log/499-cloud-llm-opt-in-decisions.md):
--
--   * CLOSED PROVIDER ENUM, no free-text URL (P0-499-3, threat-model T/I).
--     `provider` is constrained to ('local-ollama','anthropic','openai',
--     'bedrock'). There is intentionally NO endpoint/base-url column: the
--     cloud endpoint is the provider's official API, hard-coded in the Go
--     adapter, never operator-supplied. An operator-supplied URL would turn
--     the opt-in into an SSRF / exfiltration primitive. A new provider
--     requires a follow-on migration extending this CHECK + a new Go adapter.
--
--   * KEY ENCRYPTED AT REST, never plaintext in the row (P0-499-4, AC-3/AC-11).
--     `api_key_ciphertext` holds the AES-256-GCM ciphertext (nonce-prefixed)
--     of the provider API key, encrypted by internal/llm/cloud's crypter using
--     a deployment master key (the keystore-style "key material from a 0600
--     file / env, never in the DB" pattern). The plaintext key is NEVER
--     stored, NEVER returned (write-only / masked at the API), NEVER logged.
--     local-ollama rows carry no key (ciphertext is NULL).
--
--   * INVARIANT: a cloud provider REQUIRES a key; local-ollama FORBIDS one.
--     The CHECK `tenant_llm_routing_key_presence` ties key presence to
--     provider so a cloud row can never be keyless (a silent mis-route) and a
--     local row can never carry a dangling secret.
--
--   * Mutable config (set / replace / clear) => four-policy RLS under FORCE
--     (P0-499-5, AC-1). Unlike the append-only ai_generations ledger, the
--     routing config is edited in place (an admin switches provider, rotates
--     key, reverts to local), so it carries SELECT + INSERT + UPDATE + DELETE
--     policies, each gated on current_tenant_matches(tenant_id). FORCE ROW
--     LEVEL SECURITY so even a table-owner connection is RLS-bound. The
--     cross-tenant isolation proof (AC-10) runs through the NOBYPASSRLS
--     atlas_app role against these policies.
--
-- ----------------------------------------------------------------------------
-- Constitutional invariants honored:
--
--   AI-assist boundary (hard) / inference backend: cloud is OFF BY DEFAULT
--     (no row => local-ollama), OPT-IN PER TENANT (a per-tenant row, set by a
--     tenant-admin action), with the provider recorded so the banner +
--     ai_generations.model_provider audit can name it. The approval gate is
--     untouched — this table records WHERE a draft is generated, never WHETHER
--     it can be published without human approval.
--   #6  Tenant isolation at the DB layer — four-policy RLS on
--     app.current_tenant under FORCE; cross-tenant config/key reads denied by
--     the DB (P0-499-5, AC-10).
--   #2  Ingestion/evaluation separation — this is a config table; it never
--     writes the evidence ledger.
--
-- Idempotency / reversibility: paired
-- 20260612100000_tenant_llm_routing.down.sql drops the table (CASCADE removes
-- its policies + indexes) for a clean up->down->up round-trip. No enum TYPE is
-- created (the provider set is a CHECK constraint, matching the
-- ai_generations_surface_chk precedent).
-- ----------------------------------------------------------------------------

CREATE TABLE tenant_llm_routing (
    -- One routing config per tenant. tenant_id is the PRIMARY KEY: a tenant
    -- has exactly zero (=> local-ollama default) or one routing row.
    tenant_id           UUID PRIMARY KEY,

    -- The active inference provider. CLOSED ENUM (P0-499-3) — no free-text
    -- URL. local-ollama is the explicit on-row default; the typical cloud
    -- opt-in row carries anthropic / openai / bedrock.
    provider            TEXT NOT NULL DEFAULT 'local-ollama',

    -- AES-256-GCM ciphertext (nonce||ciphertext, base64-encoded by the Go
    -- crypter) of the provider API key. NULL for local-ollama (no key). The
    -- PLAINTEXT key is never stored here and never leaves the deployment in a
    -- log or an API response (P0-499-4).
    api_key_ciphertext  TEXT NULL,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Closed provider enum (P0-499-3). Adding a provider requires a follow-on
    -- migration extending this list AND a new Go adapter in internal/llm/cloud.
    CONSTRAINT tenant_llm_routing_provider_chk
        CHECK (provider IN (
            'local-ollama',
            'anthropic',
            'openai',
            'bedrock'
        )),

    -- A cloud provider REQUIRES a key; local-ollama FORBIDS one. Prevents a
    -- keyless cloud row (silent mis-route) and a dangling secret on a local
    -- row. length() guard rejects an empty-string ciphertext masquerading as
    -- present (confused-deputy hardening, same shape as slice 173/498).
    CONSTRAINT tenant_llm_routing_key_presence
        CHECK (
            (provider = 'local-ollama' AND api_key_ciphertext IS NULL)
            OR (provider <> 'local-ollama'
                AND api_key_ciphertext IS NOT NULL
                AND length(api_key_ciphertext) > 0)
        )
);

COMMENT ON TABLE tenant_llm_routing IS
    'Slice 499 per-tenant cloud-LLM opt-in routing. Absence of a row => '
    'local-ollama (the off-by-default posture). provider is a closed enum '
    '(no free-text URL). api_key_ciphertext is AES-256-GCM, never plaintext, '
    'never returned/logged.';

-- ===== Row-Level Security (four-policy, FORCE) =====
--
-- Mutable per-tenant config: SELECT + INSERT + UPDATE + DELETE policies, each
-- gated on current_tenant_matches(tenant_id). FORCE so the table owner is also
-- RLS-bound. atlas_app gets all four DML grants. The cross-tenant isolation
-- proof (AC-10) runs through the NOBYPASSRLS atlas_app role against these.
ALTER TABLE tenant_llm_routing ENABLE ROW LEVEL SECURITY;
ALTER TABLE tenant_llm_routing FORCE ROW LEVEL SECURITY;

CREATE POLICY tenant_read ON tenant_llm_routing
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
CREATE POLICY tenant_write ON tenant_llm_routing
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_update ON tenant_llm_routing
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
CREATE POLICY tenant_delete ON tenant_llm_routing
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON tenant_llm_routing TO atlas_app;
