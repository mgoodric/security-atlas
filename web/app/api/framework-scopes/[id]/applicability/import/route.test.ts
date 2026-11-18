// #1795 (review note on #2206) — the SoA import BFF route refuses a body
// that declares more than the platform's 4 MiB cap before buffering it.

import { beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../../lib/test-utils/next-mocks";

const cookieStore = new Map<string, string>();

vi.mock("next/headers", () => ({
  cookies: async () => ({
    get: (name: string) =>
      cookieStore.has(name) ? { value: cookieStore.get(name) } : undefined,
  }),
}));

vi.mock("next/server", () => mockNextServer());

import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import { POST } from "./route";

const ctx = {
  params: Promise.resolve({ id: "00000000-0000-0000-0000-000025580001" }),
};

function makeReq(body: string, contentLength?: number): Request {
  const headers: Record<string, string> = { "Content-Type": "text/csv" };
  if (contentLength !== undefined) {
    headers["Content-Length"] = String(contentLength);
  }
  return new Request(
    "http://test/api/framework-scopes/00000000-0000-0000-0000-000025580001/applicability/import",
    { method: "POST", headers, body },
  );
}

describe("POST /api/framework-scopes/[id]/applicability/import", () => {
  beforeEach(() => {
    cookieStore.clear();
    vi.restoreAllMocks();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
    cookieStore.set(ATLAS_JWT_COOKIE, "test-bearer-token");
  });

  test("413 without calling upstream when content-length exceeds 4 MiB", async () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const res = await POST(makeReq("x", (4 << 20) + 1) as any, ctx);
    expect(res.status).toBe(413);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("forwards a body within the cap as text/csv", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(
        new Response(`{"applied":1,"rejected":[]}`, { status: 200 }),
      );
    const csv = "requirement_code,decision\nR-01,applicable\n";
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const res = await POST(makeReq(csv, csv.length) as any, ctx);
    expect(res.status).toBe(200);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(String(url)).toBe(
      "http://atlas:8080/v1/framework-scopes/00000000-0000-0000-0000-000025580001/applicability/import",
    );
    expect((init as RequestInit).body).toBe(csv);
  });
});
