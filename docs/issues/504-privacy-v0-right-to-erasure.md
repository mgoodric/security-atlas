# 504 — Privacy v0: right-to-erasure (tombstone) implementation against the append-only ledger

**Cluster:** Privacy
**Estimate:** L (3-4d)
**Type:** JUDGMENT (erasure-vs-ledger-invariant reconciliation; tombstone shape)
**Status:** `not-ready`

> **GATED — downstream of two prior commitments.** This slice is **not-ready**
> for two reasons, both of which must clear before it can move to `ready`:
>
> 1. **Privacy-v0 ship timing.** OQ #7 (resolved 2026-05-20) committed that the
>    privacy sibling module ships at **v2+ when a real prospect surfaces demand**.
>    Slice 180 landed the foundation (audit-log `subject_module`, feature-flag
>    module-toggling, sibling-discipline doc) but explicitly deferred the privacy
>    primitives. This slice fires only once privacy-v0 is greenlit.
> 2. **The erasure design decision.** Slice 330 (GDPR/CCPA audit, merged)
>    AC-3 is a **load-bearing finding: right-to-erasure design** — "tombstone /
>    pseudonymize / refuse-with-explanation are all valid designs, but ONE of
>    them must be the documented design." Slice 330 explicitly **does not bundle**
>    that decision (P0-330-4) and directs a follow-up slice. **This is that
>    follow-up slice.** It assumes the tombstone design (the canvas-aligned
>    choice — see Narrative) but must not start coding until slice 330's AC-3
>    design is ratified in an ADR.

## Narrative

**WHY.** GDPR Art. 17 (right to erasure) and CCPA §1798.105 (right to deletion)
give a data subject the right to have their personal data deleted. The platform's
constitutional invariant #2 makes the evidence ledger **append-only** — "Bugs in
evaluation never corrupt the record. Point-in-time replay is always possible."
And audit-period freezing (canvas §8.4) requires frozen sample populations to
remain stable. A naive `DELETE` would violate both invariants and corrupt the
audit chain.

These two obligations genuinely conflict, and slice 330's audit surfaced exactly
this tension. The reconciliation the canvas already implies — and the design this
slice implements — is the **tombstone**: the erasure request does not delete the
ledger row; it **redacts the personal-data fields in place** (replacing them with
a stable, non-reversible tombstone marker) while preserving the row's identity,
content-hash chain position, `observed_at`, and structural metadata. The audit
chain stays intact and replayable; the personal data is gone.

**WHAT this slice ships (once ungated).**

1. **`privacy.erasure_requests` table** (in the `privacy.*` sibling namespace
   that privacy-v0 creates): subject identifier, requested_at, requested_by,
   lawful-basis-for-refusal (nullable), status (`requested` | `in-progress` |
   `completed` | `refused`), completed_at, operator_note.
2. **Tombstone redaction operation.** A transactional sweep that, for a confirmed
   erasure request, locates all rows across the personal-data-bearing surfaces
   (evidence actor fields, risk owner contacts, vendor contacts, audit-log actor
   fields, board-narrative author fields) matching the subject and overwrites the
   personal-data columns with a tombstone sentinel — **without** deleting the row,
   altering its content-hash position, or changing `observed_at`.
3. **Frozen-period guard.** Rows whose `observed_at <= frozen_at` of any active
   AuditPeriod are **redacted but flagged** — the redaction is applied to the
   live field, but a `redacted_under_freeze` marker records that the value existed
   at freeze time. (The auditor sees "value redacted per erasure request ER-NNN"
   rather than a phantom-missing field — legible, not silent.)
4. **Append-only erasure audit trail.** Every redaction writes an audit-log row
   (`subject_module='privacy'`) recording the erasure-request id, the surfaces
   touched, and the operator who confirmed — so the erasure itself is auditable.
5. **Refuse-with-explanation path.** Where a lawful basis to retain overrides the
   erasure right (e.g. a legal-hold obligation), the request is marked `refused`
   with a mandatory documented basis — never silently dropped.

**SCOPE DISCIPLINE — what's deliberately out.**

- **The DSAR export workflow** — that is slice 505 (a sibling follow-up of the
  same slice-330 audit). This slice is deletion only.
- **The RoPA primitive** — slice 506.
- **Pseudonymization-instead-of-tombstone.** Rejected as the design unless slice
  330's AC-3 ADR overrides; tombstone is canvas-aligned (invariant #2 preserved).
- **Cross-tenant erasure / hosted-offering processor obligations** — the
  self-host operator is the controller (slice 330 controller/processor finding);
  hosted-offering erasure mechanics wait on OQ #03 (hosted offering decision).
- **Automated subject-discovery.** The operator supplies the subject identifier;
  the platform does not crawl free-text evidence bodies for PII matches in v0.

## Threat model (STRIDE)

Erasure is a **destructive, high-privilege, legally-load-bearing** operation
against the most sensitive records in the platform. The threat surface is
substantial.

**S — Spoofing.** An attacker forging an erasure request could weaponize the
right-to-erasure into a data-destruction attack ("erase the CISO's audit trail").
**Mitigation:** erasure-confirm is an admin-only action gated on
`cred.IsAdmin`; the requesting subject identity is operator-verified out-of-band
(the platform records who confirmed, not a self-service subject submission); the
confirm action is logged with the confirming operator's identity.

**T — Tampering (PRIMARY).** The redaction must not be usable to silently alter
audit history beyond the personal-data fields. **Mitigation:** the redaction
operation touches only an allow-listed set of personal-data columns; the content
-hash chain position, `observed_at`, control/risk linkage, and structural
metadata are immutable to the redactor (enforced by the SQL — the UPDATE
statement names only the allow-listed columns). A redaction that would alter a
frozen-period sample population is rejected (frozen-period guard, item 3).

**R — Repudiation.** A missed or mis-applied erasure is a compliance liability.
**Mitigation:** every redaction writes an append-only `subject_module='privacy'`
audit-log row (four-policy RLS, slice 036 pattern); refusals record a mandatory
documented lawful basis. The erasure-request lifecycle is itself an audit trail.

**I — Information disclosure.** The tombstone sentinel must not leak the original
value (no reversible encoding, no hash-of-PII that enables re-identification).
**Mitigation:** the sentinel is a fixed non-reversible marker (`[redacted:ER-NNN]`),
not a hash or cipher of the original. RLS confines the erasure-request table to
the owning tenant.

**D — Denial of service.** A wide erasure sweep across all personal-data surfaces
could lock many rows. **Mitigation:** the sweep batches per surface within a
bounded transaction; large subjects are processed in chunks with progress in the
request status. Not user-triggerable at volume (admin-gated, one subject per
request).

**E — Elevation of privilege.** Erasure-confirm is the highest-privilege privacy
action. **Mitigation:** admin-only; no role below admin can confirm or refuse;
the action cannot be reached via any ordinary-user surface.

## Acceptance criteria

- [ ] **AC-1.** `privacy.erasure_requests` migration is idempotent + reversible;
      lives in the `privacy.*` namespace; RLS-scoped to the owning tenant.
- [ ] **AC-2.** The tombstone redaction overwrites only the allow-listed
      personal-data columns and leaves content-hash position, `observed_at`, and
      linkage immutable (integration test asserts the chain still replays).
- [ ] **AC-3.** A redaction touching a frozen-period sample population is flagged
      `redacted_under_freeze` and the auditor surface shows "redacted per ER-NNN"
      rather than a phantom-missing field (integration test against a frozen
      AuditPeriod).
- [ ] **AC-4.** Every redaction writes a `subject_module='privacy'` append-only
      audit-log row; refusals record a mandatory lawful basis (no silent drop).
- [ ] **AC-5.** Erasure-confirm + refuse are admin-only (403 for non-admin).
- [ ] **AC-6.** The tombstone sentinel is non-reversible (no hash/cipher of the
      original value); unit test asserts the sentinel shape.
- [ ] **AC-7.** Tombstone is the implemented design per slice 330 AC-3's ratified
      ADR; if the ADR selects a different design, this slice's spec is updated
      before coding (grill-with-docs gate).

## Anti-criteria (P0 — block merge)

- **P0-504-1.** Does NOT `DELETE` any ledger row or alter content-hash chain
  position (violates invariant #2).
- **P0-504-2.** Does NOT silently drop an erasure request — every request ends
  `completed` or `refused` with documented basis.
- **P0-504-3.** Does NOT expose erasure-confirm to any non-admin role.
- **P0-504-4.** Does NOT begin before privacy-v0 is greenlit AND slice 330 AC-3's
  erasure-design ADR is ratified.

## Dependencies

- **#180** (privacy-module foundation) — `merged`. Provides `subject_module` +
  sibling discipline.
- **#330** (privacy GDPR/CCPA audit) — `merged`. AC-3 mandates the erasure-design
  ADR this slice implements; P0-330-4 directs this follow-up.
- **Privacy-v0 greenlight** — pending real-prospect demand (OQ #7). Hard gate.
- **Invariant #2** (append-only ledger) + **canvas §8.4** (audit-period freezing)
  — the constraints the tombstone design reconciles.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.3 (append-only ledger, invariant #2)
- `Plans/canvas/08-audit-workflow.md` §8.4 (audit-period freezing)
- `Plans/canvas/11-open-questions.md` #7 (privacy sibling-module resolution)
- `docs/issues/330-privacy-gdpr-ccpa-audit.md` AC-3 (erasure-design finding)

## Constitutional invariants honored

- **#2** append-only evidence ledger — tombstone redacts in place, never deletes.
- **#6** RLS tenant isolation — erasure-request table + redaction sweep are
  tenant-scoped.
- **AI-assist boundary** — N/A (no AI surface).
- **Audit-period freezing (§8.4)** — frozen populations preserved via the
  freeze guard.
