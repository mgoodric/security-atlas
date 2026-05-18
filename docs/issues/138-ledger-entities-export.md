# 138 — Ledger entities export (evidence + policies + exceptions + samples)

**Cluster:** Backend / Frontend
**Estimate:** 1-2d
**Type:** AFK
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135. These four entities share shape: tenant-scoped row tables with audit-log siblings. This slice wires each into the slice 135 library with one endpoint per entity (`/v1/admin/evidence/export`, `/v1/admin/policies/export`, `/v1/admin/exceptions/export`, `/v1/admin/samples/export`) + corresponding BFF routes + Export buttons on each entity's list page.

**What this slice ships:** 4 endpoints, 4 BFFs, 4 Export buttons, 4 canonical column sets, 4 cross-tenant isolation tests, 4 OPA matrix tests, 4 meta-audit actions (`evidence_export`, `policies_export`, `exceptions_export`, `samples_export`).

**Scope discipline:** does NOT extend to evidence-pipeline raw artifact downloads (separate concern — slice 036 owns artifact storage); does NOT extend to policy PDF export (slice 042 area; existing); does NOT extend to sample population queries (separate read endpoint).

## Threat model

Inherits slice 135. Per-entity addendums:

- **Evidence**: payload_json may include vendor secrets (e.g. an AWS S3 evidence record's bucket-policy JSON may leak operational metadata). Column set EXCLUDES `payload_json` at v1; only includes the structured columns (kind, observed_at, source, content_hash, etc.).
- **Policies**: policy body text is large; column set INCLUDES policy body for searchability (operators need this for audit prep). RLS enforcement is the only mitigation here — and slice 135 P0-A5 provides it.
- **Exceptions**: exception justification + reviewer notes are sensitive; column set includes them but inherits slice 135 RLS.
- **Samples**: sample populations may be large; row cap lifted to 250,000 (between slice 135 default and slice 137's 500K).

## Acceptance criteria (stub — expand at pickup)

- [ ] AC-1 through AC-4: one endpoint per entity (`evidence`, `policies`, `exceptions`, `samples`).
- [ ] AC-5 through AC-8: one BFF + Export button per entity.
- [ ] AC-9 through AC-12: cross-tenant isolation test per entity.
- [ ] AC-13 through AC-16: OPA matrix test per entity (verifying admit set parity with each entity's read endpoint).
- [ ] AC-17: D1-D4 column-set decisions recorded per entity in `docs/audit-log/138-ledger-entities-export-decisions.md`.
- [ ] AC-18: Streaming-memory test for the 250K-row samples cap.
- [ ] AC-19: Playwright e2e covering all 4 Export buttons.
- [ ] AC-20: CHANGELOG entry.

## Constitutional invariants honored

Inherits slice 135. Adds: **#2 ingestion + evaluation separated** — exports include the evaluation-stage `evaluation_id` reference so downstream consumers can correlate; does NOT export evidence ledger raw bytes (artifact downloads belong to slice 036).

## Canvas references

- `Plans/canvas/02-primitives.md` — evidence/policy/exception/sample primitive shapes.
- `Plans/canvas/04-evidence-engine.md` — evidence shape; D1 column-set decision references this for evidence specifically.

## Dependencies

- **#135** Data-export library. **Gate: 135 merged.**
- Slices 011 (manual control attestation), 041 (policies), 022 (exceptions), 029 (audit hub / samples) — the read endpoints this slice's exporters parallel.

## Anti-criteria (P0 — block merge)

- Inherits slice 135 P0-A1 through P0-A14.
- **P0-A-Ledger-1:** Evidence export column set EXCLUDES `payload_json` at v1 (operational-metadata leak vector); v3 follow-on for column selection.
- **P0-A-Ledger-2:** Samples row cap 250,000; NOT removable.
- **P0-A-Ledger-3:** Does NOT export evidence ledger raw artifact bytes — that's slice 036's surface. Export is metadata only.

## Skill mix

- slice 135's `internal/export/` library.
- Go integration tests + Playwright e2e.

## Notes for the implementing agent

Four entities, same library. Per-entity work is ~3-4 hours each; expect ~1.5d total wall-clock.

The evidence `payload_json` exclusion (P0-A-Ledger-1) is the load-bearing per-entity decision. Operators who need payload introspection use the evidence-detail page (which has RLS-protected read), NOT bulk export.

Provenance: filed 2026-05-18 via `/idea-to-slice` as a sibling spillover of slice 135.
