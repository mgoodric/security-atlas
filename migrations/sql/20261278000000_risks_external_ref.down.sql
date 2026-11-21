-- Reverse #2838. Dropping the column drops its CHECK and the index on it.

DROP INDEX IF EXISTS risks_tenant_external_ref_key;

ALTER TABLE risks
    DROP COLUMN IF EXISTS external_ref;
