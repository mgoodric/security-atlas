# 036 — S3 artifact store integration (per-tenant prefixes + tenant-scoped credentials)

**Cluster:** Infra / deploy
**Estimate:** 1d
**Type:** AFK

## Narrative

Integrate an S3-compatible object store for large evidence payloads, walkthrough attachments, and exported audit bundles. Per-tenant prefix structure: `s3://atlas-artifacts/tenant-{id}/...`. Tenant-scoped credentials per request (STS / signed URL with short TTL). Records over 1MB redirect payload to S3; the evidence ledger holds the `payload_uri`. The slice delivers value because the platform can handle real-world evidence sizes (PDFs, large JSON exports, screenshot bundles) without bloating Postgres.

## Acceptance criteria

- [ ] AC-1: `POST /v1/artifacts:upload` accepts a multipart upload; returns a `payload_uri` keyed to the current tenant
- [ ] AC-2: `GET /v1/artifacts/:id` returns a short-TTL signed URL (max 1h) for download
- [ ] AC-3: Per-tenant prefix enforced; cross-tenant `GET` returns 404
- [ ] AC-4: Records with `payload > 1MB` automatically redirect to S3; `payload_uri` populated; ledger keeps metadata
- [ ] AC-5: Works against MinIO (local dev) and AWS S3 (prod) — credentials configurable
- [ ] AC-6: Audit log records every upload + download

## Constitutional invariants honored

- **Invariant 6 (RLS-analog at storage layer):** per-tenant prefixes; tenant-scoped credentials
- **Anti-pattern rejected (no proprietary agents):** uses standard S3 API, no custom agent

## Canvas references

- `Plans/canvas/09-tech-stack.md` §9.1 (S3-compatible object store)
- `Plans/canvas/05-scopes.md` §5.4 (storage tier per-tenant prefixes)

## Dependencies

- #013

## Anti-criteria (P0)

- Does NOT permit cross-tenant prefix access
- Does NOT issue signed URLs without TTL bound
- Does NOT skip audit log on uploads/downloads

## Skill mix (3–5)

- S3 SDK (AWS SDK v2 + MinIO compatibility)
- Multipart upload handling
- STS / pre-signed URL generation
- Go HTTP handlers
- Per-tenant key derivation
