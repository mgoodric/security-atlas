# 139 — Audit periods + vendors data export (CSV / JSON / XLSX)

**Cluster:** Backend / Frontend
**Estimate:** 0.5-1d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135. The smallest remaining first-class entities. Each gets a data-export endpoint reusing the slice 135 library.

**What this slice ships:** 2 endpoints (`/v1/admin/audit-periods/export`, `/v1/admin/vendors/export`), 2 BFFs, 2 Export buttons, 2 canonical column sets, 2 cross-tenant isolation tests, 2 OPA matrix tests, 2 meta-audit actions (`audit_periods_export`, `vendors_export`).

**Scope discipline:** does NOT touch the OSCAL audit-period bundle export (slice 030); does NOT include vendor questionnaire responses in vendor exports (vendor QR is a separate per-vendor read endpoint with its own threat model).

## Threat model

Inherits slice 135. Per-entity addendums:

- **Audit periods**: frozen-period rows include `frozen_at` + `frozen_by` + `frozen_artifact_uri` (the cosigned bundle ref from slice 030). Column set includes these so an operator can audit the freeze trail. Does NOT include the actual cosigned bundle bytes (that's slice 030's surface — separate download endpoint).
- **Vendors**: vendor contact emails are PII; column set masks emails to `local-part@domain.tld` → `*@domain.tld` at v1 (defer un-masking to v3 column selection).

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1: `/v1/admin/audit-periods/export` reuses slice 135 library.
- [ ] AC-2: `/v1/admin/vendors/export` reuses slice 135 library.
- [ ] AC-3 + AC-4: BFF routes + Export buttons.
- [ ] AC-5 + AC-6: cross-tenant isolation tests.
- [ ] AC-7 + AC-8: OPA matrix tests.
- [ ] AC-9: D1 vendor-email-masking decision recorded in `docs/audit-log/139-audit-periods-vendors-export-decisions.md`.
- [ ] AC-10: Playwright e2e covering both Export buttons.
- [ ] AC-11: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135. Adds: **#10 audit-period freezing** — audit-periods export includes the freeze metadata columns (frozen_at, frozen_by, frozen_artifact_uri) so the freeze trail is legible offline.

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.4 — audit-period freezing; this slice exports the freeze metadata.
- `Plans/canvas/04-evidence-engine.md` — vendor primitive shape.

## Dependencies

- **#135** Data-export library. **Gate: 135 merged.**
- Slice 028 (audit-period freezing, merged) — read endpoint this exporter parallels.
- Slice 047 (vendor management, merged) — vendor read endpoint this exporter parallels.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-A-AP-1:** Audit-period export does NOT include cosigned-bundle bytes — that's slice 030's surface.
- **P0-A-V-1:** Vendor email masking at v1 — `*@domain.tld`. Un-masked column deferred to v3.

## Skill mix

- slice 135's `internal/export/` library.
- Go integration tests + Playwright e2e.

## Notes for the implementing agent

Smallest spillover of the four. The email-masking decision is the only per-entity nuance.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135.
