# Frontend testing — vitest vs Playwright

The web workspace has two test surfaces. They cover different things and have different trade-offs. Use the right one.

## vitest (module-level)

- **Runner:** `vitest` (Vite-native, jest-compatible API)
- **Environment:** `node` — no jsdom, no DOM, no React rendering. Module-level only this slice.
- **Scope:** Modules under `web/lib/**` and `web/app/api/**/*.ts` route handlers. Pure logic — URL resolution, bearer-cookie forwarding, status-code translation, error-shape contracts.
- **Why module-level only:** Component tests need `@testing-library/react` + jsdom + DOM polyfills. That's a separate dependency surface and a separate skill mix; we deferred it (slice 069 P0-A3).
- **Speed:** < 1 second for the full seed (currently 14 tests across 3 files).
- **Run locally:** `cd web && npm run test`
- **Run with coverage:** `cd web && npm run test:coverage` → writes `web/coverage/coverage-summary.json`.
- **CI:** the `Frontend · vitest` job runs every PR that touches `web/` (slice-061 path-filter pattern).
- **Adding a test:** drop a `*.test.ts` file next to the module under test. The vitest `include` glob picks it up automatically.

## Playwright (end-to-end)

- **Runner:** `playwright test`
- **Browser:** chromium only this slice (firefox + webkit deferred — slice 069 P0-A1).
- **Scope:** User-visible flows that traverse the BFF, the RSC layer, the platform's HTTP API, and Postgres. The tests run against a real running stack (locally: `docker compose up`; in CI: the service-container bring-up inside the `Frontend · Playwright e2e` job).
- **Why end-to-end:** The BFF + RSC + platform interplay has assertable contracts (panel-level error isolation, fetch-routing per environment, role-gated 403 surfaces) that a module unit test cannot exercise. Playwright is the only place we run the integration.
- **Speed:** ~minutes for the full suite. Use for high-value flows, not for every edge case.
- **Run locally:** see `web/e2e/README.md`.
- **CI:** the `Frontend · Playwright e2e` job runs every PR that touches `web/` or platform-affecting paths.
- **Adding a test:** see `web/e2e/README.md` — same pattern as existing specs; mirror their `data-testid` discipline.

## When to reach for each

| Question                                                    | Tool       |
| ----------------------------------------------------------- | ---------- |
| "Does `apiBaseURL()` honor `ATLAS_HTTP_URL`?"               | vitest     |
| "Does the BFF return 401 with no session cookie?"           | vitest     |
| "Does an upstream 403 translate to `{ is_admin: false }`?"  | vitest     |
| "Does the dashboard render six panels?"                     | Playwright |
| "Does a failing endpoint degrade only its own panel?"       | Playwright |
| "Does the auditor see the private note that the auditee doesn't?" | Playwright |

If a behavior can be expressed as a module input/output, vitest. If it requires a browser viewing a built page hitting a running platform, Playwright. If both, write both — the vitest version catches regressions fast, the Playwright version proves the integration.
