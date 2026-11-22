// #2903 — the demo seed's S3 evidence renders on /evidence, against the
// real backend.
//
// admin-demo.spec.ts mocks the admin API, so nothing else checks that
// seeded evidence reaches the list. This spec runs the real seed
// (`atlas-cli demo seed --tenant-slug e2e-demoseed-2903`) into a tenant of
// its own, reads back from Postgres which controls hold S3
// bucket-encryption rows and with what results, and asserts the list
// renders exactly those rows, kind and result, for each control.
//
// The seed is idempotent: on a re-run against the same database it finds
// its tenant and changes nothing. It needs the Go toolchain (`go run`),
// psql, and DATABASE_URL as a BYPASSRLS DSN; CI's Playwright job has all
// three. DATABASE_URL_APP, when set, lets the seed run the post-seed
// evaluation too; the list itself reads only the ledger.
//
// Run locally:
//   cd web
//   npx playwright test e2e/demoseed-evidence-2903.spec.ts

import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";
import { signInToTenant } from "./tenant-session";

const SLUG = "e2e-demoseed-2903";
const S3_KIND = "aws.s3.bucket_encryption_state.v1";
const REPO_ROOT = resolve(__dirname, "..", "..");

type SeededControl = { id: string; title: string; results: string[] };

function databaseURL(): string {
  const url = process.env.DATABASE_URL;
  if (!url) {
    throw new Error(
      "DATABASE_URL is required; CI sets it for the Playwright job, local devs must export it",
    );
  }
  return url;
}

function psql(sql: string): string {
  return execFileSync(
    "psql",
    [databaseURL(), "-v", "ON_ERROR_STOP=1", "-tA", "-F", "|", "-c", sql],
    { encoding: "utf8" },
  ).trim();
}

// The table branch only: at `mobileMode="cards"` the list also mounts a
// hidden card stack that repeats every cell testid.
function table(page: Page) {
  return page.getByTestId("list-table-wrap");
}

test.describe("demo seed evidence renders on /evidence (#2903)", () => {
  // One worker runs the file, so beforeAll seeds once rather than once
  // per worker racing on the same slug.
  test.describe.configure({ mode: "serial" });

  let tenantId = "";
  let controls: SeededControl[] = [];

  test.beforeAll(() => {
    // `go run` compiles atlas-cli first; allow for a cold build cache.
    test.setTimeout(600_000);
    execFileSync(
      "go",
      ["run", "./cmd/atlas-cli", "demo", "seed", "--tenant-slug", SLUG],
      {
        cwd: REPO_ROOT,
        stdio: "inherit",
        env: {
          ...process.env,
          ATLAS_ENABLE_DEMO_SEED: "true",
          // The e2e database already holds other tenants.
          ATLAS_ALLOW_MULTI_TENANT: "true",
        },
      },
    );

    tenantId = psql(`SELECT id FROM tenants WHERE slug = '${SLUG}'`);
    expect(tenantId, `no tenant with slug ${SLUG} after the seed`).toMatch(
      /^[0-9a-f-]{36}$/,
    );

    // Every control holding S3 rows, with its rows' results. The seed
    // gives each control one kind, so these controls hold nothing else.
    const lines = psql(`
      SELECT c.id, c.title, string_agg(e.result::text, ',' ORDER BY e.result::text)
        FROM evidence_records e
        JOIN controls c ON c.id = e.control_id
       WHERE e.tenant_id = '${tenantId}' AND e.evidence_kind = '${S3_KIND}'
       GROUP BY c.id, c.title
       ORDER BY c.title`);
    controls = lines
      .split("\n")
      .filter(Boolean)
      .map((line) => {
        const [id, title, results] = line.split("|");
        return { id, title, results: results.split(",") };
      });
    expect(controls.length, "the seed wrote no S3 rows").toBeGreaterThan(0);
  });

  test.beforeEach(async ({ page }) => {
    await signInToTenant(page, {
      tenantId,
      roles: ["admin"],
      superAdmin: true,
    });
  });

  test("each S3 control lists its seeded rows with their kind and result", async ({
    page,
  }) => {
    // One page load per S3 control.
    test.setTimeout(120_000);
    for (const control of controls) {
      await page.goto(`/evidence?since_preset=all&control_id=${control.id}`);
      const rows = table(page).getByTestId("list-table-row");
      await expect(rows, control.title).toHaveCount(control.results.length, {
        timeout: 30_000,
      });

      const kinds = await table(page)
        .getByTestId("evidence-row-evidence-kind")
        .allTextContents();
      expect(kinds, control.title).toEqual(control.results.map(() => S3_KIND));

      // The raw result enum rides on the cell's title attribute.
      const resultCells = table(page).getByTestId("evidence-row-result");
      const results: string[] = [];
      for (let i = 0; i < control.results.length; i++) {
        results.push((await resultCells.nth(i).getAttribute("title")) ?? "");
      }
      expect(results.sort(), control.title).toEqual(control.results);
    }
  });

  test("the encryption-at-rest control holds the S3 rows", async () => {
    // #2903: before it, kinds went round-robin and this control held
    // access-review, attestation and Okta rows instead.
    expect(controls.map((c) => c.title)).toContain(
      "Encryption-at-rest on every data store",
    );
  });
});
