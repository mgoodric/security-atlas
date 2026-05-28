# Security-audit follow-up watchlist

Items surfaced by ad-hoc architecture / security reviews that do **not**
require a code change today but **do** warrant periodic re-audit at the
cadence noted. Read this file at the start of each scheduled security
audit (e.g. when filing the next `docs/audits/<YYYY>-Q<n>-security-audit.md`)
to fold these items into the audit's scope.

This file is a watchlist, not a tracker. When an item is meaningfully
resolved (the surface is removed, the BYPASSRLS path is retired, the
risk class changes), edit the entry to record the resolution rather than
deleting it — the audit trail is the point.

---

## Entry: `internal/platform/status.go` — `BootstrapTenantID` BYPASSRLS fallback

- **File:** `internal/platform/status.go`
- **Function:** `BootstrapTenantID(ctx context.Context) (uuid.UUID, error)`
- **Surface class:** BYPASSRLS query path (writePool / migrate pool)
- **Risk summary:** `BootstrapTenantID` runs against the BYPASSRLS migrate
  pool on the intentionally-unauthenticated `/v1/install-state` surface
  (slice 073 contract). Its primary lookup
  (`SELECT id FROM tenants WHERE is_bootstrap_tenant = TRUE LIMIT 1`)
  is the canonical post-slice-210 path. Its **fallback** lookup
  (`SELECT tenant_id FROM users ORDER BY created_at ASC LIMIT 1`) is a
  graceful-degradation path for pre-slice-210 installed instances —
  the bootstrap user's tenant_id is by construction the canonical
  bootstrap tenant for those installs, but the path bypasses RLS by
  design. The path itself is documented in a ~40-line public doc
  comment on the function in `internal/platform/status.go`; the public
  reasoning is intentional. The watchlist entry exists because BYPASSRLS
  query paths are the most security-sensitive class of code in the
  repo and warrant periodic re-confirmation that:
  1. the fallback is still graceful-degradation only, not a primary path
     anyone has accidentally regressed onto;
  2. the function is still the only BYPASSRLS pool consumer on the
     intentionally-unauthenticated surface;
  3. the install-state response still only surfaces the tenant_id and
     no other tenant-scoped state.
- **Audit cadence:** **annual**, aligned with the Q2 security-audit
  anniversary (next due 2027-Q2 per the cadence in
  `docs/audits/2026-Q2-security-audit.md`). Refine cadence per
  maintainer judgment if the surrounding install-state surface
  meaningfully changes (e.g. additional fields, additional callers, or
  retirement of the pre-slice-210-install fallback path).
- **Last reviewed:** 2026-05-27 via ad-hoc architecture review (run via
  `voltagent-qa-sec:architect-reviewer` against `main` at commit
  `7f58715b`). Reviewer's finding: BYPASSRLS path is documented and
  necessarily exists; no action today, but the class of code warrants
  the audit hook.
- **Context pointer:** slice
  [`docs/issues/324-evidence-sdk-docs-alignment.md`](../issues/324-evidence-sdk-docs-alignment.md)
  (which bundled this watchlist entry alongside the evidence-SDK docs
  reword).
