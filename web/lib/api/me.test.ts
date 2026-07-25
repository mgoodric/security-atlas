// Slice 594 — vitest coverage for the /v1/me/preferences wire shape,
// specifically the slice-583 slack + webhook per-(event, channel) cells
// that the settings Notifications matrix now sets.
//
// The lib functions are thin fetch wrappers over the BFF at
// /api/me/preferences; the BFF is a verbatim passthrough (no channel
// validation — that lives in userprefs.Channels on the platform, which
// slice 583 widened to admit slack + webhook). These tests assert:
//
//   * getMyPreferences unwraps {preferences: {...}} and surfaces slack +
//     webhook cells unchanged (default-on-missing-row is a read-side
//     concern the page applies; the wire carries explicit booleans).
//   * patchMyPreferences forwards a {event: {slack|webhook: bool}} partial
//     verbatim as the PATCH body and unwraps the {preferences} response.
//
// Pure wire-shape tests — no React, no DOM. Global fetch is mocked.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { getMyPreferences, patchMyPreferences } from "./me";

const realFetch = globalThis.fetch;

function mockFetch(impl: typeof fetch) {
  globalThis.fetch = vi.fn(impl) as unknown as typeof fetch;
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

describe("getMyPreferences", () => {
  beforeEach(() => vi.restoreAllMocks());
  afterEach(() => {
    globalThis.fetch = realFetch;
  });

  test("unwraps the {preferences} envelope including slack + webhook cells", async () => {
    mockFetch(async () =>
      jsonResponse({
        preferences: {
          control_drift: { in_app: true, email: false, slack: false },
          risk_review_overdue: { webhook: false },
        },
      }),
    );
    const prefs = await getMyPreferences();
    expect(prefs.control_drift.slack).toBe(false);
    expect(prefs.control_drift.email).toBe(false);
    expect(prefs.control_drift.in_app).toBe(true);
    expect(prefs.risk_review_overdue.webhook).toBe(false);
  });

  test("throws an APIError on a non-OK response", async () => {
    mockFetch(async () => jsonResponse({ error: "nope" }, 500));
    await expect(getMyPreferences()).rejects.toThrow();
  });
});

describe("patchMyPreferences", () => {
  beforeEach(() => vi.restoreAllMocks());
  afterEach(() => {
    globalThis.fetch = realFetch;
  });

  test("forwards a slack per-kind cell verbatim and unwraps the response", async () => {
    let sentBody: string | undefined;
    let sentMethod: string | undefined;
    mockFetch(async (_url, init) => {
      sentBody = init?.body as string;
      sentMethod = init?.method;
      return jsonResponse({
        preferences: { control_drift: { slack: false } },
      });
    });

    const out = await patchMyPreferences({ control_drift: { slack: false } });

    expect(sentMethod).toBe("PATCH");
    expect(JSON.parse(sentBody as string)).toEqual({
      control_drift: { slack: false },
    });
    expect(out.control_drift.slack).toBe(false);
  });

  test("forwards a webhook per-kind cell verbatim", async () => {
    let sentBody: string | undefined;
    mockFetch(async (_url, init) => {
      sentBody = init?.body as string;
      return jsonResponse({
        preferences: { evidence_staleness: { webhook: true } },
      });
    });

    const out = await patchMyPreferences({
      evidence_staleness: { webhook: true },
    });

    expect(JSON.parse(sentBody as string)).toEqual({
      evidence_staleness: { webhook: true },
    });
    expect(out.evidence_staleness.webhook).toBe(true);
  });

  test("throws an APIError carrying the upstream error message", async () => {
    mockFetch(async () => jsonResponse({ error: "invalid channel" }, 400));
    await expect(
      patchMyPreferences({ control_drift: { slack: false } }),
    ).rejects.toThrow("invalid channel");
  });
});
