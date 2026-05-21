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

import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";
import { resolve } from "node:path";

const dirname = fileURLToPath(new URL(".", import.meta.url));

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
      ],
      exclude: [
        "**/*.test.ts",
        "**/*.d.ts",
        "lib/**/*.tsx",
        "app/api/**/*.tsx",
        "app/[(]authed[)]/**/*.tsx",
        "app/audit-log/**/*.tsx",
      ],
    },
  },
});
