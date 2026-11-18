// #2392 — the anchor-history fetchers in control-detail.ts: the first page
// asks for no cursor, a later page forwards the platform's next_cursor.

import { beforeEach, describe, expect, test, vi } from "vitest";

import {
  fetchControlAnchorHistory,
  getControlAnchorHistory,
} from "./control-detail";

const CONTROL_ID = "33333333-3333-3333-3333-333333332392";
const page = {
  control_id: CONTROL_ID,
  decisions: [],
  count: 0,
  next_cursor: "",
};

function mockFetch() {
  return vi
    .spyOn(globalThis, "fetch")
    .mockImplementation(async () => new Response(JSON.stringify(page)));
}

describe("anchor-history fetchers (#2392)", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete process.env.NEXT_PUBLIC_API_BASE_URL;
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  test("the BFF fetcher asks for the first page without a cursor", async () => {
    const spy = mockFetch();
    await expect(fetchControlAnchorHistory(CONTROL_ID)).resolves.toEqual(page);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `/api/controls/${CONTROL_ID}/anchor-history`,
    );
  });

  test("the BFF fetcher forwards a cursor, encoded", async () => {
    const spy = mockFetch();
    await fetchControlAnchorHistory(CONTROL_ID, "a/b=");
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `/api/controls/${CONTROL_ID}/anchor-history?cursor=a%2Fb%3D`,
    );
  });

  test("the platform fetcher sends the bearer, with and without a cursor", async () => {
    const spy = mockFetch();
    await getControlAnchorHistory("test-bearer-2392", CONTROL_ID);
    await getControlAnchorHistory("test-bearer-2392", CONTROL_ID, "next");
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/controls/${CONTROL_ID}/anchor-history`,
    );
    expect(String(spy.mock.calls[1]?.[0])).toBe(
      `http://atlas:8080/v1/controls/${CONTROL_ID}/anchor-history?cursor=next`,
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect((init?.headers as Record<string, string>)?.Authorization).toBe(
      "Bearer test-bearer-2392",
    );
  });
});
