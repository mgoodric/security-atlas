// Slice 069 — vitest configuration for the web workspace.
//
// Scope:
//   * Module-level tests only (no React component rendering this slice —
//     P0-A3 in slice 069 anti-criteria; @testing-library/react is NOT a
//     dependency). Tests live alongside the modules under test:
//       web/lib/**/*.test.ts
//       web/app/api/**/*.test.ts
//       web/app/(authed)/**/*.test.ts   (slice 098: page-local pure
//                                        logic — filter narrowing,
//                                        row-join helpers — colocated
//                                        with the page that consumes
//                                        them; node env, no JSX in the
//                                        module under test)
//       web/app/audit-log/**/*.test.ts  (slice 130: same precedent —
//                                        the layout exports its
//                                        route-guard predicate
//                                        `canReachAuditLog` as pure
//                                        logic so a fail-closed
//                                        regression is covered without
//                                        a server-component harness)
//   * Test environment is `node` (not jsdom): every covered module is
//     either runtime-agnostic (lib/api.ts URL resolution) or server-only
//     (BFF route handlers, lib/api/bff.ts).
//   * Playwright e2e specs (web/e2e/**) are excluded — they run via the
//     `playwright test` runner, not vitest.
//
// Slice 347 — per-file coverage ratchet. The `coverage.thresholds` map
// is read from `coverage-thresholds.json` (sibling file, mirrors the
// Go-side `cmd/scripts/coverage-thresholds.json` shape). Each floor =
// max(0, floor(measured - 2pp)). The ratchet is enforced by vitest's
// own threshold-check; the existing CI `Frontend · vitest` job fails
// red on any per-file regression below floor. Closes slice 334 V-1
// (HIGH) and slice 069's deferred follow-up "raise the bar" promise.

import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const dirname = fileURLToPath(new URL(".", import.meta.url));

// Slice 347 — load per-file thresholds. The JSON file is the canonical
// source so that ratchet lifts are a clean numerical diff, not a TS
// edit. Metadata keys ($comment, $methodology, $how_to_raise, etc.)
// are stripped before handing the map to vitest; only `thresholds` is
// forwarded.
type FileThresholds = {
  statements: number;
  branches: number;
  functions: number;
  lines: number;
};
const thresholdsFile = JSON.parse(
  readFileSync(resolve(dirname, "coverage-thresholds.json"), "utf8"),
) as { thresholds: Record<string, FileThresholds> };
const perFileThresholds: Record<string, FileThresholds> =
  thresholdsFile.thresholds;

export default defineConfig({
  resolve: {
    // Mirror the `@/*` path alias from tsconfig.json so test files can
    // `import { apiBaseURL } from "@/lib/api"` like production code does.
    alias: {
      "@": resolve(dirname, "."),
    },
  },
  test: {
    environment: "node",
    globals: false,
    // Slice 092: proxy.test.ts lives at the web root because Next.js 16
    // requires `proxy.ts` (the renamed middleware) to sit at the
    // workspace root, alongside next.config.ts. The test mirrors that
    // placement so it stays colocated with its subject.
    include: [
      "lib/**/*.test.ts",
      "app/api/**/*.test.ts",
      // Route-group directory `(authed)` has literal parens that glob
      // engines treat as group syntax — escape with brackets so vitest
      // walks into it. The escaped pattern matches a directory named
      // exactly `(authed)`.
      "app/[(]authed[)]/**/*.test.ts",
      // Slice 130: the /audit-log layout exports its route-guard
      // predicate as pure logic. Covered without a route-group wrapper,
      // so no escape is needed.
      "app/audit-log/**/*.test.ts",
      "proxy.test.ts",
      // Slice 208: next-config.test.ts lives at the web root because
      // next.config.ts must sit at the workspace root (Next.js
      // convention). Same colocation precedent as proxy.test.ts above.
      "next-config.test.ts",
      // Slice 132: the README-screenshot capture pipeline's safety
      // gate (assertCaptureSafe + isLoopbackOrPrivate) is the
      // load-bearing information-disclosure mitigation per the slice
      // 132 threat model. The test file exercises 13 branches of the
      // gate so a refactor cannot widen the admit set silently — a
      // public-IP slip would publish real customer data to the public
      // README permanently.
      "scripts/**/*.test.ts",
      // Slice 178: the UI honesty audit harness's pure-logic modules
      // (mockup-diff categorization + read-only guardrail detection +
      // manifest validator) are covered here as node-env vitest. The
      // Playwright spec at `e2e-audit/ui-honesty.spec.ts` is excluded
      // from vitest (`exclude: e2e-audit/**/*.spec.ts` below) and runs
      // via the `Frontend · UI honesty (advisory)` job instead.
      "e2e-audit/lib/**/*.test.ts",
      // Slice 183: pure-logic helpers that two or more components
      // share are colocated under `components/<area>/` (the helper
      // can't live in app/(authed)/<route>/ because it must be
      // importable by sibling components). The include is intentionally
      // narrow — only `.test.ts` (no `.test.tsx`), matching the
      // node-env / no-JSX precedent — so the JSX view modules are
      // never accidentally entered by the unit runner.
      "components/**/*.test.ts",
    ],
    exclude: [
      "**/node_modules/**",
      "**/.next/**",
      "**/dist/**",
      "e2e/**",
      "e2e-audit/**/*.spec.ts",
    ],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      reportsDirectory: "./coverage",
      include: [
        "lib/**/*.ts",
        "app/api/**/*.ts",
        "app/[(]authed[)]/**/*.ts",
        "app/audit-log/**/*.ts",
        "proxy.ts",
        // Slice 183: shared component-area pure-logic modules
        // (e.g. components/calendar/link-for.ts) — see vitest
        // `include` block for the discipline.
        "components/**/*.ts",
      ],
      exclude: [
        "**/*.test.ts",
        "**/*.d.ts",
        "lib/**/*.tsx",
        "app/api/**/*.tsx",
        "app/[(]authed[)]/**/*.tsx",
        "app/audit-log/**/*.tsx",
        "components/**/*.tsx",
      ],
      // Slice 347 — per-file ratchet. vitest matches each glob key with
      // micromatch against the file's path relative to the workspace
      // root (cwd). Each value is a {statements, branches, functions,
      // lines} floor seeded at floor(measured - 2pp). vitest's built-in
      // check fails red on any per-file regression. To raise a floor,
      // write the tests AND lift the number in coverage-thresholds.json
      // in the same PR (slice 069 contract; P0-347-1 monotonic ↑).
      thresholds: {
        // autoUpdate: false (default) — slice 347 D3 defers automation
        // to a follow-up so the first round of ratchet hygiene is
        // hand-curated and reviewable.
        ...perFileThresholds,
      },
    },
  },
});
