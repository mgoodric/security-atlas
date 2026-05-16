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
      "proxy.test.ts",
    ],
    exclude: ["**/node_modules/**", "**/.next/**", "**/dist/**", "e2e/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      reportsDirectory: "./coverage",
      include: [
        "lib/**/*.ts",
        "app/api/**/*.ts",
        "app/[(]authed[)]/**/*.ts",
        "proxy.ts",
      ],
      exclude: [
        "**/*.test.ts",
        "**/*.d.ts",
        "lib/**/*.tsx",
        "app/api/**/*.tsx",
        "app/[(]authed[)]/**/*.tsx",
      ],
    },
  },
});
