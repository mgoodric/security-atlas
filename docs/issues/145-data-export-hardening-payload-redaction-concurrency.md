# 145 — Data-export hardening: payload_json redaction + per-tenant concurrency limit

**Cluster:** Backend / Multi-tenancy
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 during retro-STRIDE on slice 135 (data-export library, merged). Two marginal hardening gaps in the shipped export surface that aren't security bugs but are operationally meaningful:

1. **`payload_json` exported unredacted by default.** Slice 135's audit-log export includes the full `payload_json` column in CSV/JSON/XLSX outputs. Audit-log payloads contain user-supplied + system-generated PII (control titles, evidence kinds, before/after diffs). For forensics use cases this is correct; for "hand to a third-party auditor" use cases, operators want a redacted variant. Slice 138 (ledger-entities export, not-ready) already explicitly excludes `payload_json` for evidence exports — this slice makes the audit-log export's inclusion-by-default explicit AND adds an opt-out.

2. **Per-tenant export concurrency unlimited.** Slice 135 P0-A8 caps rows per export but does NOT cap concurrent exports per (tenant, user). A buggy client or attacker with valid session could fire 100 concurrent `/export` requests against the largest tenant — each streams for minutes, saturating the per-tenant pgxpool connections, degrading every other endpoint in that tenant. Slice 141 added rate-limiting for switches via `sessions.last_switched_at`; the same pattern applies here.

**What this slice ships:**

- `GET /v1/admin/audit-log/export` gains `?include_payload=<bool>` query param. Default: `true` (preserves slice 135 behavior; not a breaking change for existing operators). When `false`: encoder writes empty string for `payload_json` column / null for JSON / empty cell for XLSX.
- NEW `internal/export/concurrency.go` — package-level semaphore keyed by `(tenant_id, user_id)`. Max concurrent in-flight exports per key: 2. Excess returns 429 with `Retry-After: 30`. Configurable via env `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER` (default 2).
- Slice 138 (ledger-entities export, not-ready) and 136/137/139 inherit both behaviors via the slice-135 library's `Exporter` interface contract update.
- Documentation in CONTRIBUTING.md under "Data exports" section: forensics workflow uses default (include_payload=true); external-audit-handoff workflow uses include_payload=false.

**Scope discipline (what is OUT):**

- **Default-flip to `include_payload=false`** — out of scope; would break existing forensics use cases. Defer to a v3 follow-on if external-audit-handoff becomes the dominant workflow.
- **Column-level redaction beyond `payload_json`** — out of scope; `actor_id`, `target_id`, etc. stay unredacted (they're identifier-level, not content-level).
- **Per-export bandwidth throttling** — out of scope; concurrency cap is the load-bearing mitigation; bandwidth is a future tuning concern.
- **`?include_payload` for non-audit-log entities** — out of scope; slice 138 already documents its evidence-payload-exclusion policy.

## Threat model

Inherits slice 135's STRIDE. Additions:

| STRIDE                       | Threat                                                                                                                                                         | Mitigation                                                                                                                                                                                             |
| ---------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| **I** Information disclosure | Operator hands a slice-135 audit-log export to a third party for SOC 2 audit; export includes payload_json with internal-only data (e.g. evidence diffs).      | `?include_payload=false` query param redacts the column. Audit-log meta-audit row records the include_payload value used so the operator can prove which export went to which audience.                |
| **D** DoS                    | Authenticated misbehaving caller (script bug or malicious) fires N concurrent /export requests; per-tenant pgxpool saturates; other tenant endpoints degraded. | Per-(tenant, user) concurrency cap (semaphore). Excess returns 429 with Retry-After. Configurable via env. Slice-082 integration test fires 5 concurrent exports against cap=2 → 2 succeed, 3 get 429. |

## Acceptance criteria

- [ ] **AC-1:** `GET /v1/admin/audit-log/export` accepts `?include_payload=<bool>` query param; default `true`. Body validation: 400 on non-bool string.
- [ ] **AC-2:** CSV/JSON/XLSX encoders honor the flag: CSV emits empty cell; JSON emits explicit `null`; XLSX emits empty cell. Per-encoder unit tests.
- [ ] **AC-3:** Meta-audit row written on every export records `include_payload` value (slice 135 D8's `audit_log_export` action gets new payload key).
- [ ] **AC-4:** NEW `internal/export/concurrency.go` with per-(tenant_id, user_id) semaphore; max in-flight = `ATLAS_EXPORT_MAX_CONCURRENT_PER_USER` (default 2).
- [ ] **AC-5:** Concurrency cap exceeded → 429 with `Retry-After: 30` header + JSON body explaining the limit.
- [ ] **AC-6:** Slice-082 integration test: 5 concurrent exports against cap=2 → exactly 2 succeed, 3 get 429.
- [ ] **AC-7:** CONTRIBUTING.md "Data exports" subsection documents both forensics + external-audit-handoff workflows.
- [ ] **AC-8:** CHANGELOG entry under `[Unreleased] / Added`: "Audit-log export gains `?include_payload` flag for redacted-handoff workflow; per-(tenant, user) export concurrency cap (#145)".
- [ ] **AC-9:** Decisions log at `docs/audit-log/145-data-export-hardening-decisions.md` records: D1 default-direction choice (include_payload=true preserves backwards-compat, not false), D2 concurrency cap default (2 vs higher; chose 2 because the per-export streaming model means 2 in-flight is already meaningful pgxpool pressure).

## Constitutional invariants honored

Inherits slice 135. Adds: **operational transparency** — meta-audit captures the export's audience (via include_payload value) for the audit-log replay case.

## Dependencies

- **#135** Data-export library (merged) — extends.
- **#124** Unified audit-log aggregator (merged) — extends meta-audit payload shape.
- Note: depends on 135 only. Slices 136-139 (per-entity exports, not-ready) consume the library and inherit both behaviors transparently.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-HARDEN-1** Default behavior on `?include_payload` is `true` — preserves slice 135 wire shape for existing callers. NO breaking change.
- **P0-HARDEN-2** Concurrency cap is per-(tenant, user) — NOT global. A super_admin running concurrent exports across 5 tenants is NOT blocked by cap=2 in any single tenant.
- **P0-HARDEN-3** 429 response carries `Retry-After: 30` (slice 141 P0-DOS-1 pattern).
- **P0-HARDEN-4** Meta-audit row records `include_payload` value (operational transparency).
- **P0-HARDEN-5** NO vendor-prefixed test fixture tokens.

## Skill mix

- slice 135's `internal/export/` library (extend).
- Go integration tests + Playwright e2e (smoke-test on the audit-log page Export button with new query param).

## Notes for the implementing agent

Surfaced via retro-STRIDE on slice 135 during the 2026-05-18 `/idea-to-slice` session that filed slice 141. Slice 135's engineer covered the major STRIDE categories well; this slice closes the two marginal hardening gaps the original Security pass didn't catch (because the original Security pass was inline-self-grilled before the user pointed out the formal skill was actually installed).

The concurrency cap default of 2 is a JUDGMENT call (D2). Higher allows more parallel forensics work but loosens DoS. Engineer at pickup can record a different default if data shows 2 is too tight.

Provenance: filed 2026-05-18 from a retro-STRIDE finding during the slice 141 work.
