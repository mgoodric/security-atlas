# 505 — Privacy v0: data-subject-access-request (DSAR) export workflow

**Cluster:** Privacy
**Estimate:** M (2-3d)
**Type:** JUDGMENT (export-shape + subject-correlation policy)
**Status:** `not-ready`

> **GATED on privacy-v0 greenlight.** Like slice 504, this slice fires only once
> the privacy sibling module is greenlit (OQ #7: privacy v0 ships at v2+ when a
> real prospect surfaces demand). Slice 330's **AC-5** is a load-bearing finding:
> "If [there is no way to export all personal data the platform holds about] a
> user, file a follow-up slice for the workflow." Slice 330 (P0-330-4) does not
> bundle that workflow. **This is that follow-up slice.**

## Narrative

**WHY.** GDPR Art. 15 (right of access) and CCPA §1798.110 (right to know) give
a data subject the right to obtain a copy of all personal data an organization
holds about them. For a self-host operator (the controller, per slice 330's
controller/processor finding), security-atlas must let them produce a complete,
machine-readable export of one subject's personal data spanning every surface
where it lands: evidence actor fields, risk owners/contacts, vendor contacts,
audit-log actor records, OPA decision logs, board-narrative authorship, and
policy-acknowledgment attestations.

This is the **read** counterpart to slice 504's erasure (the **delete**). Both
descend from the same slice-330 audit and share the subject-correlation problem:
how to find every row belonging to one subject across the schema.

**WHAT this slice ships (once ungated).**

1. **Subject-correlation query.** A read-only traversal that, given an operator
   -supplied subject identifier, collects every personal-data-bearing row across
   the named surfaces. The surface allow-list is the same one slice 504 redacts
   (single source of truth: a shared `privacy.PersonalDataSurfaces` registry, so
   erasure and export can never drift on which fields count as personal data).
2. **DSAR export bundle.** A structured export (JSON, one section per surface,
   each row with provenance: table, row id, observed_at) the operator can hand to
   the data subject. Large bundles stream to S3-compatible object storage
   (consistent with evidence-artifact handling > 1 MB).
3. **DSAR request-tracking table** (`privacy.dsar_requests`): subject identifier,
   requested_at, requested_by, status (`requested` | `generating` | `delivered` |
   `refused`), delivered_at, bundle artifact pointer.
4. **Append-only DSAR audit trail.** Every export generation writes a
   `subject_module='privacy'` audit-log row (who exported what subject's data,
   when) — exporting personal data is itself a privacy-relevant action.
5. **Frozen-period transparency.** The export includes rows from frozen audit
   periods (a subject's right of access is not bounded by audit freezing) but
   annotates each with its freeze status so the operator understands the
   provenance.

**SCOPE DISCIPLINE — what's deliberately out.**

- **Erasure** — slice 504. This slice is read-only.
- **RoPA** (org-level processing inventory) — slice 506. DSAR is per-subject;
  RoPA is per-processing-activity. Different primitives.
- **Self-service subject portal.** The export is operator-run (the operator
  verifies subject identity out-of-band). No public subject-facing endpoint in v0
  (that surface area + its authn is a separate, larger design).
- **W3C DPV / DPV-JSON-LD export format.** Slice 180 records DPV as privacy-v0+
  work; the v0 export is plain structured JSON, not DPV-LD.
- **Automated free-text PII scanning** — the operator supplies the subject
  identifier; the export correlates on structured actor/owner/contact fields, not
  by scanning evidence bodies for incidental name mentions.

## Threat model (STRIDE)

A DSAR export **assembles all of one subject's personal data into a single
bundle** — a high-value disclosure target. The threat surface centers on
information disclosure and authorization.

**S — Spoofing.** A forged DSAR could exfiltrate a subject's data to an attacker.
**Mitigation:** DSAR generation is admin-gated; subject identity is operator
-verified out-of-band; the requesting operator's identity is recorded. The export
delivers to the operator (who hands it to the verified subject), not to a self
-supplied recipient address.

**T — Tampering.** Export is read-only; it cannot mutate the records it reads.
**Mitigation:** the correlation query uses read-only access; no UPDATE/DELETE on
the source surfaces.

**R — Repudiation.** Disclosing personal data is an accountable act.
**Mitigation:** every generation writes an append-only `subject_module='privacy'`
audit-log row recording operator + subject + timestamp + bundle pointer.

**I — Information disclosure (PRIMARY).** The bundle is a concentrated PII
payload; a cross-tenant correlation bug would be a severe breach. **Mitigation:**
the correlation query runs entirely inside the tenant RLS context (no
cross-tenant join is structurally reachable); the bundle artifact in object
storage is tenant-scoped and access-controlled identically to evidence artifacts;
the bundle is not retained indefinitely (operator-configurable TTL, default short)
so an old bundle is not a standing liability.

**D — Denial of service.** A subject with a large footprint could produce a
large bundle. **Mitigation:** generation is async (status `generating` ->
`delivered`); the bundle streams to object storage rather than buffering in
memory; admin-gated so not volume-abusable.

**E — Elevation of privilege.** DSAR generation is high-privilege.
**Mitigation:** admin-only; no ordinary-user surface reaches it.

## Acceptance criteria

- [ ] **AC-1.** `privacy.dsar_requests` migration is idempotent + reversible;
      `privacy.*` namespace; RLS-scoped to the owning tenant.
- [ ] **AC-2.** A `privacy.PersonalDataSurfaces` registry is the single source of
      truth for which fields count as personal data; slice 504's erasure consumes
      the same registry (integration test asserts export + erasure cover the same
      surface set — no drift).
- [ ] **AC-3.** The export bundle is complete across every registered surface for
      a seeded subject, each row carrying provenance (table, row id, observed_at)
      (integration test against seeded multi-surface data).
- [ ] **AC-4.** The correlation query is structurally confined to the tenant RLS
      context; an integration test in a two-tenant fixture asserts tenant A's
      DSAR never returns tenant B's rows.
- [ ] **AC-5.** Every generation writes a `subject_module='privacy'` append-only
      audit-log row.
- [ ] **AC-6.** Bundles > 1 MB stream to object storage; the request row stores
      an artifact pointer, not the inline payload.
- [ ] **AC-7.** DSAR generation is admin-only (403 for non-admin).

## Anti-criteria (P0 — block merge)

- **P0-505-1.** Does NOT expose a public/self-service subject endpoint in v0.
- **P0-505-2.** Does NOT correlate across tenants (RLS-confined).
- **P0-505-3.** Does NOT retain bundles indefinitely (TTL enforced).
- **P0-505-4.** Does NOT begin before privacy-v0 greenlight.
- **P0-505-5.** Export + erasure (slice 504) MUST share one personal-data-surface
  registry — no duplicate, drift-prone surface lists.

## Dependencies

- **#180** (privacy-module foundation) — `merged`.
- **#330** (privacy GDPR/CCPA audit) — `merged`. AC-5 directs this follow-up.
- **#504** (right-to-erasure) — sibling; shares the `PersonalDataSurfaces`
  registry. Whichever lands first owns creating the registry; the second consumes
  it.
- **Privacy-v0 greenlight** — pending real-prospect demand (OQ #7). Hard gate.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` (object-storage handling for > 1 MB)
- `Plans/canvas/11-open-questions.md` #7 (privacy sibling-module resolution)
- `docs/issues/330-privacy-gdpr-ccpa-audit.md` AC-5 (DSAR-export finding)

## Constitutional invariants honored

- **#6** RLS tenant isolation — correlation query is RLS-confined; the
  two-tenant test is the load-bearing assertion.
- **#2** append-only ledger — export is read-only; never mutates source rows.
- **AI-assist boundary** — N/A (no AI surface).
