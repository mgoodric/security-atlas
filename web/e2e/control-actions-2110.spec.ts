// #2110 — the control page's actions against the real platform: Evaluate
// now, Download bundle and Upload new version. No route is mocked.
//
// SETUP: the spec makes its own control by uploading a bundle through the
// BFF (the same upload the page's form uses), with anchor_fit: none so it
// needs no SCF catalog in the e2e database, and a per-run bundle_id so a
// re-run never collides with an earlier one. It signs in to a tenant of
// its own (fixtures/e2e/control-actions.sql): in the shared demo tenant a
// new active control changes what specs running in parallel count. The
// credential is a tenant admin, so it may evaluate and upload; the Go tier
// covers who else may (owner) and who may not.
//
// Hard rule (P0-A9 / GitGuardian): every id and string is neutral.

import { gunzipSync, gzipSync } from "node:zlib";

import { expect, test, type Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";
import { seedFromFixture } from "./seed";

const ACTIONS_TENANT = "00000000-0000-0000-0000-000000002110";

// signInToActionsTenant mints an admin bearer for the spec's tenant
// through the env-gated /v1/test/issue-jwt (slice 201) and sets it as the
// session cookie, as sidebar-nav.spec.ts does for its tenant.
async function signInToActionsTenant(page: Page): Promise<void> {
  const url = `${
    process.env.ATLAS_HTTP_URL ?? "http://localhost:8080"
  }/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: ACTIONS_TENANT,
      roles: ["admin"],
      super_admin: true,
    }),
  });
  if (!res.ok) {
    throw new Error(
      `#2110: ${url} returned ${res.status}: ${await res.text()}`,
    );
  }
  const { token } = (await res.json()) as { token?: string };
  if (!token) throw new Error(`#2110: ${url} returned 200 but no token field`);
  const base = new URL(
    process.env.PLATFORM_BASE_URL ?? "http://localhost:3000",
  );
  await page.context().clearCookies();
  await page.context().addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: token,
      domain: base.hostname,
      path: "/",
      httpOnly: true,
      secure: base.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

// tarGz builds a gzip-tar holding one regular file at the archive root:
// the bundle shape POST /v1/controls:upload-bundle reads.
function tarGz(name: string, content: string): Buffer {
  const body = Buffer.from(content, "utf8");
  const header = Buffer.alloc(512, 0);
  header.write(name, 0, 100, "utf8");
  header.write("0000644\0", 100, 8, "ascii");
  header.write("0000000\0", 108, 8, "ascii");
  header.write("0000000\0", 116, 8, "ascii");
  header.write(
    body.length.toString(8).padStart(11, "0") + "\0",
    124,
    12,
    "ascii",
  );
  header.write("00000000000\0", 136, 12, "ascii");
  header.write("        ", 148, 8, "ascii");
  header.write("0", 156, 1, "ascii");
  header.write("ustar\0", 257, 6, "ascii");
  header.write("00", 263, 2, "ascii");
  let sum = 0;
  for (const b of header) sum += b;
  header.write(sum.toString(8).padStart(6, "0") + "\0 ", 148, 8, "ascii");
  const pad = Buffer.alloc((512 - (body.length % 512)) % 512, 0);
  return gzipSync(Buffer.concat([header, body, pad, Buffer.alloc(1024, 0)]));
}

// untarFirst reads the first file of a gzip-tar: its name and content.
function untarFirst(archive: Buffer): { name: string; content: string } {
  const tar = gunzipSync(archive);
  const name = tar
    .subarray(0, 100)
    .toString("utf8")
    .replace(/\0[\s\S]*$/, "");
  const size = parseInt(tar.subarray(124, 136).toString("ascii").trim(), 8);
  return { name, content: tar.subarray(512, 512 + size).toString("utf8") };
}

function manifest(bundleID: string, title: string): string {
  return `bundle_schema_version: "1"
bundle_id: ${bundleID}
title: "${title}"
anchor_fit: none
anchor_fit_note: "e2e fixture control for the #2110 control actions; no SCF anchor"
implementation_type: automated
freshness_class: daily
evidence_queries:
  - id: payload_encrypted
    language: jsonpath
    expression: "$.encrypted"
`;
}

async function upload(page: Page, archive: Buffer) {
  const res = await page.request.post("/api/controls/upload-bundle", {
    multipart: {
      "bundle.tar.gz": {
        name: "bundle.tar.gz",
        mimeType: "application/gzip",
        buffer: archive,
      },
    },
  });
  expect(res.ok(), await res.text()).toBe(true);
  return (await res.json()) as { control_id: string; superseded_id?: string };
}

// evaluationCount reads the control's 30-day evaluation total. #2140
// retired the page's Effectiveness KPI, so it reads the same BFF the KPI
// read, with the page's own session.
async function evaluationCount(page: Page, controlID: string): Promise<number> {
  const res = await page.request.get(
    `/api/controls/${encodeURIComponent(controlID)}/effectiveness`,
  );
  if (!res.ok()) return -1;
  const body = (await res.json()) as { total_count?: number };
  return typeof body.total_count === "number" ? body.total_count : -1;
}

test.describe("control page actions (#2110)", () => {
  test.beforeAll(() => {
    seedFromFixture("control-actions");
  });

  test("Evaluate now, Download bundle and Upload new version", async ({
    page,
  }) => {
    test.setTimeout(120_000);
    await signInToActionsTenant(page);
    const bundleID = `e2e_2110_${Date.now().toString(36)}`;
    const v1 = manifest(bundleID, "Control actions e2e v1");
    const created = await upload(page, tarGz("control.yaml", v1));

    await page.goto(`/controls/${created.control_id}`);
    const actions = page.getByTestId("control-header-actions");
    await expect(actions).toBeVisible({ timeout: 30_000 });

    // The retired placeholders are gone.
    await expect(
      page.getByRole("button", { name: /Run query|Edit YAML/ }),
    ).toHaveCount(0);

    // Evaluate now: the platform appends an evaluation and the page's
    // state moves with it.
    await expect
      .poll(() => evaluationCount(page, created.control_id), {
        timeout: 30_000,
      })
      .toBeGreaterThanOrEqual(0);
    const before = await evaluationCount(page, created.control_id);
    // Hold the request briefly (it still reaches the platform) so the
    // pending label is observable.
    await page.route(
      `**/api/controls/${created.control_id}/evaluate`,
      async (route) => {
        await new Promise((r) => setTimeout(r, 750));
        await route.continue();
      },
    );
    const evaluated = page.waitForResponse(
      (r) =>
        r.url().endsWith(`/api/controls/${created.control_id}/evaluate`) &&
        r.request().method() === "POST",
    );
    await page.getByTestId("control-action-evaluate").click();
    await expect(page.getByTestId("control-action-evaluate")).toHaveText(
      "Evaluating…",
    );
    const evalRes = await evaluated;
    expect(evalRes.status()).toBe(200);
    const state = (await evalRes.json()) as {
      states: { trigger: string }[];
    };
    expect(state.states.length).toBeGreaterThan(0);
    expect(state.states[0]?.trigger).toBe("manual");
    await expect(page.getByTestId("control-action-evaluate")).toHaveText(
      "Evaluate now",
    );
    await expect
      .poll(() => evaluationCount(page, created.control_id), {
        timeout: 30_000,
      })
      .toBeGreaterThan(before);

    // Download bundle: a gzip-tar with control.yaml at the root, the
    // manifest uploaded, which uploads back as a valid bundle.
    const downloading = page.waitForEvent("download");
    await page.getByTestId("control-action-download-bundle").click();
    const download = await downloading;
    expect(download.suggestedFilename()).toBe(`${bundleID}.tar.gz`);
    const path = await download.path();
    const { readFileSync } = await import("node:fs");
    const archive = readFileSync(path);
    const file = untarFirst(archive);
    expect(file.name).toBe("control.yaml");
    expect(file.content).toBe(v1);
    const reupload = await upload(page, archive);
    expect(reupload.control_id).toBe(created.control_id);

    // Upload new version: a changed title supersedes the control, and the
    // page moves to the new version.
    await page.getByTestId("control-action-upload-toggle").click();
    await page.getByTestId("control-upload-file").setInputFiles({
      name: "bundle.tar.gz",
      mimeType: "application/gzip",
      buffer: tarGz(
        "control.yaml",
        manifest(bundleID, "Control actions e2e v2"),
      ),
    });
    const uploaded = page.waitForResponse(
      (r) =>
        r.url().endsWith("/api/controls/upload-bundle") &&
        r.request().method() === "POST",
    );
    await page.getByTestId("control-upload-submit").click();
    const upRes = await uploaded;
    expect(upRes.status()).toBe(200);
    const result = (await upRes.json()) as {
      control_id: string;
      superseded_id?: string;
    };
    expect(result.superseded_id).toBe(created.control_id);
    expect(result.control_id).not.toBe(created.control_id);
    await expect(page).toHaveURL(
      new RegExp(`/controls/${result.control_id}$`),
      {
        timeout: 30_000,
      },
    );
  });
});
