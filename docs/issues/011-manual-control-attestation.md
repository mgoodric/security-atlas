# 011 — Manual control type + attestation/upload flow

**Cluster:** Control-as-code
**Estimate:** 1.5d
**Type:** AFK

## Narrative

Implement the runtime path for `manual_periodic` and `manual_attested` controls. When a manual control is due (per its `freshness_class`), the owner is notified and presented with the control's `attestation_schema` form. On submit, the form data + any uploaded artifact (PDF, screenshot, signed file) is hashed, stored in S3 (slice 036), and recorded as an evidence record in the ledger via the same push path used by automated connectors. The slice delivers value because manual controls operate with the same lifecycle and dashboard treatment as automated controls — fulfilling invariant 9.

## Acceptance criteria

- [ ] AC-1: Frontend renders a form derived from the control's `attestation_schema` when a manual control is due
- [ ] AC-2: Submitting the form (with optional file attachment) creates an evidence record with `provenance.actor_type=human`, `actor_id=<userId>`, `evidence_kind=manual.attestation.v1`
- [ ] AC-3: Uploaded artifacts (≤10MB) land in the S3 artifact store with a tenant-scoped prefix
- [ ] AC-4: Manual control with successful attestation flips control state to `pass`; evaluation engine respects the freshness window
- [ ] AC-5: Attempted attestation by a user without the control's `owner_role` is rejected
- [ ] AC-6: Audit log records who attested + when

## Constitutional invariants honored

- **Invariant 9 (manual evidence first-class):** same evidence schema, same ledger path, same dashboard treatment
- **Invariant 2 (ingestion/eval separated):** attestation pushes to ledger; evaluation consumes (does not write)
- **AI-assist boundary:** AI does NOT auto-attest manual controls under any circumstance

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.5 (manual evidence first-class)
- `Plans/canvas/02-primitives.md` §2.1 (implementation_type enum)

## Dependencies

- #009, #013, #036

## Anti-criteria (P0)

- Does NOT permit attestation without authenticated user + role check
- Does NOT auto-attest under any AI flow
- Does NOT store artifacts outside the tenant's S3 prefix
- Does NOT skip the hash + provenance trail

## Skill mix (3–5)

- Next.js + dynamic form generation from JSON Schema
- Go upload handler with multipart parsing
- S3 client with per-tenant prefixes
- Role-based access control hook
- Evidence ledger client (push path from slice 013)
