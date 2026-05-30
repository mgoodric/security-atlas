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
    // Slice 348 V-2: collapsed include to a generic `**/*.test.ts`
    // glob. vitest walks directories regardless of literal-paren
    // naming, so the prior escape-bracket entry for `app/(authed)/`
    // is no longer needed. New colocated test directories
    // (lib/**, app/api/**, app/(authed)/**, app/audit-log/**,
    // scripts/**, e2e-audit/lib/**, components/**, plus
    // proxy.test.ts and next-config.test.ts at workspace root)
    // are auto-included by the directory walk. The exclude block
    // below keeps Playwright spec files (`e2e/**`,
    // `e2e-audit/**/*.spec.ts`) and build artifacts out of the
    // vitest runner — those run via the `playwright test` runner.
    //
    // Discipline preserved from the prior explicit list:
    //   * No JSX in vitest — slice 069 P0-A3. The narrow `**/*.test.ts`
    //     pattern (NOT `.test.tsx`) keeps JSX view modules out of the
    //     node-env runner.
    //   * Workspace root tests (proxy.test.ts, next-config.test.ts)
    //     are covered by the generic glob.
    //   * `scripts/**` README-screenshot safety-gate tests (slice
    //     132 information-disclosure mitigation) covered.
    //   * `e2e-audit/lib/**` UI-honesty pure-logic modules covered;
    //     `e2e-audit/**/*.spec.ts` excluded so the Playwright spec
    //     for that harness runs via the dedicated job.
    //   * `components/**` shared helpers (slice 183) covered; `.tsx`
    //     view modules are not matched.
    include: ["**/*.test.ts"],
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
