# 155 — Questionnaire feature: design + build (CAIQ / SIG / HECVAT response collection)

**Cluster:** Backend / Frontend / Multi-tenancy
**Estimate:** 3-5d (large; design-heavy)
**Type:** JUDGMENT
**Status:** `not-ready`

## Narrative

Surfaced 2026-05-18 from the comprehensive front-end-to-back-end gap audit. The dashboard mockup (`Plans/mockups/dashboard.html` line 491) references "Acme Corp CAIQ response due" indicating a questionnaire-response surface. Canvas §4 (Evidence Engine) §4.6 describes questionnaire ingestion + AI-assisted answer suggestion as a v1 feature.

**Current state:**

- `Plans/mockups/questionnaire.html` — referenced in mockup directory but NOT delivered as an actual mockup file
- No frontend page at `web/app/(authed)/questionnaire/` (or similar)
- No backend endpoints
- No slice exists

This is genuinely new work — not a fix or gap-fill but a substantial feature build. The canvas already commits to:

- Universal questionnaire import (Excel / CSV / JSON / Word) — canvas §4.6.1
- HECVAT bundled; CAIQ + SIG ingest of customer-provided files (licensing-bounded) — canvas §4.6.2
- Manual answer authoring with cited evidence
- AnswerLibrary for canonical SCF-anchored answers
- PDF export — canvas §4.6.3
- No AI-assist at v1; opt-in in v2 — canvas §4.6 + CLAUDE.md AI-assist boundary

**Scope discipline (what is OUT for v1):**

- AI-assist (v2)
- CAIQ + SIG TEMPLATES bundling (licensing-bounded; customer brings file)
- Cross-tenant questionnaire sharing
- Vendor-facing portal (where the requesting vendor submits)

**What this slice ships (v1 scope):**

- Design phase: deliver the missing `Plans/mockups/questionnaire.html` mockup
- Backend: `internal/api/questionnaires/` package with CRUD + import/export endpoints
- Frontend: `web/app/(authed)/questionnaires/` route + detail / response views
- AnswerLibrary: SCF-anchored canonical answers reusable across questionnaires
- Manual answer authoring with citations to evidence records
- PDF export

This is a significant slice — should be GRILLED via `/idea-to-slice` at pickup with full grill-with-docs + Security STRIDE + grill-me passes before drafting ACs.

## Acceptance criteria (stub — expand at pickup via /idea-to-slice grill)

- [ ] AC-PRE: Deliver missing `Plans/mockups/questionnaire.html` mockup (design phase; maintainer + designer collaboration).
- [ ] AC-PRE: Run `/idea-to-slice` grill-with-docs + Security STRIDE on the feature spec; produce 20-30 ACs.

(Detailed ACs filed when the design mockup lands + grill completes.)

## Constitutional invariants honored

- **#9 Manual evidence is first-class.** Questionnaire answers cite evidence records; same first-class treatment as automated evidence.
- **#1 One control, N framework satisfactions.** AnswerLibrary anchors answers to SCF; reused across questionnaires (CAIQ, SIG, custom).
- **AI-assist boundary.** v1 has NO AI-assist; canonical-answer reuse is pattern-matching, not AI inference.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — questionnaire shape + license posture.

## Dependencies

- **#013** Evidence ledger (merged) — answers cite evidence records.
- **#003** Evidence SDK (merged) — push profile for ingest.
- Future slice — questionnaire AI-assist (v2 only).

## Anti-criteria (P0 — block merge)

- **P0-Q-1** NO CAIQ or SIG TEMPLATES bundled (license-bounded per canvas §4.6.2 + open-question #15).
- **P0-Q-2** NO AI-assist in v1 (deferred per CLAUDE.md).
- **P0-Q-3** NO vendor-facing portal in v1 (out of scope; future slice).
- **P0-Q-4** NO scope creep into cross-tenant sharing.
- **P0-Q-5** NO vendor-prefixed test fixture tokens.

## Notes for the implementing agent

LARGE slice. Likely should split into 2-4 sub-slices once design mockup lands. Marker priority is LOW until design phase completes — the maintainer should NOT pick this up without first delivering the mockup + running `/idea-to-slice` for the comprehensive design.

This slice is a placeholder so the gap is captured in the canonical `_STATUS.md`; the actual design + build work is its own multi-week effort.

Provenance: filed 2026-05-18 from comprehensive front-end-to-back-end gap audit; placeholder slice to capture the missing feature.
