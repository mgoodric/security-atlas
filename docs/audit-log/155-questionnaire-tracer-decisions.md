# Slice 155 — Questionnaire tracer-bullet decisions log

**Type:** JUDGMENT
**Surfaced during:** slice 155 implementation (2026-05-21).
**Parent slice:** [`docs/issues/155-questionnaire-feature-design-and-build.md`](../issues/155-questionnaire-feature-design-and-build.md)
**Scope-lock authority:** maintainer, 2026-05-20 — tracer-bullet scope: Excel import + manual authoring + AnswerLibrary skeleton (SCF-anchor keyed) + PDF export. CSV / JSON / Word import, HECVAT bundled templates, vendor portal, and AI-assist are explicitly DEFERRED as spillover slices.

This log records the build-time decisions Claude made without blocking on a human approver, per the JUDGMENT-slice workflow (`Plans/prompts/04-per-slice-template.md`). The maintainer iterates post-deployment.

---

## D1 — Excel parser library: `github.com/xuri/excelize/v2`

**Decision.** Use `github.com/xuri/excelize/v2 v2.10.1`.

**Alternatives weighed.**

| Library                              | Maintenance                  | License | xlsx (write + formula) | Verdict                          |
| ------------------------------------ | ---------------------------- | ------- | ---------------------- | -------------------------------- |
| `github.com/xuri/excelize/v2`        | Active                       | BSD-3   | Full                   | Chosen.                          |
| `github.com/tealeg/xlsx/v3`          | Stale (~2y)                  | BSD-3   | Read-only post-v3.4    | Rejected — no longer maintained. |
| `github.com/360EntSecGroup/excelize` | Forked into xuri/excelize/v2 | n/a     | n/a                    | Rejected — superseded.           |

**CVE history considered.** excelize has had a handful of historical CVEs concentrated in two classes:

1. Spreadsheet formula injection / hyperlink resolution.
2. Zip-bomb-style decompression DoS on malformed XLSX containers.

**Mitigations applied.**

- **Size cap before parsing.** `MaxUploadBytes = 5 MB` is enforced (a) by `http.MaxBytesReader` on the request body, (b) by `r.ParseMultipartForm(MaxUploadBytes)`, and (c) by an explicit `len(raw) > MaxUploadBytes` check inside `ParseExcel` before `excelize.OpenReader()` touches the bytes. Three independent layers.
- **Opaque formula treatment.** Cells are read via `f.GetRows()` which returns the cached display value of any formula cell, never evaluating the formula. Test `TestParseExcel_FormulaCellIsOpaque` asserts an attacker-supplied `HYPERLINK("http://attacker.example", ...)` formula never appears in the parsed output.
- **Row cap.** `MaxRows = 5000` rejects sheets larger than the realistic ceiling (SIG Core: ~855 rows; HECVAT: 321 rows). Sheet count is bounded to the first sheet only.
- **Source filename never used as a path.** Stored as `source_filename` text only — never `os.Open()`'d or passed to a shell.

**Acceptance gate.** Dependency-auditor sweep before merge surfaces no fresh advisories on excelize v2.10.1.

---

## D2 — AnswerLibrary suggestion ranking

**Decision.** Most-recent-first by `updated_at DESC`, ID-stable secondary sort, default page size 10.

**Reasoning.**

- The whole load-bearing call (slice spec) is that the library keys on SCF anchor. Multiple priors per anchor are the _whole point_ — surface them all.
- "Most recent first" is the operator's mental model: "what did I say last time about MFA?". No need for similarity scoring or AI ranking at v1 (constitutional invariant — no AI-assist v1).
- ID-stable secondary sort guarantees deterministic ordering when two entries share `updated_at` (e.g., bulk imports).
- 10 is enough for the tracer-bullet UX. If the operator's library grows to where 10 is insufficient, the library-management UI (search / pagination / version) is a v2 follow-on.

**Rejected alternatives.**

- Free-text similarity ranking — requires embeddings / pgvector, both deferred to v2.
- Most-cited-first — requires tracking citation count, which is out of tracer scope.

**Implementation.** Raw pgx (decision D6 below), not sqlc, because the `ORDER BY updated_at DESC, id ASC LIMIT $2` shape isn't worth the sqlc round-trip for a single 20-line query.

---

## D3 — Excel column mapping: header-row heuristic, manual override deferred

**Decision.** Auto-detect via header-row heuristic. Manual column-mapping UI step DEFERRED to a spillover slice.

**Heuristic shape.** Scan the first 5 rows; pick the first row containing at least one canonical-field alias match. Aliases are case-insensitive over `{code, text, domain, answer_type}`. Unmatched columns surface as `unmapped_columns` in the import response so the operator can see what was ignored.

**Why manual override is deferred.** The header-row heuristic resolves the canonical CAIQ / SIG / HECVAT / VSAQ shapes today. Custom vendor questionnaires with non-standard headers will surface as `unmapped_columns`; the operator can iterate by adjusting the spreadsheet header BEFORE re-uploading. A proper in-UI mapping step (drag-drop column → field) is real UX work — better landed as its own slice once we have operator feedback on the heuristic miss rate.

**Trade-off.** Operators with bespoke spreadsheets may need to massage headers before upload (one-time per vendor). The mockup acknowledges this with the "Notes (internal) / Internal Owner" unmapped-columns table in the import-review state.

---

## D4 — PDF rendering: chromedp + server-rendered HTML

**Decision.** Reuse the chromedp render path from `internal/board/pdf.go` (slice 022 / 027 / 137 precedent). Server-render HTML via Go's `text/template`-style string builder → headless Chrome `PrintToPDF`.

**Why this approach.**

- Zero new go.mod dependencies (chromedp is already wired).
- Deterministic, audit-friendly output (no flaky external service).
- The same `ErrChromeUnavailable` graceful-503 affordance as the board PDF — operators without Chrome get the questionnaire workflow intact; PDF export is a disabled affordance.
- `data:` URL only (never fetched URL) ⇒ no SSRF surface.
- `html.EscapeString` on every dynamic string ⇒ no HTML-injection from operator-supplied text.

**Rejected alternatives.**

- `gofpdf` — requires hand-laying every paragraph; high friction for questionnaires which vary by row count.
- `wkhtmltopdf` external binary — non-deterministic versions across deployments; ABI churn.
- LaTeX — overkill for the tracer-bullet shape; would also require a TeX installation in the docker-compose.

**Template approach.** Single-column print layout: questionnaire header + question-and-answer pairs + citation strip. Each `.item` is `page-break-inside: avoid` so a question doesn't get split across pages. Section the page via CSS, not pagination logic.

---

## D5 — Unmappable questions: store as `needs_mapping`, never reject upload

**Decision.** When the Excel parser cannot infer an SCF anchor for a row (no anchor column, ambiguous, or empty), the row is stored with `scf_anchor_id = NULL` and the question's `NeedsMapping` field is true. **Upload is NEVER rejected for unmappable rows.**

**Reasoning.**

- The operator is mid-upload — refusing the upload because of a few unmappable rows is a UX cliff. They'll do anything to get past it (delete the rows in the spreadsheet, fake the anchor) — both bad outcomes.
- The mapping is the _next_ operator step, not a precondition. The UI surfaces unmapped rows as "needs mapping" with an in-app picker. The PATCH `/v1/questionnaires/{id}/answers/{qid}` endpoint accepts an `scf_anchor_id` field for in-flight resolution.
- The data model is forgiving: `scf_anchor_id` is nullable in `questionnaire_questions` (with a partial index on the non-null subset for fast lookup).

**Trade-off.** Reports / suggestions are scoped to mapped questions only. A questionnaire with many `needs_mapping` rows produces a sparse PDF. The mockup acknowledges this with the "needs mapping" amber state in the question-list pane.

---

## D6 — sqlc for CRUD, raw pgx for the suggestion query

**Decision.** Use sqlc for `questionnaires`, `questionnaire_questions`, `questionnaire_answers`, and `answer_library` CRUD (8 queries in `internal/db/queries/questionnaires.sql`). Use raw `pgx` for `SuggestForAnchor`.

**Why split.**

- sqlc v1.31.1 generates clean Go for the CRUD shapes.
- The suggestion query (`SELECT ... WHERE scf_anchor_id = $1 ORDER BY updated_at DESC LIMIT $2`) has historically been quirky through sqlc when the LIMIT is a parameter. Raw pgx is 20 lines and far easier to reason about — and trivially testable via a `LibraryReader` interface (used in `library_test.go`).

**Pattern precedent.** Mirrors `internal/board/store.go` which uses sqlc for the table CRUD and falls back to raw pgx for the small number of recursive / DISTINCT-ON shapes that don't generate cleanly.

---

## D7 — Frontend deferred to a follow-on slice

**Decision.** This slice ships the mockup + the backend HTTP API. The actual Next.js page (`web/app/(authed)/questionnaires/`) is a v2 follow-on.

**Reasoning.**

- The mockup grounds the wire shape — backend handlers, response shapes, error codes are all confirmed against the mockup.
- The tracer-bullet success test is _backend Excel ingest + AnswerLibrary works end-to-end via curl or a small admin tool_. Building the polished React page on top adds frontend work that wouldn't gate the slice's binary success test.
- Frontend Playwright + vitest tests for the new page would also add to PR scope. Following the project's CI-discipline ratchet (slice 069), a frontend slice belongs in its own PR.

**Follow-on slice candidate.** "Slice 156 — questionnaire frontend (Next.js)" — would land the `web/app/(authed)/questionnaires/` route, the import flow, the answering UI, and the new vitest + Playwright specs.

---

## Security review (per workflow step 5 — REQUIRED for this slice)

Performed in-line during BUILD; threats considered:

| Class                                         | Mitigation                                                                                                                                                            |
| --------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Spreadsheet formula injection                 | excelize reads cached display values, never evaluates formulas. Test asserts attacker URL absent from parsed output.                                                  |
| Zip-bomb / decompression DoS                  | 5 MB size cap enforced at three layers (MaxBytesReader, ParseMultipartForm, parser preflight). Row cap of 5000.                                                       |
| XXE / external entity references in OOXML XML | xuri/excelize v2 disables external entity references by default.                                                                                                      |
| SQL injection                                 | All parameters bound via pgx; sqlc-generated code uses parameter binding. No string concatenation against user input anywhere in `internal/questionnaire/`.           |
| Cross-tenant suggestion leak                  | RLS enforced via `tenancy.ApplyTenant` on every Store method. Integration test `TestCrossTenantSuggestionIsolation` asserts tenant B sees zero entries from tenant A. |
| PDF SSRF                                      | chromedp navigates only to `data:` URLs — never fetched URLs. No remote-content path.                                                                                 |
| HTML injection in PDF rendering               | All dynamic strings pass through `html.EscapeString` before inlining into the document.                                                                               |
| Path traversal via filename                   | `source_filename` is stored as text only; never used as a filesystem path or shell argument.                                                                          |
| AI-assist boundary violation                  | NO AI inference path exists in this slice. AnswerLibrary suggestion is deterministic SQL — not embedding or model inference. Constitutional invariant maintained.     |

No P0 anti-criteria triggered:

- No CAIQ / SIG TEMPLATES bundled (P0-Q-1) ✓
- No AI-assist (P0-Q-2) ✓
- No vendor portal (P0-Q-3) ✓
- No cross-tenant sharing surface (P0-Q-4) ✓
- No vendor-prefixed test tokens (P0-Q-5) ✓ — fixtures use `Acme`, `Globex`, `OmniCorp` mockup labels only (no API tokens / keys / secrets).

---

## Spillover slices candidate list

If demand surfaces, file as standalone slices (per the spillover-as-slice policy):

1. **Multi-format import** — CSV / JSON / Word ingest into the same `internal/questionnaire/ParseExcel`-shaped pipeline.
2. **HECVAT bundled templates** — ship the v4.1.5 fixture in `questionnaire_templates/` with default SCF mappings.
3. **AnswerLibrary management UI** — search, edit, deprecate, version. Likely backend-light, frontend-heavy.
4. **Vendor-facing portal** — a separate route where the vendor (not the tenant operator) fills in their own questionnaire.
5. **AI-assist suggestion engine** — v2 only; tied to the AI-assist boundary in CLAUDE.md.
6. **Frontend Next.js page** — `web/app/(authed)/questionnaires/` route + Playwright + vitest specs.
7. **In-UI manual column-mapping step** — drag-drop column-to-field UI after upload.

---

## Open questions surfaced (forwarded to canvas open-questions if appropriate)

- **Operator workflow for `needs_mapping` rows at scale.** If a custom vendor questionnaire arrives with 80% un-anchored rows, what's the right batch-mapping UX? Mockup shows per-row "map" affordance; large-volume questionnaires might want a "map all by domain" shortcut. Deferred to operator feedback post-launch.
- **AnswerLibrary versioning.** When the operator's canonical answer changes ("we used to require TOTP; now we require WebAuthn"), should older entries surface in suggestions or be archived? Deferred — current shape allows multiple entries per anchor with the operator choosing which to use; archival/versioning is library-management-UI work.
- **PDF render reproducibility.** chromedp output is deterministic enough for the audit-trace use case, but Chrome version drift across deployments could yield subtly different PDFs. If audit-evidence integrity becomes a concern, pin the Chrome version or capture the input HTML as the canonical artifact and treat the PDF as derived.
