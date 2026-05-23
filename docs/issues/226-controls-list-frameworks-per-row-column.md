# 226 — Add Frameworks-per-row column to /controls list

**Cluster:** Quality / UI parity (frontend + backend)
**Estimate:** 2d (BFF + upstream join + UI column + tests)
**Type:** JUDGMENT
**Status:** `ready`

## Narrative

Surfaced during slice 204 audit fleet (controls page), captured as
follow-up per continuous-batch policy. The mockup at
`Plans/mockups/controls.html` (line 197) shows a right-aligned
`Frameworks` column on every anchor row, listing the frameworks the
anchor satisfies — e.g. `SOC2 · ISO · CSF` for `SCF:IAC-06`,
`SOC2 · ISO · CSF · GDPR` for `SCF:CRY-04`.

The live `/controls` page renders six columns (SCF anchor · Name ·
Family · State · Freshness · Last observed); the Frameworks column
is omitted. `web/app/(authed)/controls/page.tsx` `columns` array
(lines 225-294) has no Frameworks entry.

The data exists upstream: the SCF importer (slice 006) creates
STRM-typed edges from SCF anchors to framework requirements, and the
unified-control-framework graph (canvas §3, `UCF_GRAPH_MODEL.md`)
makes the per-anchor framework set a single graph query. But the
BFF response shape (`AnchorWithState` per `web/lib/api.ts`) does
NOT carry a `frameworks` field — so this is a backend + frontend
gap, not a pure presentational omission.

The Frameworks column is informational, not load-bearing. It
matters because:

1. A user scanning the controls list needs to see which anchors
   contribute to which framework audits. Without the column, the
   user must click into each control's detail page to see the
   framework set.
2. The list is the natural sort/group axis when planning audit-
   period scoping ("which anchors do I need before my SOC 2 audit
   freezes?"). Today that requires a per-row drilldown.

## Threat model

| STRIDE                | Threat                                                                               | Mitigation                                                                                                                              |
| --------------------- | ------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------- |
| **S** Spoofing        | Framework set is a global catalog property, not tenant-scoped — no spoofing surface. | n/a                                                                                                                                     |
| **T** Tampering       | None — read path.                                                                    | n/a                                                                                                                                     |
| **R** Repudiation     | None — read path.                                                                    | n/a                                                                                                                                     |
| **I** Info disclosure | The framework set per anchor is global catalog data; no tenant secret.               | n/a — global catalog data is intentionally visible to every authenticated user.                                                         |
| **D** DoS             | Per-anchor framework join could fan out into a N+1 query.                            | AC-3: the upstream join is a single recursive CTE — never per-row. Slice 098's "1,400 queries vs 1 query" hazard applies here directly. |
| **E** EoP             | None — read path.                                                                    | n/a                                                                                                                                     |

**Verdict.** `mitigations-required` only on the DoS axis. The
recursive CTE shape is the established pattern.

## Acceptance criteria

- **AC-1.** Upstream `GET /v1/anchors?include=state` response gains
  an additional join: each anchor carries a `frameworks: string[]`
  field with the short codes of every framework the anchor's STRM
  edges reach (e.g. `["soc2", "iso27001", "nist_csf"]`).
- **AC-2.** Short-code authority lives in
  `internal/catalog/framework_codes.go` (or equivalent): a single
  source of truth for the mockup-style abbreviations. Code names
  align with the framework pill values in
  `web/app/(authed)/controls/page.tsx` `FRAMEWORK_OPTIONS`
  (`soc2`, `iso27001`, `nist_csf`, `pci_dss`, `hipaa`, `gdpr`).
- **AC-3.** Upstream query joins frameworks via a single SQL
  statement (recursive CTE walking SCF anchor → framework
  requirement edges → framework). No per-row fan-out.
- **AC-4.** BFF response shape: `AnchorWithState` in
  `web/lib/api.ts` gains `frameworks: string[]`. Type narrowing
  - tests updated.
- **AC-5.** A seventh column lands in
  `web/app/(authed)/controls/page.tsx`, right-aligned, rendering
  the framework set joined by `·` separators (matching the
  mockup line 217 shape).
- **AC-6.** Empty framework set (anchor with no satisfaction edges
  yet) renders as `—`, not as an empty string.
- **AC-7.** Display abbreviations: `SOC2`, `ISO`, `CSF`,
  `PCI`, `HIPAA`, `GDPR` (matches mockup; the short code
  authority maps wire-format to display abbreviation).
- **AC-8.** Vitest unit coverage: BFF route handler with mocked
  upstream returning anchors with framework sets; column renderer
  with various set sizes including empty.
- **AC-9.** Playwright e2e spec: assert the Frameworks column
  header renders, assert at least one row carries a non-empty
  framework set.
- **AC-10.** Per-slice docs:
  `docs/audit-log/226-controls-frameworks-column-decisions.md`
  capturing (D1) display abbreviation authority + rationale;
  (D2) recursive-CTE choice vs application-side join; (D3) empty-
  set rendering policy; (D4) CI-delta scan results.
- **AC-11.** Pre-commit clean, DCO sign-off, Co-Authored-By trailer.

## Constitutional invariants honored

- **Invariant 1 (one control, N framework satisfactions).** The
  Frameworks column SURFACES this invariant — without it, the UCF
  graph's load-bearing decision is invisible to users.
- **Invariant 7 (SCF is the canonical control catalog).** The
  framework set is computed by walking STRM edges from the SCF
  anchor, not by duplicating per-framework control text.

## Canvas references

- `Plans/canvas/03-ucf.md` — UCF graph + STRM edges
- `Plans/UCF_GRAPH_MODEL.md` — graph diagrams + recursive CTE
  worked example
- `Plans/mockups/controls.html` line 197 + 208-262 — column header
  - per-row data shape
- `docs/audit-log/204-page-audit-controls.md` — parent audit

## Dependencies

- **#204** (UI parity audit fleet) — parent.
- **#006** (SCF catalog importer) — merged. Source of STRM edges.
- **#100** (controls list page) — merged. The page this slice
  modifies.
- **#104** (BFF anchors-with-state join) — merged. The slice this
  extends.

## Anti-criteria (P0 — block merge)

- **P0-226-1.** Does NOT introduce a per-row fan-out query. The
  framework join is a single recursive CTE.
- **P0-226-2.** Does NOT hardcode the framework short-code map
  in the frontend. The authority is the backend; the BFF carries
  the codes wire-side; the frontend renders only.
- **P0-226-3.** Does NOT touch the slice 204 audit harness.
- **P0-226-4.** Does NOT commit any vendor-prefixed test fixture
  tokens; neutral `test-*` only.

## Skill mix (3-5)

1. sqlc + recursive CTE — upstream graph query.
2. Go API handler — extend `/v1/anchors` response shape.
3. Next.js App Router + shadcn/ui Table — add column.
4. Vitest + Playwright — coverage.
