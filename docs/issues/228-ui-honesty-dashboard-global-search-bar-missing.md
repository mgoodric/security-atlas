# 228 — UI honesty: dashboard global command-K search bar missing from topbar

**Cluster:** Quality / UI parity (frontend)
**Estimate:** 0.5d
**Type:** AFK
**Status:** `not-ready` (depends on a real backing search endpoint — see Dependencies)

## Narrative

Surfaced during the slice 204 per-page UI parity audit fleet (page slug: `dashboard`; mockup file: `Plans/mockups/dashboard.html`). Category (i) layout / chrome parity.

The dashboard mockup's topbar (`Plans/mockups/dashboard.html` lines 43–47) renders a 256px-wide global search input pinned to the right of the topbar, with a `⌘K` keyboard-shortcut badge and the placeholder `"Search controls, evidence, risks…"`. The mockup positions it as a primary navigation affordance — a fast-path to jump across the control / evidence / risk graphs without sidebar traversal.

The live `/dashboard` topbar (`web/components/shell/topbar.tsx`) does NOT render any search input. Operators landing on the dashboard have no global-search affordance at all — they must use the sidebar to reach each surface and then page through. The mockup's "primary affordance" is unrepresented in the shipped UI.

This is a parity gap, not a slice-178 HONESTY-GAP (the live UI doesn't lie about having search — it just lacks the chrome). But the parity gap is load-bearing because the dashboard is the primary-user (solo security leader) morning home screen; the inability to ⌘K-jump to a control by SCF anchor is a daily friction point.

**Why `not-ready`.** Wiring the input requires a backing search endpoint (controls + evidence + risks index). No such endpoint exists today — slice 064 ships controls search only, slice 050 ships evidence search only, no risks search ships yet. The slice needs (a) a unified `GET /v1/search?q=…` returning typed hits across the three primitives, or (b) a UI-side multi-fetch fan-out + result-merging strategy. Either is its own design surface beyond this audit-spillover; this slice tracks the gap, the implementing slice will choose the shape.

## Threat model

**S — Spoofing.** No new auth surface. Search reuses the existing session bearer for tenant scoping.

**T — Tampering.** A search endpoint must respect RLS (invariant #6) — Tenant A search results MUST NOT include Tenant B records. Implementing slice files a SQL-level audit per existing four-policy pattern.

**I — Info disclosure.** Search must NOT leak control / evidence / risk titles across tenants. Mitigation: the implementing slice's RLS test asserts cross-tenant zero-leak.

**D — DoS.** Unbounded text search against the control catalog (~1400 SCF anchors) + evidence ledger (potentially millions of records) needs a server-side LIMIT and a pg_trgm index. Implementing slice owns that.

**Verdict.** **needs-mitigations.** Server-side RLS test + LIMIT + index. None block this audit-spillover; they block the implementing slice.

## Acceptance criteria

- **AC-1.** A search input renders in the topbar matching the mockup's placement (right-pinned, 256px wide, `⌘K` shortcut badge, placeholder "Search controls, evidence, risks…").
- **AC-2.** Pressing `⌘K` (macOS) or `Ctrl+K` (other) focuses the input from anywhere in the (authed) app.
- **AC-3.** Typed input fans out to a controls + evidence + risks search; results render in a dropdown grouped by primitive, with the same `data-testid="global-search-results"` shape the audit harness can assert on.
- **AC-4.** Selecting a result navigates to its detail page (`/controls/<id>`, `/evidence/<id>`, `/risks/<id>`).
- **AC-5.** Empty state renders honestly: "No matches" rather than a forward-looking placeholder.
- **AC-6.** Cross-tenant RLS test passes — Tenant A search returns zero Tenant B records even with overlapping keywords.

## Constitutional invariants honored

- **Invariant 6 (RLS at the DB layer).** Search queries route through the same RLS-scoped DB role as every other tenant-scoped read; the four-policy pattern applies.
- **Anti-pattern rejection (`Plans/canvas/01-vision.md` §1.6).** The mockup is honest about what the affordance does (search across three real primitives); no "AI-suggested results" without backing — that's a separate slice if/when it ships.

## Canvas references

- `Plans/canvas/01-vision.md` — solo-security-leader persona, morning home screen
- `Plans/canvas/02-primitives.md` — Control / Evidence / Risk as searchable primitives

## Dependencies

- **A unified `/v1/search` endpoint OR a UI-side fan-out design.** Neither exists today. The implementing slice picks one; this audit-spillover does NOT pre-commit the shape.
- **Slice 064** (controls search) — merged. Reusable.
- **Slice 050** (evidence search) — merged. Reusable.
- **No risks search slice exists yet** — the implementing slice files one as a precondition OR builds it inline.

## Anti-criteria (P0 — block merge)

- **P0-A1.** DOES NOT ship search as a placeholder that returns hardcoded results.
- **P0-A2.** DOES NOT skip the cross-tenant RLS test.
- **P0-A3.** DOES NOT add the input without a working backend — visible-but-non-functional is the HONESTY-GAP this slice is correcting against.

## Surfaced by

Slice 204 dashboard audit (parent). See `docs/audit-log/204-page-audit-dashboard.md` finding F-204D-1.
