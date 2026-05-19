# 150 — Empty-set robustness audit: list endpoints return 500 on fresh install

**Cluster:** Backend / Quality
**Estimate:** 1-2d
**Type:** AFK
**Status:** `ready`

## Narrative

Surfaced 2026-05-18 from operator report on v1.10.0 Unraid — three separate panels return 500 on a fresh install with empty database:

> "Under recent drift I see 'Could not load this panel · 500 Internal Server Error'"
> "Metrics shows 'Could not load board metrics · 500 Internal Server Error'"
> "The policies page shows 'Could not load policies · 500 Internal Server Error'"

This is a SHARED bug shape: list / aggregate endpoints assume non-empty result sets and panic / fail on empty data. Fresh install = empty database = every list endpoint that doesn't handle the 0-row case returns 500.

**What this slice ships:**

- Audit ALL `/v1/*` list + aggregate endpoints for empty-set handling. Pattern: `rows[0]` access without bounds check; nil-deref in aggregation logic; `division by zero` from rate / percentage calcs; missing empty-string-vs-missing-row distinction.
- Fix each endpoint that 500s on empty: return `{items: []}` with 200 + appropriate headers.
- Integration test per endpoint exercising the 0-row path.
- Document the convention in `CONTRIBUTING.md`: "List endpoints MUST return empty array, not 500, on 0 rows."

**Known affected endpoints (operator-confirmed):**

- `GET /v1/controls/drift` (slice 016) — Recent drift panel
- `GET /v1/board/metrics` or similar (slice 076) — Board metrics panel
- `GET /v1/policies` (slice 022) — Policies page

**Suspected additional endpoints worth auditing:**

- `GET /v1/risks`
- `GET /v1/evidence`
- `GET /v1/audit-periods`
- `GET /v1/board` (board packs list)
- `GET /v1/vendors`
- `GET /v1/admin/users`

## Acceptance criteria

- [ ] AC-1: Audit grep for `rows[0]` / `data[0]` / unchecked aggregations across `internal/api/*/handlers.go`. Document findings in decisions log.
- [ ] AC-2: Fix `/v1/controls/drift` empty-set handling; integration test.
- [ ] AC-3: Fix `/v1/board/metrics` (or whichever endpoint backs the metrics panel) empty-set handling; integration test.
- [ ] AC-4: Fix `/v1/policies` empty-set handling; integration test.
- [ ] AC-5: For each ADDITIONAL endpoint surfaced by AC-1, fix + test.
- [ ] AC-6: Add empty-install Playwright e2e: log into a fresh-seed deployment; navigate to dashboard + metrics + policies + drift; assert each page loads without 500.
- [ ] AC-7: NEW section in CONTRIBUTING.md: "Empty-set robustness — list endpoints return empty array, never 500, on 0 rows. Integration test required."
- [ ] AC-8: CHANGELOG entry: "Fixed list endpoints returning 500 on fresh install (drift, metrics, policies, etc.) (#150)".

## Threat model

| STRIDE                       | Threat                                                                                                                                                    | Mitigation                                                                                                                                  |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| **D** DoS                    | The 500 errors leak server-side internal state (stack traces in dev mode; null-deref signatures could give attackers reconnaissance about app structure). | Return generic 200 with empty array; no stack traces in error responses (slice 087 hardening already covers); no internal pathnames leaked. |
| **I** Information disclosure | Same as above.                                                                                                                                            | Same.                                                                                                                                       |

## Dependencies

- **#016** Drift backend (merged) — fix.
- **#022** Policies backend (merged) — fix.
- **#076** Metrics backend (merged) — fix.
- Various other list endpoints — audit + fix as needed.

## Anti-criteria (P0 — block merge)

- **P0-EMPTY-1** EVERY shipped `/v1/*` list endpoint MUST return 200 with empty array on 0 rows. NO 500 path.
- **P0-EMPTY-2** Integration test per fixed endpoint exercising the 0-row path is merge-blocking.
- **P0-EMPTY-3** CONTRIBUTING.md convention added — future endpoints inherit the rule.
- **P0-EMPTY-4** NO scope creep into adding new endpoints (this is a fix slice).
- **P0-EMPTY-5** NO vendor-prefixed test fixture tokens.

## Notes for the implementing agent

Pattern-shaped fix. Engineer at pickup should grep aggressively for the failure shape, not just fix the 3 operator-confirmed endpoints. Goal: every list endpoint inherits the empty-array convention.

This blocks every fresh-install user. Triage HIGH.

Provenance: filed 2026-05-18 from operator v1.10.0 report.
