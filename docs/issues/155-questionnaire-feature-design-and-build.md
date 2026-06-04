# 155 — Questionnaire feature: design + build (CAIQ / SIG / HECVAT response collection)

**Cluster:** Backend / Frontend / Multi-tenancy
**Estimate:** 3-4d (tracer-bullet scope locked by maintainer 2026-05-20)
**Type:** JUDGMENT
**Status:** `merged` (status reconciled 2026-06-03 — backlog drained per \_STATUS.md SoR; loop terminated batch 184)

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

**Scope discipline — TRACER-BULLET LOCK 2026-05-20 (what is OUT for v1):**

- AI-assist (v2)
- CAIQ + SIG TEMPLATES bundling (licensing-bounded; customer brings file)
- Cross-tenant questionnaire sharing
- Vendor-facing portal (where the requesting vendor submits)
- **Multi-format import (CSV / JSON / Word) — DEFERRED. Excel only for v1.** Spillover slices file as demand surfaces.
- **HECVAT bundled templates — DEFERRED.** Customer brings the Excel file; library is format-agnostic via the Excel reader. Bundling HECVAT is a follow-on slice that just adds the .xlsx fixtures + a "starter library" UX affordance.

**What this slice ships (v1 tracer-bullet scope, locked by maintainer 2026-05-20):**

- Design phase: deliver the missing `Plans/mockups/questionnaire.html` mockup (mockup-only scope; the load-bearing UX call is what the answer-authoring + citation surface looks like)
- Backend: `internal/api/questionnaires/` package with CRUD + **Excel import** + Excel export endpoints
- Frontend: `web/app/(authed)/questionnaires/` route + detail / response views (manual authoring; one citation picker; PDF export button)
- AnswerLibrary: **skeleton** — SCF-anchored canonical answers reusable across questionnaires. Skeleton = the table + the lookup query + a "previous answer" suggestion picker on the authoring surface. Full library management UI (search, edit, deprecate, version) is a v2 follow-on.
- Manual answer authoring with citations to evidence records
- PDF export

**Why tracer-bullet:** the v1 binary success test is the solo CISO running their next SOC 2 out of atlas. Vendor security questionnaires arrive in their inbox in Excel format 80%+ of the time. Shipping Excel-only ingest + manual authoring + a primitive answer-library closes the gap immediately, and every other format / AI-assist / vendor portal is purely additive — never blocks the v1 success test.

**Load-bearing design decision at pickup:** the AnswerLibrary key shape. Free-text-keyed → glorified word processor. SCF-anchor-keyed → genuine reuse across CAIQ / SIG / vendor-custom questionnaires. The skeleton MUST key on SCF anchor IDs (an answer is "what we say about IAC-06" not "what we said when asked 'do you encrypt data at rest'"). The question-text mapping is a _secondary_ index for fuzzy matching, not the primary key.

This is still a significant slice — should be GRILLED via `/idea-to-slice` at pickup with full grill-with-docs + Security STRIDE + grill-me passes before drafting full ACs against the tracer-bullet scope. The scope lock above is the load-bearing call; the grill produces the implementation ACs.

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

LARGE slice — but scope is now tracer-bullet locked (Excel import + manual authoring + AnswerLibrary skeleton + PDF export). Likely still produces 2-3 follow-on spillover slices: (1) CAIQ/SIG ingest (Excel + CSV + JSON for the same library); (2) full AnswerLibrary management UI (search/edit/deprecate/version); (3) vendor-facing portal. Those file as demand surfaces.

Maintainer pre-confirm 2026-05-20: scope locked, ready for engineer pickup. Engineer's first step at pickup is delivering the `Plans/mockups/questionnaire.html` mockup against the locked scope (NOT the full canvas §4.6 surface). The mockup IS the design grill input.

Provenance: filed 2026-05-18 from comprehensive front-end-to-back-end gap audit; scope locked 2026-05-20 by maintainer to tracer-bullet (Excel-only, manual-authoring-first, AnswerLibrary skeleton).
