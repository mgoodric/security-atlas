-- Reverse of 20260612100000_tenant_llm_routing.sql. Drops the per-tenant
-- routing config table; CASCADE removes its four RLS policies. Leaves the
-- slice-498 inference substrate byte-identical (this slice never altered
-- ai_generations or any slice-498 object). No enum TYPE was created (the
-- provider set is a CHECK constraint), so nothing else to drop.

DROP TABLE IF EXISTS tenant_llm_routing CASCADE;
