# 049 — Manual upload / CSV / S3 / SFTP escape-hatch connector

**Cluster:** Connectors
**Estimate:** 1d
**Type:** AFK

## Narrative

Implement the universal escape-hatch ingestion connector at `connectors/manual-upload/`. Three intake paths: (1) UI upload (CSV / JSON / PDF) by an authenticated user, (2) cron-watcher over a configured S3 prefix that ingests new files, (3) SFTP-pulled files. Each becomes a `manual.upload.v1` evidence record. Each upload requires the user to tag it with: target `control_id`, `scope`, `observed_at`, narrative. Idempotency key derived from file hash + control_id + observed_at. Distinct from slice 011 (manual control attestation): that flow is for control owners attesting against a schema; this connector is for arbitrary file evidence (audit artifacts, screenshots, third-party reports, certs). The slice delivers value because any evidence — even unstructured — can land in the ledger with full provenance, completing the "no control is left without an evidence path" goal.

## Acceptance criteria

- [ ] AC-1: UI upload form accepts file + control_id + scope + observed_at + narrative; produces evidence record
- [ ] AC-2: Cron-watcher polls configured S3 prefix; new files become evidence records with full provenance
- [ ] AC-3: SFTP pull configurable; supports key auth
- [ ] AC-4: Idempotency: same file hash + control + observed_at returns the original receipt
- [ ] AC-5: Records tagged with `provenance.actor_type=human` (UI) or `actor_type=service_account` (S3/SFTP)
- [ ] AC-6: Files >1MB redirect to S3 per slice 036

## Constitutional invariants honored

- **Invariant 9 (manual evidence first-class):** unstructured evidence has the same provenance + ledger discipline as automated
- **Anti-pattern rejected (no proprietary agents):** uses S3 / SFTP / browser — universal paths

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.2 (CSV / S3 / SFTP / Manual upload in v1 roster)

## Dependencies

- #003, #013

## Anti-criteria (P0)

- Does NOT permit upload without authenticated user + control tag
- Does NOT skip idempotency (re-upload of same file must not duplicate)
- Does NOT permit upload without scope tagging

## Skill mix (3–5)

- Go HTTP multipart handler
- S3 polling watcher
- SFTP client
- File hash + dedup
- Idempotency from content hash
