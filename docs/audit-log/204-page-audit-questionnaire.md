# 204 — Per-page UI parity audit · /questionnaires

**Audit date:** 2026-05-23
**Auditor:** slice 204 subagent fleet (questionnaire page)
**Mockup:** `Plans/mockups/questionnaire.html` (title: "Acme Corp · CAIQ v4.1 · security-atlas")
**Live URL:** `https://atlas-edge.home.gmoney.sh/questionnaires` (probed)
**Implementing slice (backend):** #155 — merged at `12da637` via PR #433 on 2026-05-20

## Executive summary

The questionnaire page **does not exist as a live frontend route**. The
mockup is comprehensive and slice-scope-aligned (it was rewritten by
slice 155 to match the tracer-bullet scope), and the backend HTTP API
(7 routes under `/v1/questionnaires/*`) shipped with slice 155. But
slice 155's decisions log D7 explicitly deferred the Next.js page to a
follow-on slice ("Slice 156 — questionnaire frontend (Next.js)") that
was **never filed**. Slice 156 is `dashboard-opa-admit-omissions`, an
unrelated concern.

So the questionnaire feature is operator-inaccessible today despite
backend + mockup being merged. Two findings file as spillovers.

## Probes

```
$ curl -sk --cookie "atlas_jwt=$JWT" \
    https://atlas-edge.home.gmoney.sh/questionnaires \
    -o /tmp/quest-live-1.html -w "HTTP %{http_code}\n"
HTTP 404

$ curl -sk --cookie "atlas_jwt=$JWT" \
    https://atlas-edge.home.gmoney.sh/questionnaire \
    -o /tmp/quest-live-2.html -w "HTTP %{http_code}\n"
HTTP 404
```

Filesystem confirmation:

- `web/app/(authed)/questionnaires/` — does NOT exist
- `web/app/(authed)/questionnaire/` — does NOT exist
- `grep -rn -i 'questionnaire' web/components/ web/app/` returns
  zero hits (no sidebar nav, no shell wiring, no BFF route)
- Backend (slice 155): `internal/api/questionnaires/handlers.go`
  registers 7 routes (`Create`, `List`, `Get`, `ImportExcel`,
  `UpsertAnswer`, `Suggestions`, `ExportPDF`)
- Slice 155 D7: "This slice ships the mockup + the backend HTTP
  API. The actual Next.js page (`web/app/(authed)/questionnaires/`)
  is a v2 follow-on."

## Findings (by slice 204 category)

### F-204-Q-1 — Frontend page missing (category iv: MOCKUP-STALE; category i: layout-parity by absence)

**Severity:** high. **Spillover:** [#263](../issues/263-questionnaire-frontend-page-missing.md).

The page the mockup describes has no live implementation. This is the
canonical "audit honesty signal" that slice 178 was built to surface
— a comprehensive mockup with no corresponding shipped surface. Slice
155 explicitly deferred this work in D7 + listed it as spillover
candidate #6, but the candidate was never filed.

Resolution: slice 263 ships
`web/app/(authed)/questionnaires/` against the existing slice 155
backend contract. Estimated 3-5d.

### F-204-Q-2 — Mockup Stage B (column-mapping review UI) is scope-deferred (category iv: MOCKUP-STALE)

**Severity:** low. **Spillover:** [#264](../issues/264-questionnaire-column-mapping-review-ui.md).

The mockup includes a "Stage B" surface (lines 113-175) showing a
post-upload column-mapping review step. Slice 155 D3 explicitly
deferred this UI ("Auto-detect via header-row heuristic. Manual
column-mapping UI step DEFERRED to a spillover slice"). The mockup
therefore promises a feature slice 155 chose not to ship.

Resolution: slice 264 defaults to Option A (remove Stage B from the
mockup; document the deferral in the file header) with Option B
(actually build Stage B + supporting backend) reserved for if/when
operator feedback shows the heuristic miss rate is high. Defaulting
to Option A keeps audit-honesty cheap and waits for real signal.

## Findings NOT filed (explicit non-issues)

- **AI-assist features.** The mockup deliberately excludes AI-assist
  (see top-of-file comment lines 17-30: "AI-assist (NO 'AI drafted'
  cards, NO model confidence, NO retrieval-context panels)"). The
  manual-authoring textarea placeholder explicitly states "The
  operator writes this directly — no AI drafting at v1." This
  honors the CLAUDE.md AI-assist boundary perfectly — no MOCKUP-STALE
  finding here.
- **AnswerLibrary key shape.** Mockup shows SCF-anchor-keyed
  suggestions (`SCF:IAC-06`, "2 prior in library") — matches slice 155
  D2 load-bearing call (AnswerLibrary keys on SCF anchor IDs). No
  finding.
- **PDF export.** Mockup shows PDF-export button in top bar +
  bottom-of-list-pane. Backend `POST /v1/questionnaires/{id}/export-pdf`
  exists. Will be exercised once slice 263 lands — no separate finding.
- **Backend routes coverage.** All 7 backend routes from slice 155
  have corresponding affordances in the mockup (create / list /
  detail / import-excel / upsert-answer / suggestions / export-pdf).
  No backend gap to file.
- **Tenant scoping / RLS.** Backend slice 155 enforces tenancy via
  the canonical four-policy RLS pattern. No frontend probe possible
  until the page lands; will surface naturally during slice 263's
  Playwright coverage.

## Audit honesty signal acknowledgement

This audit's finding shape (1 large + 1 small) is exactly what slice
178's MOCKUP-STALE + HONESTY-GAP taxonomy was designed to surface:
the platform shipped a backend + mockup but did not ship the
operator-facing route, leaving a public-facing artifact (the mockup)
that overstates the platform's shipped capability. Filing slice 263
closes the primary gap; filing slice 264 closes the secondary
mockup-vs-scope drift.

Slice 155 is the implementing slice. This audit does NOT debug,
extend, or fix slice 155's implementation — the spillovers continue
the slice 155 thread into the unshipped frontend surface.

## Anti-criteria honored (slice 204)

- NO inline fixes. Only the two slice files + this audit log were
  written; no production code touched.
- NO touches outside `docs/audit-log/204-*.md` + `docs/issues/2*-*.md`
  (verified via `git status`).
- NO Bearer tokens / cookie values / screenshots / DOM dumps in any
  file written under this audit.
- NO slice numbers outside the assigned range (263-267); 2 slices
  filed (263, 264) — well under the cap of 5.
- NO debugging of slice 155's implementation.
- NO `_STATUS.md` / `CHANGELOG.md` modification.
- NO slice 204 spec modification.
