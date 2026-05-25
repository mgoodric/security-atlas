# 263 — UI honesty: questionnaire frontend page (Stages A + C; Stage B deferred to #264)

**Cluster:** Frontend
**Estimate:** 3-5d
**Type:** JUDGMENT
**Status:** `ready`
**Parent:** slice 204 (per-page UI parity audit) — finding category iv
(MOCKUP-STALE) + category i (layout/chrome parity, by absence).
**Grilled:** 2026-05-24 via `/idea-to-slice` against the slice 155
backend + the `Plans/mockups/questionnaire.html` surface (8 design
points resolved; see "User-confirmed decisions" below).

## Narrative

Slice 155 (merged at `12da637`) shipped the backend HTTP API for
questionnaires (7 routes under `/v1/questionnaires/...`). The Next.js
page that consumes those routes was never built — slice 155 D7
explicitly deferred the frontend to a follow-on slice that never
landed. This slice closes that gap.

The mockup at `Plans/mockups/questionnaire.html` describes a complete
authoring surface across three stages:

- **Stage A — Excel upload**: drag-drop / file-picker / 5MB cap →
  `POST /v1/questionnaires/{id}/import-excel`
- **Stage B — Column-mapping review**: parse the uploaded sheet, let
  the operator confirm which column is "question text" vs "answer"
  vs "control_id" before commit. **Deferred to slice 264** per slice
  155 D3 + this slice's P0-263-5.
- **Stage C — Two-pane authoring**: question list on the left,
  detail/answer view on the right. Includes the AnswerLibrary
  suggestions panel, the citation picker, the save-to-library
  controls, and the PDF-export button.

This slice ships **Stages A + C**. On successful upload the operator
lands directly in Stage C with the first question selected. Stage B
becomes a separate small slice (slice 264) that intercepts post-parse
between Stage A and Stage C — adding it later does not break this
slice's wire shape.

### What ships in this slice

**Routes:**

- `web/app/(authed)/questionnaires/page.tsx` — list view
- `web/app/(authed)/questionnaires/[id]/page.tsx` — authoring view
  (Stage C two-pane)

**BFF routes** (per slice 110 pattern; forward `atlas_jwt` cookie):

- `web/app/api/questionnaires/route.ts` — GET (list) + POST (create)
- `web/app/api/questionnaires/[id]/route.ts` — GET (detail)
- `web/app/api/questionnaires/[id]/import-excel/route.ts` — POST
  (multipart Excel upload)
- `web/app/api/questionnaires/[id]/answers/[qid]/route.ts` — PATCH
  (single-answer save)
- `web/app/api/questionnaires/[id]/suggestions/route.ts` — GET
  (deterministic SCF-anchor pattern-match — NO LLM)
- `web/app/api/questionnaires/[id]/export-pdf/route.ts` — POST
  (PDF download)

**Components:**

- `web/components/questionnaire/upload-zone.tsx` — drag-drop +
  file-picker + 5MB client-side cap
- `web/components/questionnaire/question-list.tsx` — left pane
- `web/components/questionnaire/answer-editor.tsx` — right pane
  textarea + per-answer controls
- `web/components/questionnaire/suggestions-panel.tsx` — top 3
  SCF-anchor suggestions
- `web/components/questionnaire/citation-picker.tsx` — unified
  ⌘K-style command palette wrapping slice 268's `/v1/search`

**Sidebar nav** (slice 186 pattern):

- New entry "Questionnaires" under the Operations cluster (same
  cluster as Calendar / Vendors), visible to ALL authed users.
  Per-questionnaire write authz is enforced at the API layer (slice
  155's existing admin gate); the nav is intentionally non-gated to
  match Calendar/Vendors.

### Scope discipline (deliberately OUT)

- **Stage B column-mapping review** — slice 264. Out per P0-263-5.
- **CAIQ / SIG / HECVAT bundled templates** — license-bounded per
  slice 155 P0-Q-1. The upload zone accepts any Excel; bundling
  vendor templates is a separate licensing question.
- **Vendor-facing portal** — slice 155 P0-Q-3 explicitly defers
  the "share a link with a vendor" surface. Out.
- **AI-assist** — slice 155 P0-Q-2 + this slice P0-263-1. The
  suggestions panel is deterministic SCF-anchor pattern-match
  (slice 155 D2); ZERO LLM / AI cards / model-confidence badges /
  retrieval-context panels.
- **Backend changes** — P0-263-4. This slice consumes the 7 routes
  slice 155 shipped; no new HTTP API, no migration.

## Threat model

| STRIDE                       | Threat                                                                                                                                                    | Mitigation                                                                                                                                                                                         |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing               | Frontend page consumes `/v1/questionnaires/*` routes which already enforce tenant context via the standard atlas auth middleware.                         | All BFF routes attach the `atlas_jwt` cookie via slice 110's pattern; the platform enforces RLS + per-tenant gating.                                                                               |
| **T** Tampering              | Excel-upload flow could be abused (large files, malicious payloads).                                                                                      | Backend (slice 155) caps at 5MB + excelize security mitigations. Frontend adds client-side 5MB check as UX courtesy; rejects oversized files before POST.                                          |
| **R** Repudiation            | Answer mutations go through the existing PATCH route which already records audit trail.                                                                   | Slice 155's audit-log writes cover every answer save + library-save action; no new audit-log surface added.                                                                                        |
| **I** Information disclosure | Suggestion picker surfaces prior answers from the AnswerLibrary. Library is tenant-scoped by RLS at the backend; frontend must not bypass tenant context. | All GET/POST calls go through the BFF (which attaches the atlas-jwt) — no direct cross-origin or bearer-leak path. Citation picker reuses slice 268's `/v1/search` which is RLS-scoped per tenant. |
| **D** DoS                    | Repeatedly hitting `import-excel` with large files could exhaust storage.                                                                                 | Slice 155's backend already caps + rate-limits. Frontend adds client-side file-size validation as a UX courtesy.                                                                                   |
| **E** EoP                    | None — frontend cannot grant privileges; relies on backend authz.                                                                                         | n/a                                                                                                                                                                                                |

**Verdict.** **has-mitigations** — no new threat surface introduced
by the frontend slice beyond what slice 155's backend already handles.
AI-assist boundary is the load-bearing invariant (codified as
P0-263-1 + AC-12).

## Acceptance criteria

### Stage A — Excel upload

- [ ] **AC-1.** `/questionnaires` (list view) renders the tenant's
      existing questionnaires from `GET /v1/questionnaires` (via the
      new BFF route).
- [ ] **AC-2.** When the tenant has ZERO questionnaires, the page
      renders a single hero CTA: drag-drop zone + "Upload your first
      vendor questionnaire (Excel)" headline. No roster cards, no
      helper-text card, no sample-questionnaire CTA (user-confirmed
      empty-state shape).
- [ ] **AC-3.** When the tenant has 1+ questionnaires, the list view
      renders a row per questionnaire (title + last-modified +
      progress chip "12/47 answered") plus a "+ Upload Excel" button
      in the page header.
- [ ] **AC-4.** Excel-upload zone accepts drag-drop AND file-picker.
      Files > 5MB are rejected client-side with a destructive toast
      ("File too large — 5MB maximum"). Files with non-.xlsx
      extension are rejected with a similar toast.
- [ ] **AC-5.** Successful upload calls `POST /v1/questionnaires` (to
      create the questionnaire shell) then
      `POST /v1/questionnaires/{id}/import-excel` with the file body.
      On 200, the page navigates to `/questionnaires/{id}` (Stage C
      authoring view).
- [ ] **AC-6.** Upload failure renders a destructive toast with the
      backend's error message; the operator stays on the list view.
- [ ] **AC-7.** During upload, a loading spinner replaces the
      drag-drop zone CTA so the operator knows the import is in
      flight.

### Stage C — Two-pane authoring view

- [ ] **AC-8.** `/questionnaires/[id]` renders a two-pane layout:
      left pane = scrollable question list (one row per question),
      right pane = answer editor for the currently-selected question.
      Layout collapses to a single column at `< md` (slice 277
      mobile-responsive baseline composes; left pane becomes a
      collapsible drawer at mobile widths).
- [ ] **AC-9.** Each question row in the left pane shows: question
      text (truncated to 2 lines), answer status chip (`Unanswered`
      / `Draft` / `Final`), and a citation-count badge.
- [ ] **AC-10.** Clicking a question row binds the right pane to that
      question. The right pane shows: question text (full), answer
      textarea (autosaved-debounced PATCH to
      `/v1/questionnaires/{id}/answers/{qid}`), suggestions panel,
      citation picker, save-to-library checkbox, status pill.
- [ ] **AC-11.** Answer textarea autosaves on 500ms debounce after
      keystroke quiescence. PATCH errors surface a non-destructive
      banner above the textarea: "Last save failed — retry?"; click
      retries.

### Suggestions panel (AI-assist-clean per slice 155 D2)

- [ ] **AC-12.** Suggestions panel calls
      `GET /v1/questionnaires/{id}/suggestions?question_id={qid}` and
      renders the TOP 3 results ranked by SCF-anchor frequency
      (backend-determined; this slice consumes the response verbatim).
      ZERO new AI surface; ZERO model-confidence badges; ZERO
      retrieval-context panels. P0-263-1 invariant enforcement.
- [ ] **AC-13.** Each suggestion card shows: a 2-line excerpt of the
      prior answer text, the SCF anchor ID (e.g., `CRY-05`), and a
      "Use this answer" button. Clicking the button REPLACES the
      current textarea content with the suggestion (operator can
      then edit). NO append-mode in v1.
- [ ] **AC-14.** When the backend returns zero suggestions, the
      panel renders a muted "No prior answers for this anchor"
      caption. Silent absence — no decorative empty-state graphics.

### Citation picker (unified ⌘K palette)

- [ ] **AC-15.** Citation picker is a shadcn `<Command>` palette
      opened via a "+ Cite" button below the answer textarea. The
      palette has a single search input that calls slice 268's
      `/v1/search?types=controls,evidence&q=<query>` (no new
      endpoint — reuse).
- [ ] **AC-16.** Search results render grouped by type (controls
      first, evidence second). Each row shows id + title + a small
      type-icon. Clicking a row attaches the citation to the current
      answer (via the existing PATCH route's `citations` field).
- [ ] **AC-17.** Currently-attached citations render as removable
      chips below the answer textarea. Removing a chip PATCHes the
      answer with the updated citation list.

### Save-to-library

- [ ] **AC-18.** Per-answer "Save as canonical for this SCF anchor"
      checkbox sits below the citation chips. Default OFF. When
      checked, the answer's autosave PATCH includes
      `save_to_library: true`; the backend (slice 155) handles the
      library write. UI does NOT separately POST to a library
      endpoint.

### PDF export

- [ ] **AC-19.** Page header "Export PDF" button calls
      `POST /v1/questionnaires/{id}/export-pdf`; on 200 the response
      body is streamed to the browser as a download
      (`questionnaire-{title-slug}.pdf`).
- [ ] **AC-20.** Export failure renders a destructive toast with the
      backend's error message; the operator stays on the authoring
      view.

### Sidebar nav

- [ ] **AC-21.** Sidebar exposes a "Questionnaires" entry under the
      Operations cluster (same cluster as Calendar + Vendors). Entry
      visible to ALL authed users (matches Calendar/Vendors
      convention); per-questionnaire write authz is enforced at the
      API layer (slice 155).
- [ ] **AC-22.** Active-route highlighting matches the existing
      sidebar pattern; the entry is highlighted on
      `/questionnaires` AND `/questionnaires/*`.

### Tests

- [ ] **AC-23.** Vitest covers BFF route handlers + any new
      pure helpers (e.g. file-size validation, SCF-anchor display
      helper). Minimum 15 cases.
- [ ] **AC-24.** Playwright spec `questionnaires.spec.ts` covers
      the empty-list → upload → answer-one-question → cite-one-evidence
      → save-to-library → export-PDF happy path. 7+ assertions.
      Uses the slice 274 + slice 275 auto-wait pattern (no `.count()`
      snapshots; `page.waitForResponse` for any async boundary).
- [ ] **AC-25.** Existing Playwright + vitest suites pass unchanged
      (`npm run test` + `npm run test:e2e` clean).

### Polish

- [ ] **AC-26.** CHANGELOG entry under `## [Unreleased]` → `### Added`.
- [ ] **AC-27.** Decisions log at
      `docs/audit-log/263-questionnaire-frontend-decisions.md`
      captures: D1 (empty-state shape — hero CTA only), D2 (post-upload
      navigation — direct to Stage C), D3 (suggestion ranking — top 3 by
      SCF-anchor frequency), D4 (save-to-library — per-answer checkbox),
      D5 (citation picker — unified ⌘K palette via slice 268), D6
      (sidebar placement — Operations cluster, all authed), D7
      (AI-assist enforcement — deterministic suggestions only).

## Constitutional invariants honored

- **Invariant 9 (manual evidence is first-class).** Questionnaire
  answers cite evidence records via the citation picker; the
  frontend surfaces this with the same UI affordance as other
  manual-evidence flows.
- **Invariant 1 (one control, N framework satisfactions).** The
  suggestions panel ranks by SCF-anchor frequency — preserving
  slice 155 D2's design call that an answer is "what we say about
  IAC-06", reusable across CAIQ/SIG/custom shapes.
- **AI-assist boundary (hard).** ZERO LLM in this slice. The
  suggestions endpoint is deterministic pattern-match per slice
  155 D2; the mockup explicitly excludes AI per its lines 17-30.
  P0-263-1 + AC-12 codify the enforcement.
- **Invariant 6 (tenant isolation via RLS).** All BFF routes
  forward the bearer cookie; RLS at the platform layer enforces
  tenant scoping on every read AND write.

## Canvas references

- `Plans/canvas/04-evidence-engine.md` §4.6 — questionnaire shape
- `Plans/mockups/questionnaire.html` — target surface (lines 17-30
  explicitly exclude AI-assist; this slice honors that exclusion)
- `docs/audit-log/155-questionnaire-tracer-decisions.md` D2, D3, D7
  — backend's deterministic-suggestions design + Stage B deferral +
  this slice's lineage

## Dependencies

- **#155** (questionnaire backend tracer) — `merged` at `12da637`.
  All 7 HTTP routes ship; this slice consumes them.
- **#013** (evidence ledger) — `merged`. Citation picker reads
  evidence records via slice 268.
- **#003** (Evidence SDK) — `merged`. Push profile available for
  programmatic answer ingest in a future slice.
- **#186** (sidebar role-conditional pattern) — `merged`. Reference
  for the sidebar nav entry.
- **#268** (unified `/v1/search` endpoint) — `merged` at `d9d8e69b`.
  Citation picker reuses this BFF; no new search infrastructure.
- **#274** (settings AC-9 e2e fix) + **#275** (control-detail-tabs
  e2e auto-wait helper) — `merged`. e2e patterns this slice's
  Playwright spec must follow (no `.count()` snapshots; auto-wait
  on every async boundary).
- **#277** (mobile-responsive baseline) — in-review (PR #623).
  Two-pane layout collapse at `< md` composes with the baseline.

## Anti-criteria (P0 — block merge)

- **P0-263-1.** Does NOT introduce AI-assist (CLAUDE.md AI-assist
  boundary — slice 155 P0-Q-2). NO model-confidence badges, NO
  "AI drafted" cards, NO retrieval-context panels. Suggestions
  are deterministic backend pattern-match — UI consumes verbatim.
- **P0-263-2.** Does NOT ship CAIQ / SIG bundled templates
  (license-bounded — slice 155 P0-Q-1).
- **P0-263-3.** Does NOT ship a vendor-facing portal (slice 155
  P0-Q-3).
- **P0-263-4.** Does NOT touch the backend HTTP API; consumes only
  the routes shipped by slice 155. No new platform endpoint, no
  migration, no schema change.
- **P0-263-5.** Does NOT ship the Stage B column-mapping review
  UI — that is slice 264. Post-upload nav goes Stage A → Stage C
  directly.
- **P0-263-6.** Does NOT modify `docs/issues/_STATUS.md` from
  inside the slice's own commits — orchestrator's surface.
- **P0-263-7.** Does NOT call any backend endpoint outside the
  slice 155 + slice 268 set. The 7 questionnaire routes + the
  unified `/v1/search` route are the ONLY platform surfaces.
- **P0-263-8.** Does NOT regress slice 277's mobile-responsive
  baseline once that lands. Two-pane layout uses Tailwind
  breakpoints; no fixed pixel widths > 320px.

## Skill mix (4)

1. Next.js 16 App Router routes + BFF route patterns
2. shadcn/ui composition (`<Command>` palette for citation picker;
   two-pane layout primitives)
3. TanStack Query with debounced autosave PATCH
4. Playwright auto-wait pattern (slice 274/275 lesson) + vitest
   pure-helper testing

## Notes for the implementing agent

### Phase 2 grill output

Eight design points resolved via `/idea-to-slice` on 2026-05-24
(user-driven Q&A). See the matched D-numbers in AC-27 for the
canonical decisions. Highest-leverage calls:

- **Empty state = hero CTA only** (not roster, not sample). Keeps
  the empty surface honest.
- **Post-upload → Stage C directly** (skip Stage B). Stage B will
  intercept here later via slice 264; the wire shape doesn't change.
- **Suggestions = top 3 by SCF-anchor frequency** (slice 155
  deterministic-suggestion endpoint). NO LLM.
- **Citation picker = unified ⌘K palette via slice 268** (don't
  build a new picker; reuse the just-merged `/v1/search`).
- **Save-to-library = per-answer checkbox** (operator-controlled
  granularity; default OFF).
- **Sidebar = Operations cluster, all authed users** (matches
  Calendar/Vendors pattern; per-questionnaire write authz is
  backend-enforced).
- **AI-assist boundary = deterministic suggestions only** (the
  suggestions endpoint exists per slice 155 D2; calling it is fine
  because it's not an LLM call).
- **Stage B deferred to slice 264** (confirmed; P0-263-5 enforces).

### Phase 3 threat model summary

Verdict: **has-mitigations**. Frontend consumes slice 155's
already-hardened backend; no new threat surface. The load-bearing
mitigation is the AI-assist boundary (P0-263-1 + AC-12 + the
hard-coded "no model badges" pattern).

### Implementation order (recommended)

1. **BFF routes first** (the 6 routes under `web/app/api/questionnaires/`)
   — vitest pins each handler's bearer-forward + response-shape.
2. **List page + empty state + upload zone** (Stage A) — Playwright
   walks the empty-list → upload → navigate-to-Stage-C path.
3. **Authoring two-pane shell** (Stage C scaffold) — left list +
   right editor, autosave wired, status pill rendering.
4. **Suggestions panel** — call the suggestions endpoint, render
   top 3, wire "Use this answer" → textarea replace.
5. **Citation picker** — wrap slice 268's `/v1/search` in a
   `<Command>` palette; wire selection → PATCH citations.
6. **Save-to-library** + **PDF export** + **sidebar nav** in
   parallel — these are independent surfaces.
7. **Playwright happy path** — the AC-24 spec last, mocking BFF
   endpoints per the slice 274/275 auto-wait pattern.

### Mobile considerations (slice 277 compose)

The two-pane layout MUST collapse to single-column at `< md`
(Tailwind breakpoint 768px). Recommended pattern:

- `< md`: render list view as `/questionnaires/[id]?pane=list`,
  authoring view as `/questionnaires/[id]?pane=edit`. URL-bound
  pane state lets back-button work intuitively on mobile.
- `≥ md`: render both panes side-by-side; pane URL param ignored.

Reference: slice 277's responsive-discipline doc (when it lands).

Provenance: grilled 2026-05-24 via `/idea-to-slice` after the
continuous-batch loop's GUARD-3 escalation flagged the original
AC-PRE stub as not dispatch-ready. Eight design points
(empty-state shape, post-upload nav, suggestion ranking, save-to-
library control, citation picker shape, sidebar placement,
AI-assist boundary, Stage B disposition) resolved via maintainer
Q&A; canonical decisions live in the D1-D7 references in AC-27.
