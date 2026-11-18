// Slice 263 — vitest coverage for the multipart import-excel BFF.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_263 } from "../../../../../lib/test-utils/test-tokens";
import { multipartRequest } from "../../../../../lib/test-utils/upload-requests";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () =>
    Promise.resolve({
      get: mockCookieGet,
    }),
}));

import { POST } from "./route";

const QID = "00000000-0000-0000-0000-000025580001";

function makeReq(form: FormData): Promise<Request> {
  return multipartRequest(form);
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
    const res = await POST((await makeReq(form)) as never, paramsFor(QID));
    expect(res.status).toBe(401);
  });

  test("forwards multipart body and bearer", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
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

    const res = await POST((await makeReq(form)) as never, paramsFor(QID));
    expect(res.status).toBe(201);
    const calledURL = String(fetchSpy.mock.calls[0]?.[0] ?? "");
    expect(calledURL).toContain(
      "/v1/questionnaires/00000000-0000-0000-0000-000025580001/import-excel",
    );
    const init = fetchSpy.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method).toBe("POST");
    const headers = init?.headers as Record<string, string> | undefined;
    expect(headers?.Authorization).toBe(`Bearer ${TEST_BEARER_263}`);
    expect(init?.body).toBeInstanceOf(FormData);
  });

  test("400 for a body that is not valid multipart, before calling the platform", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    const body = "not multipart";
    const req = new Request("http://t/", {
      method: "POST",
      headers: {
        "content-type": "multipart/form-data; boundary=x",
        "content-length": String(body.length),
      },
      body,
    });
    const res = await POST(req as never, paramsFor(QID));
    expect(res.status).toBe(400);
    expect(fetchSpy).not.toHaveBeenCalled();
  });

  test("propagates upstream 413 too-large", async () => {
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_263 });
    vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ error: "upload exceeds size cap" }), {
        status: 413,
      }),
    );
    const form = new FormData();
    const res = await POST((await makeReq(form)) as never, paramsFor(QID));
    expect(res.status).toBe(413);
  });
});
