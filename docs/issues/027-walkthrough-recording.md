# 027 — Walkthrough recording: annotated capture + transcript + hash/sign

**Cluster:** Audit workflow
**Estimate:** 2d
**Type:** AFK

## Narrative

Implement the walkthrough recording primitive — an auditor or control owner records a narrative walkthrough of how a control operates, with optional annotated screen captures and transcript. Each walkthrough is hashed (sha256 of canonical form) and stored as an audit artifact with provenance. The walkthrough becomes part of the audit-export bundle (slice 030 OSCAL). The slice delivers value because auditors can stop demanding screenshots-via-email; they capture the walkthrough in-product with cryptographic integrity.

## Acceptance criteria

- [ ] AC-1: `POST /v1/walkthroughs` creates with: control_id, narrative (markdown), attachments[] (uploaded screenshots/files), transcript (optional)
- [ ] AC-2: Attachments stored in S3 (slice 036) with per-tenant prefix; image annotations stored as JSON metadata
- [ ] AC-3: Canonical hash computed over `{narrative, attachment_hashes[], transcript, control_id, created_at, created_by}`
- [ ] AC-4: Walkthrough record visible at `GET /v1/walkthroughs/:id`; auditor and the control's owner can read; auditor's testing notes are private
- [ ] AC-5: Walkthrough exportable as PDF + JSON (consumed by slice 030 OSCAL assessment-results)
- [ ] AC-6: Tamper detection: any modification to attachments invalidates the hash; surfaced at retrieval

## Constitutional invariants honored

- **Invariant 10 (audit-period freezing):** walkthroughs are pinned to an `AuditPeriod` from creation; immutable thereafter
- **AI-assist boundary:** AI may suggest narrative structure but never auto-publishes walkthrough content

## Canvas references

- `Plans/canvas/08-audit-workflow.md` §8.3 (walkthrough primitive)

## Dependencies

- #025, #036

## Anti-criteria (P0)

- Does NOT skip hashing or tamper detection
- Does NOT permit auto-generated walkthrough text without explicit user authorship
- Does NOT permit modification of a walkthrough after audit-period freeze

## Skill mix (3–5)

- Go + S3 multipart upload
- Canonical JSON hashing (sha256)
- Image annotation metadata schema
- PDF rendering
- Tamper detection patterns
