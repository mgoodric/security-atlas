import { defineConfig, globalIgnores } from "eslint/config";
import nextVitals from "eslint-config-next/core-web-vitals";
import nextTs from "eslint-config-next/typescript";

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  // Override default ignores of eslint-config-next.
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Slice 060: Playwright spec authored ahead of @playwright/test
    // install. The file is a stub-shape contract today.
    "e2e/**",
    // Generated Playwright output. Already gitignored (.gitignore:108) and
    // tracked in zero files, but eslint does not read .gitignore, so a run of
    // the e2e suite leaves ~3 MB of vendored trace bundles that lint as 164
    // errors. Those errors then fail the pre-push hook for every branch in the
    // repo, including pushes from other worktrees, until the directory is
    // deleted by hand. Found 2026-08-29 while recovering stranded work.
    "playwright-report/**",
    "test-results/**",
  ]),
  // Slice 370 (AC-5) — soft cap on the per-domain api client modules to
  // prevent the god-file from re-accreting. The former `web/lib/api.ts`
  // was 2901 LOC / 219 exports (slice 328 H-2); after the per-domain
  // split every module is well under 600 LOC. A hard `error` (decision
  // D4) keeps it that way: a new domain that grows past 600 lines must
  // be split rather than appended to. Test files are exempt — fixtures
  // legitimately run long. `skipBlankLines`/`skipComments` keep the cap
  // measuring real code, matching the spirit of the Go-side review.
  {
    files: ["lib/api/**/*.ts"],
    ignores: ["lib/api/**/*.test.ts"],
    rules: {
      "max-lines": [
        "error",
        { max: 600, skipBlankLines: true, skipComments: true },
      ],
    },
  },
]);

export default eslintConfig;
