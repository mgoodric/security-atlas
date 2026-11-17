// #2344 — the upload BFFs read their body through readBoundedUpload
// (web/lib/api/upload.ts), which refuses a body without a Content-Length
// (411) and cuts one off past the route's cap (413). This spec pins the
// other side against the real stack: a browser-shaped multipart upload,
// which always declares its length, still goes through each BFF to the
// platform. No route is mocked.
//
// The artifact upload is a full happy path (MinIO is in the e2e stack).
// The other three need rows this spec does not seed (a staged framework
// version, a questionnaire, a walkthrough), so for them the check is that
// the platform answered: not the BFF's 411/413, and not a 5xx. The 411 and
// 413 paths, including a lying Content-Length, are pinned in vitest
// (app/api/upload-caps.test.ts). controls/upload-bundle has its own real
// happy path in control-actions-2110.spec.ts.
//
// Hard rule (P0-A9 / GitGuardian): every id and string is neutral.

import { expect, test } from "./fixtures";

const UNKNOWN_ID = "b2344000-0000-4000-8000-0000000000ff";

const file = (name: string, mimeType: string, body: string) => ({
  name,
  mimeType,
  buffer: Buffer.from(body),
});

test.describe("upload BFFs pass a browser-shaped upload through (#2344)", () => {
  test("artifact upload stores the file", async ({ authedPage: page }) => {
    const res = await page.request.post("/api/artifacts/upload", {
      multipart: {
        file: file("e2e-2344.txt", "text/plain", "e2e #2344 artifact body"),
      },
    });
    expect(res.status(), await res.text()).toBe(201);
    const body = (await res.json()) as { artifact?: { id?: string } };
    expect(body.artifact?.id).toBeTruthy();
  });

  const reachesPlatform: { name: string; path: string; form: () => object }[] =
    [
      {
        name: "control import",
        path: "/api/controls/imports",
        form: () => ({
          framework: "demo-fwk",
          file: file("e2e-2344.csv", "text/csv", "id,title\nE2E-1,Example\n"),
        }),
      },
      {
        name: "questionnaire Excel import",
        path: `/api/questionnaires/${UNKNOWN_ID}/import-excel`,
        form: () => ({
          file: file(
            "e2e-2344.xlsx",
            "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
            "not a workbook",
          ),
        }),
      },
      {
        name: "walkthrough attachment",
        path: `/api/audit/walkthroughs/${UNKNOWN_ID}/attachments`,
        form: () => ({ file: file("e2e-2344.txt", "text/plain", "e2e") }),
      },
    ];

  for (const r of reachesPlatform) {
    test(`${r.name} reaches the platform`, async ({ authedPage: page }) => {
      const res = await page.request.post(r.path, {
        multipart: r.form() as never,
      });
      const status = res.status();
      const text = await res.text();
      expect(status, text).not.toBe(411);
      expect(status, text).not.toBe(413);
      expect(status, text).toBeLessThan(500);
    });
  }
});
