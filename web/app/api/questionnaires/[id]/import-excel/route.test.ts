// Slice 263 — vitest coverage for the multipart import-excel BFF.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

vi.mock("next/server", () => {
  class NextResponse extends Response {
    static json(
      body: unknown,
      init?: { status?: number; headers?: Record<string, string> },
    ): NextResponse {
      return new NextResponse(JSON.stringify(body), {
        status: init?.status ?? 200,
        headers: {
          "Content-Type": "application/json",
          ...(init?.headers ?? {}),
        },
      });
    }
  }
  return { NextResponse };
});

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

function makeReq(form: FormData): {
  formData: () => Promise<FormData>;
} {
  return { formData: async () => form };
}

function paramsFor(id: string): {
  params: Promise<{ id: string }>;
} {
  return { params: Promise.resolve({ id }) };
}

describe("POST /api/questionnaires/[id]/import-excel", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("401 when bearer cookie missing", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const form = new FormData();
    const res = await POST(makeReq(form) as never, paramsFor("abc"));
    expect(res.status).toBe(401);
  });

  test("forwards multipart body and bearer", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    const fetchSpy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ questions: [], unmapped_columns: [] }), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    );

    const form = new FormData();
    form.append(
      "file",
      new Blob(["pretend-xlsx"], {
        type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
      }),
      "test.xlsx",
    );

    const res = await POST(makeReq(form) as never, paramsFor("abc"));
    expect(res.status).toBe(201);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain("/v1/questionnaires/abc/import-excel");
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe("Bearer test-bearer-263");
    expect(init?.body).toBeInstanceOf(FormData);
  });

  test("propagates upstream 413 too-large", async () => {
    mockCookieGet.mockReturnValue({ value: "test-bearer-263" });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "upload exceeds size cap" }), {
        status: 413,
      }),
    );
    const form = new FormData();
    const res = await POST(makeReq(form) as never, paramsFor("abc"));
    expect(res.status).toBe(413);
  });
});
