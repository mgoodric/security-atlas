// Slice 069 — vitest configuration for the web workspace.
//
// Scope:
//   * Module-level tests only (no React component rendering this slice —
//     P0-A3 in slice 069 anti-criteria; @testing-library/react is NOT a
//     dependency). Tests live alongside the modules under test:
//       web/lib/**/*.test.ts
//       web/app/api/**/*.test.ts
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
    include: ["lib/**/*.test.ts", "app/api/**/*.test.ts"],
    exclude: ["**/node_modules/**", "**/.next/**", "**/dist/**", "e2e/**"],
    coverage: {
      provider: "v8",
      reporter: ["text", "json-summary"],
      reportsDirectory: "./coverage",
      include: ["lib/**/*.ts", "app/api/**/*.ts"],
      exclude: [
        "**/*.test.ts",
        "**/*.d.ts",
        "lib/**/*.tsx",
        "app/api/**/*.tsx",
      ],
    },
  },
});
