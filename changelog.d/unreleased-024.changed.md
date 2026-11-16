- **users_idp_principal_unique:** relaxed from a global UNIQUE on
  `(idp_issuer, idp_subject)` to a per-tenant UNIQUE on `(tenant_id,
idp_issuer, idp_subject)` so the slice-192 multi-tenant identity
  design is no longer in latent conflict with the schema (slice 143
  D6).
