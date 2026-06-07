-- Down migration for slice 464: revoke the read grant added for the
-- cross-tenant `atlas evidence verify` walk.

REVOKE SELECT ON evidence_records FROM atlas_service_account;
