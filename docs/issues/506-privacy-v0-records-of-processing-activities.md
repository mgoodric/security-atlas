# 506 — Privacy v0: Records of Processing Activities (RoPA, GDPR Art. 30)

**Cluster:** Privacy
**Estimate:** M (2d)
**Type:** JUDGMENT (RoPA field model + lawful-basis taxonomy)
**Status:** `not-ready`

> **GATED on privacy-v0 greenlight.** Fires only once the privacy sibling module
> is greenlit (OQ #7: privacy v0 ships at v2+ when a real prospect surfaces
> demand). Slice 330's **AC-4** is a load-bearing finding: "If a Records of
> Processing Activities document [does not exist], file a follow-up slice."
> Slice 330 (P0-330-4) does not bundle it. **This is that follow-up slice.**

## Narrative

**WHY.** GDPR Art. 30 requires a controller to maintain a **Record of Processing
Activities** — a structured register of each processing purpose, its lawful
basis (Art. 6), the data categories and data-subject categories involved,
recipients, retention periods, and (where relevant) transfer safeguards. For a
self-host operator who is the controller (slice 330's controller/processor
finding), the platform should provide a first-class RoPA primitive rather than
forcing the operator into a spreadsheet — the exact anti-pattern the project
rejects elsewhere ("don't reach for a Google Sheet to fill a gap").

A RoPA differs from DSAR (slice 505, per-subject) and erasure (slice 504,
per-subject): RoPA is **org-level and per-processing-activity**. It is closer in
shape to the existing risk register or vendor register than to a subject-data
operation — a first-class CRUD primitive with a controlled vocabulary.

**WHAT this slice ships (once ungated).**

1. **`privacy.processing_activities` table** (`privacy.*` namespace): activity
   name, purpose, lawful basis (Art. 6 enum: `consent` | `contract` |
   `legal_obligation` | `vital_interests` | `public_task` | `legitimate_interests`),
   data categories (controlled tag set), data-subject categories, recipients,
   retention period, cross-border-transfer safeguard (nullable), owner role,
   created/updated metadata, status (`active` | `retired`).
2. **CRUD + list/filter handlers** under the privacy sibling API surface,
   admin-or-privacy-owner gated.
3. **Lawful-basis discipline.** Lawful basis is a required closed enum; a
   processing activity cannot be saved without one (slice 330's lawful-basis
   finding: "document explicitly" per purpose).
4. **RoPA export** — a structured (and human-readable Markdown) export the
   operator can attach to an audit or hand to a DPA, mirroring the existing
   audit-narrative export pattern.
5. **Five high-signal seed activities** (not a placeholder library — the same
   anti-pattern discipline as the 5 stock policies): the processing activities a
   typical self-host operator actually has (operator-account management, evidence
   ingestion, audit-trail logging, board-narrative generation, vendor-contact
   management), each with a worked lawful-basis and retention example. The seed
   loader enforces an exact count guard, mirroring `StockPolicyCount`.

**SCOPE DISCIPLINE — what's deliberately out.**

- **Per-subject operations** (DSAR / erasure) — slices 505 / 504.
- **A 50-entry "RoPA template library."** Rejected — same anti-pattern as the 50
  -placeholder policy library (CLAUDE.md). Five high-signal seeds, count-guarded.
- **AI-generated RoPA narrative text.** The seed activities are human-authored
  fixtures; the platform does not generate RoPA prose with an LLM (AI-assist
  boundary: no AI-authored compliance artifacts without human approval, and RoPA
  is not on the sanctioned AI-assist surface list).
- **DPIA (Art. 35) primitive.** Slice 180 lists DPIA among privacy primitives;
  it is a separate, larger primitive (risk-assessment-shaped) — not bundled here.
- **Automatic RoPA inference from connector activity.** Tempting, but the lawful
  -basis call is a human legal judgment; auto-inference would manufacture
  unverified legal claims. Operator authors each activity.

## Threat model (STRIDE)

RoPA is an **org-level CRUD register**, structurally similar to the risk/vendor
registers. Lower sensitivity than per-subject operations, but still
tenant-confidential governance data.

**S — Spoofing.** Write access must be controlled — a forged RoPA edit could
misrepresent the org's lawful-basis posture. **Mitigation:** create/update is
admin-or-privacy-owner gated; the writing identity is recorded.

**T — Tampering.** RoPA edits are audit-relevant governance changes.
**Mitigation:** updates write a `subject_module='privacy'` audit-log row capturing
the before/after of lawful-basis and retention (the load-bearing fields).

**R — Repudiation.** "Who changed the lawful basis and when?" must be answerable.
**Mitigation:** append-only audit trail on every write; status changes
(`active` -> `retired`) are logged, never hard-deleted.

**I — Information disclosure.** RoPA reveals the org's processing posture.
**Mitigation:** RLS tenant-scopes the table; the export inherits tenant scope; no
cross-tenant read is structurally reachable.

**D — Denial of service.** Bounded CRUD; not a high-volume surface. N/A beyond
standard rate limits.

**E — Elevation of privilege.** RoPA write is a governance action.
**Mitigation:** admin-or-privacy-owner only; ordinary users get read (or no)
access per the role matrix.

## Acceptance criteria

- [ ] **AC-1.** `privacy.processing_activities` migration is idempotent +
      reversible; `privacy.*` namespace; RLS-scoped to the owning tenant.
- [ ] **AC-2.** Lawful basis is a required closed Art. 6 enum; a save without one
      is rejected (integration test asserts the constraint).
- [ ] **AC-3.** CRUD + list/filter handlers are admin-or-privacy-owner gated
      (403 for under-privileged roles).
- [ ] **AC-4.** Every write produces a `subject_module='privacy'` append-only
      audit-log row capturing lawful-basis + retention before/after.
- [ ] **AC-5.** The RoPA export renders structured JSON + human-readable Markdown
      for an audit/DPA hand-off.
- [ ] **AC-6.** Exactly five high-signal seed activities load; the seed loader
      enforces an exact-count guard (mirroring `StockPolicyCount`); a directory
      with more/fewer is rejected.
- [ ] **AC-7.** No seed activity's narrative is AI-generated; all are
      human-authored fixtures (asserted by provenance attribution, not LLM call).

## Anti-criteria (P0 — block merge)

- **P0-506-1.** Does NOT ship a >5-entry placeholder RoPA library (anti-pattern).
- **P0-506-2.** Does NOT auto-infer lawful basis or generate RoPA prose via LLM.
- **P0-506-3.** Does NOT permit saving an activity without a lawful basis.
- **P0-506-4.** Does NOT begin before privacy-v0 greenlight.

## Dependencies

- **#180** (privacy-module foundation) — `merged`.
- **#330** (privacy GDPR/CCPA audit) — `merged`. AC-4 directs this follow-up.
- **#022 / #seed** (5 stock policies) — `merged`. The count-guard pattern this
  slice mirrors.
- **Privacy-v0 greenlight** — pending real-prospect demand (OQ #7). Hard gate.

## Canvas references

- `Plans/canvas/02-primitives.md` (register-shaped primitive pattern)
- `Plans/canvas/11-open-questions.md` #7 (privacy sibling-module resolution)
- `docs/issues/330-privacy-gdpr-ccpa-audit.md` AC-4 (RoPA finding)
- CLAUDE.md anti-patterns ("5 high-signal templates, not 50 placeholders")

## Constitutional invariants honored

- **#6** RLS tenant isolation — RoPA register is tenant-scoped.
- **AI-assist boundary** — RoPA prose is human-authored; no LLM-generated
  compliance artifact.
- **Anti-pattern (template libraries)** — five count-guarded seeds, not fifty.
