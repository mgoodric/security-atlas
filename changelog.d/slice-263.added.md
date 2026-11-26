- **frontend:** slice 263 — questionnaire authoring page (Stages A + C).
  Closes the FE gap left by slice 155 D7 — the Next.js page that
  consumes the slice 155 backend HTTP API was never built. Ships the
  `/questionnaires` list view (empty-state hero CTA per user-confirmed
  D1; non-empty table + inline upload zone) and the
  `/questionnaires/{id}` two-pane authoring view (left = question list
  grouped by domain; right = answer editor with debounced autosave +
  deterministic suggestions panel + slice-268-backed citation picker +
  per-answer save-to-library checkbox + PDF export). Stage B (column-
  mapping review) is deferred to slice 264 per P0-263-5. Six new BFF
  routes under `web/app/api/questionnaires/` proxy slice 155's 7 HTTP
  routes verbatim — no backend changes (P0-263-4). The suggestions
  panel renders the deterministic SuggestForAnchor output verbatim —
  ZERO model badges, ZERO "AI drafted" cards, ZERO retrieval-context
  panels — codifying the AI-assist boundary at the UI layer (P0-263-1
  - AC-12). The citation picker reuses the slice-268 unified
    `/v1/search` endpoint via a command-palette-style popover built
    inline (no new shadcn dep). Mobile (`< md`) collapses the two-pane
    layout to URL-bound `?pane=list|edit` so the back-button works,
    composing with slice 277's mobile-responsive baseline. New
    Playwright spec at `web/e2e/questionnaires.spec.ts` walks the AC-24
    happy path with the slice 274/275 `waitForResponse` auto-wait
    pattern. Sidebar adds the Questionnaires entry in the Operations
    cluster (Vendors / Questionnaires / Board Packs); all authed users
    see it (matches Calendar/Vendors precedent — per-tenant write
    authz stays at the slice 155 API layer). See
    [`docs/audit-log/263-questionnaire-frontend-decisions.md`](docs/audit-log/263-questionnaire-frontend-decisions.md)
    for the seven D-decisions resolved via grilling.
