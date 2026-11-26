// #2344 — the upload BFFs read their body through readBoundedUpload
// (web/lib/api/upload.ts), which refuses a body without a Content-Length
// (411) and cuts one off past the route's cap (413). This spec pins the
// other side against the real stack: a browser-shaped multipart upload,
// which always declares its length, still goes through each BFF to the
// platform. No route is mocked.
//
// The check is that the platform answered: not the BFF's 411/413, and not
// a BFF failure. The rows a full happy path needs are not in the e2e
// stack: it runs without an artifact store (so /v1/artifacts:upload is
// not mounted, 404, and a walkthrough attachment is 503 "artifact store
// not configured"), and this spec seeds no staged framework version,
// questionnaire or walkthrough. Those platform answers still prove the
// upload crossed the BFF. The BFF relays a platform 5xx with a generic
// body (#2704), so the 503 shows as that body: only a relayed platform
// answer carries it, the BFF's own failure is 502 "upstream unavailable".
// The 411 and 413 paths, including a lying
// Content-Length, are pinned in vitest (app/api/upload-caps.test.ts).
// controls/upload-bundle has a real happy path in
// control-actions-2110.spec.ts.
//
// Hard rule (P0-A9 / GitGuardian): every id and string is neutral.

import { expect, test } from "./fixtures";

const UNKNOWN_ID = "b2344000-0000-4000-8000-0000000000ff";

// The platform's 503 when the stack has no artifact store, as the BFF
// relays it: UPSTREAM_5XX_BODY in web/lib/api/bff.ts (#2704).
const RELAYED_5XX = JSON.stringify({ error: "platform error" });

const file = (name: string, mimeType: string, body: string) => ({
  name,
  mimeType,
  buffer: Buffer.from(body),
});

const uploads: { name: string; path: string; form: () => object }[] = [
  {
    name: "artifact upload",
    path: "/api/artifacts/upload",
    form: () => ({
      file: file("e2e-2344.txt", "text/plain", "e2e #2344 artifact body"),
    }),
  },
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

test.describe("upload BFFs pass a browser-shaped upload through (#2344)", () => {
  for (const u of uploads) {
    test(`${u.name} reaches the platform`, async ({ authedPage: page }) => {
      const res = await page.request.post(u.path, {
        multipart: u.form() as never,
      });
      const status = res.status();
      const text = await res.text();
      expect(status, text).not.toBe(411);
      expect(status, text).not.toBe(413);
      if (status >= 500) {
        expect(status, text).toBe(503);
        expect(text).toBe(RELAYED_5XX);
      }
    });
  }
});
