# 263 — UI honesty: questionnaire frontend page missing (mockup + backend exist; no live route)

**Cluster:** Frontend
**Estimate:** 3-5d (full questionnaire Next.js page + Playwright + vitest specs)
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** slice 204 (per-page UI parity audit) — finding category iv (MOCKUP-STALE) + category i (layout/chrome parity, by absence)

## Narrative

Surfaced during slice 204 per-page audit of `Plans/mockups/questionnaire.html`
(audit log: `docs/audit-log/204-page-audit-questionnaire.md`).

**The gap.** The mockup describes a complete questionnaire authoring surface
(Excel upload zone, two-pane question-list + detail view, AnswerLibrary
suggestion cards, citation picker, PDF export). The backend HTTP API for
all of it shipped in slice 155 (`internal/api/questionnaires/` — 7 routes
under `/v1/questionnaires/...`). The Next.js page that consumes those
routes was **never built**.

Live probes (audit-time):

- `GET https://atlas-edge.home.gmoney.sh/questionnaires` → HTTP 404
- `GET https://atlas-edge.home.gmoney.sh/questionnaire` → HTTP 404

Filesystem confirmation:

- `web/app/(authed)/questionnaires/` — does NOT exist
- `web/app/(authed)/questionnaire/` — does NOT exist
- No file under `web/` references the string `questionnaire` (verified
  via `grep -rn -i 'questionnaire' web/components/ web/app/`)

**This is a deliberate deferral, not a missed merge.** Slice 155's decisions
log (`docs/audit-log/155-questionnaire-tracer-decisions.md`) records this
as **D7 — Frontend deferred to a follow-on slice**:

> "This slice ships the mockup + the backend HTTP API. The actual
> Next.js page (`web/app/(authed)/questionnaires/`) is a v2 follow-on.
> ... **Follow-on slice candidate.** 'Slice 156 — questionnaire
> frontend (Next.js)' — would land the `web/app/(authed)/questionnaires/`
> route, the import flow, the answering UI, and the new vitest +
> Playwright specs."

That candidate slice was never filed. Slice 156 is `dashboard-opa-admit-omissions`
— a different concern. So the slice-155 spillover candidate #6 (the
frontend Next.js page) has remained unfiled for 2+ days, and the
questionnaire feature is operator-inaccessible despite the backend
being merged at `12da637` (PR #433) on 2026-05-20.

**Why this is a HONESTY-GAP, not just a feature gap.** The mockup is
referenced by the dashboard mockup (`Plans/mockups/dashboard.html`
line 491 — "Acme Corp CAIQ response due") as if the surface exists.
The v1 binary success test (solo CISO running their next SOC 2 out
of atlas) explicitly depends on the questionnaire workflow per
slice 155's narrative: "Vendor security questionnaires arrive in their
inbox in Excel format 80%+ of the time. Shipping Excel-only ingest +
manual authoring + a primitive answer-library closes the gap
immediately." Shipping only the backend HTTP API closes the gap for
a curl-fluent operator; the actual primary user cannot reach the
feature.

## Threat model

| STRIDE                | Threat                                                                                                                                                    | Mitigation                                                                                                                                           |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Frontend page would consume `/v1/questionnaires/*` routes which already enforce tenant context via the standard atlas auth middleware.                    | AC: the new page does NOT introduce a new auth path; it uses the existing BFF + atlas-jwt cookie pattern from `web/lib/api/bff.ts`.                  |
| **T** Tampering       | Excel-upload flow could be abused (large files, malicious payloads).                                                                                      | Backend already enforces 5MB cap + excelize security mitigations per slice 155. Frontend's job is to enforce the same client-side limit before POST. |
| **R** Repudiation     | None — answer mutations go through the existing PATCH route which already records audit trail.                                                            | n/a                                                                                                                                                  |
| **I** Info disclosure | Suggestion picker surfaces prior answers from the AnswerLibrary. Library is tenant-scoped by RLS at the backend; frontend must not bypass tenant context. | AC: all GET/POST calls go through the BFF (which attaches the atlas-jwt) — no direct cross-origin or bearer-leak path.                               |
| **D** DoS             | Repeatedly hitting `import-excel` with large files could exhaust storage.                                                                                 | Slice 155's backend already caps + rate-limits. Frontend adds client-side file-size validation as a UX courtesy.                                     |
| **E** EoP             | None — frontend cannot grant privileges; relies on backend authz.                                                                                         | n/a                                                                                                                                                  |

**Verdict.** No new threat surface introduced by the frontend slice beyond
what slice 155's backend already handles.

## Acceptance criteria (stub — expand at pickup via /idea-to-slice grill)

- [ ] **AC-PRE.** Run `/idea-to-slice` grill-with-docs + Security STRIDE
      against slice 155's mockup + decisions log + canvas §4.6 before
      drafting full ACs. Expected output: 15-25 ACs spanning route shape,
      data flow, Playwright + vitest coverage, sidebar nav integration.
- [ ] **AC-1.** `web/app/(authed)/questionnaires/` route lands with:
  - `page.tsx` — list of tenant's questionnaires (consumes
    `GET /v1/questionnaires`)
  - `[id]/page.tsx` — detail/authoring view (two-pane shape per
    mockup Stage C; consumes `GET /v1/questionnaires/{id}`,
    `PATCH /v1/questionnaires/{id}/answers/{qid}`,
    `GET /v1/questionnaires/{id}/suggestions`,
    `POST /v1/questionnaires/{id}/export-pdf`)
- [ ] **AC-2.** Excel upload flow lands per mockup Stage A:
      drag-drop + file-picker + 5MB client-side cap; uses
      `POST /v1/questionnaires/{id}/import-excel`.
- [ ] **AC-3.** Stage B "Review parsed rows · column mapping" UI is
      **OUT of this slice's scope** (filed separately as slice 264 —
      the mockup shows it but slice 155 D3 explicitly deferred it).
- [ ] **AC-4.** AnswerLibrary suggestion card renders top 2-3 prior
      answers for the question's SCF anchor; "Use this answer" inserts
      into the textarea.
- [ ] **AC-5.** Citation picker lets the operator attach evidence
      records and SCF-anchored control records to an answer.
- [ ] **AC-6.** Save-to-library checkbox writes the answer back to
      the AnswerLibrary as canonical for that SCF anchor.
- [ ] **AC-7.** PDF export button triggers backend render + download.
- [ ] **AC-8.** Sidebar nav surfaces a "Questionnaires" entry under
      the authed shell. If the sidebar component does not yet support
      the link's icon convention, file a tiny follow-on rather than
      expanding this slice.
- [ ] **AC-9.** Playwright spec `questionnaires.spec.ts` covers
      the empty-list → upload → answer-one-question → export-PDF
      happy path.
- [ ] **AC-10.** Vitest covers the BFF route handlers + any new
      client-side parsing helpers.

(Full ACs filed at pickup via /idea-to-slice grill.)

## Constitutional invariants honored

- **Invariant #9 (manual evidence is first-class).** Questionnaire
  answers cite evidence records; the frontend surfaces this with the
  same UI affordance as other manual-evidence flows.
- **Invariant #1 (one control, N framework satisfactions).** The
  frontend surfaces AnswerLibrary suggestions keyed on SCF anchors —
  preserving the slice 155 D2 design call that an answer is "what
  we say about IAC-06," reusable across CAIQ/SIG/custom shapes.
- **AI-assist boundary (hard).** This slice ships ZERO AI-assist —
  no "AI drafted" cards, no model-confidence badges, no
  retrieval-context panels. Suggestions are deterministic
  pattern-matches keyed on SCF anchors. The mockup itself
  explicitly excludes AI-assist (top-of-file comment in
  `Plans/mockups/questionnaire.html` lines 17-30).

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — questionnaire shape
- `Plans/mockups/questionnaire.html` — the target surface
- `docs/audit-log/155-questionnaire-tracer-decisions.md` D7 — the
  prior deferral that this slice resolves

## Dependencies

- **#155** (questionnaire backend tracer) — `merged` at `12da637`.
  All 7 HTTP routes ship; this slice consumes them.
- **#013** (evidence ledger) — `merged`. Citation picker reads
  evidence records.
- **#003** (Evidence SDK) — `merged`. Push profile available for
  programmatic answer ingest in a future slice.

## Anti-criteria (P0 — block merge)

- **P0-263-1.** Does NOT introduce AI-assist (CLAUDE.md AI-assist
  boundary — slice 155 P0-Q-2).
- **P0-263-2.** Does NOT ship CAIQ / SIG bundled templates
  (license-bounded — slice 155 P0-Q-1).
- **P0-263-3.** Does NOT ship a vendor-facing portal (slice 155
  P0-Q-3).
- **P0-263-4.** Does NOT touch the backend HTTP API; consumes only
  the routes shipped by slice 155.
- **P0-263-5.** Does NOT ship the Stage B column-mapping review UI
  — that is slice 264.

## Notes for the implementing agent

This is a substantial frontend slice. The backend contract is fully
specified (slice 155's `internal/api/questionnaires/handlers.go`
header comment enumerates all 7 routes with their request/response
shapes). The mockup is comprehensive and slice-scope-locked. The
load-bearing UX call (AnswerLibrary suggestions keyed on SCF
anchors) is already resolved by slice 155 D2.

Provenance: filed 2026-05-23 from slice 204 per-page UI parity audit
of `Plans/mockups/questionnaire.html`. Audit log:
`docs/audit-log/204-page-audit-questionnaire.md`.
