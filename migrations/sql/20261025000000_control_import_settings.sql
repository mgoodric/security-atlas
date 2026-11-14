-- security-atlas — #2079: control import LLM rerank on local Ollama, opt-in.
--
-- The rerank of import candidates (#1781) calls an LLM only for a tenant
-- routed to a cloud provider (tenant_llm_routing). The local-ollama default
-- cannot tell an installed Ollama from a missing one, so it is never asked.
-- This table records the explicit per-tenant opt-in that lets a local-ollama
-- tenant have its import candidates reranked on its own Ollama.
--
-- Absence of a row means off: the migration leaves every tenant where it was.
-- A cloud-routed tenant is reranked regardless of this flag (the cloud opt-in
-- already allows it).
--
-- Mutable per-tenant config, so four-policy RLS under FORCE, the
-- tenant_llm_routing shape (invariant #6).

CREATE TABLE IF NOT EXISTS control_import_settings (
    tenant_id         UUID PRIMARY KEY,
    -- Rerank on the tenant's local Ollama when it is not routed to a cloud
    -- provider.
    llm_rerank_local  BOOLEAN NOT NULL DEFAULT false,
    updated_by        TEXT NOT NULL DEFAULT '',
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE control_import_settings IS
    '#2079 per-tenant control import settings. llm_rerank_local opts a '
    'local-ollama tenant in to the LLM rerank of import candidates. Absence '
    'of a row => off.';

ALTER TABLE control_import_settings ENABLE ROW LEVEL SECURITY;
ALTER TABLE control_import_settings FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_read ON control_import_settings;
CREATE POLICY tenant_read ON control_import_settings
    FOR SELECT
    USING (current_tenant_matches(tenant_id));
DROP POLICY IF EXISTS tenant_write ON control_import_settings;
CREATE POLICY tenant_write ON control_import_settings
    FOR INSERT
    WITH CHECK (current_tenant_matches(tenant_id));
DROP POLICY IF EXISTS tenant_update ON control_import_settings;
CREATE POLICY tenant_update ON control_import_settings
    FOR UPDATE
    USING (current_tenant_matches(tenant_id))
    WITH CHECK (current_tenant_matches(tenant_id));
DROP POLICY IF EXISTS tenant_delete ON control_import_settings;
CREATE POLICY tenant_delete ON control_import_settings
    FOR DELETE
    USING (current_tenant_matches(tenant_id));

GRANT SELECT, INSERT, UPDATE, DELETE ON control_import_settings TO atlas_app;
