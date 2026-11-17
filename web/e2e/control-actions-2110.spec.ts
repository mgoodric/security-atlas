// #2110 — the control page's actions against the real platform: Evaluate
// now, Download bundle and Upload new version. No route is mocked.
//
// SETUP: the spec makes its own control by uploading a bundle through the
// BFF (the same upload the page's form uses), with anchor_fit: none so it
// needs no SCF catalog in the e2e database, and a per-run bundle_id so a
// re-run never collides with an earlier one. Nothing seeded is mutated.
// The signed-in e2e credential is a tenant admin, so it may evaluate and
// upload; the Go tier covers who else may (owner) and who may not.
//
// Hard rule (P0-A9 / GitGuardian): every id and string is neutral.

import { gunzipSync, gzipSync } from "node:zlib";

import type { Page } from "@playwright/test";

import { expect, test } from "./fixtures";

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
  const name = tar.subarray(0, 100).toString("utf8").replace(/\0.*$/s, "");
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

// evaluationCount reads the Effectiveness KPI's "<pass>/<total> evaluations".
async function evaluationCount(page: Page): Promise<number> {
  const text = await page
    .getByTestId("kpi-card")
    .filter({ hasText: "Effectiveness" })
    .textContent();
  const m = /\d+\/(\d+) evaluations/.exec(text ?? "");
  return m ? Number(m[1]) : -1;
}

test.describe("control page actions (#2110)", () => {
  test("Evaluate now, Download bundle and Upload new version", async ({
    authedPage: page,
  }) => {
    test.setTimeout(120_000);
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
      .poll(() => evaluationCount(page), { timeout: 30_000 })
      .toBeGreaterThanOrEqual(0);
    const before = await evaluationCount(page);
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
      .poll(() => evaluationCount(page), { timeout: 30_000 })
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
