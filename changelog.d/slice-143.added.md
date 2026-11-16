- **admin:** slice 143 — create-tenant flow (super_admin-gated). Adds
  `POST /v1/admin/tenants` + `GET /v1/admin/tenants` + the matching
  `/admin/tenants` management page. Seeds each new tenant with one
  builtin `environment` scope dimension and a default `All` scope
  cell. Soft rate-limit: 100 tenants per super_admin per rolling 24h
  window. Dual-writes `super_admin_audit_log` + `me_audit_log`
  (mirroring the slice-142 pattern). Tenant deletion remains out of
  scope.
