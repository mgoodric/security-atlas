// #2110 — vitest coverage for the control-detail action BFF routes:
// Evaluate now, Download bundle and the bundle upload. Each forwards the
// bearer to its platform path and passes the status and body through; path
// ids must be UUIDs and the upload is checked before it is buffered.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import { mockNextServer } from "../../../../../lib/test-utils/next-mocks";
import { TEST_BEARER_DEFAULT } from "../../../../../lib/test-utils/test-tokens";

vi.mock("next/server", () => mockNextServer());

const mockCookieGet = vi.fn();

vi.mock("next/headers", () => ({
  cookies: () => Promise.resolve({ get: mockCookieGet }),
}));

import { GET as downloadBundle } from "../bundle/route";
import { POST as uploadBundle } from "../../upload-bundle/route";
import { POST as evaluate } from "./route";

const C = "c2110000-0000-4000-8000-000000000001";
const params = (id = C) => ({ params: Promise.resolve({ id }) });

function upstream(status: number, body: BodyInit, headers = {}) {
  return vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
    new Response(body, {
      status,
      headers: { "Content-Type": "application/json", ...headers },
    }),
  );
}

function uploadReq(
  form: FormData,
  headers: Record<string, string> = {
    "content-type": "multipart/form-data; boundary=x",
  },
) {
  return { headers: new Headers(headers), formData: async () => form } as never;
}

describe("control action BFF routes", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    mockCookieGet.mockReset();
    mockCookieGet.mockReturnValue({ value: TEST_BEARER_DEFAULT });
    process.env.ATLAS_HTTP_URL = "http://atlas:8080";
  });
  afterEach(() => {
    delete process.env.ATLAS_HTTP_URL;
  });

  test("evaluate POSTs to the platform and passes the new state through", async () => {
    const state = { control_id: C, states: [{ result: "pass" }], count: 1 };
    const spy = upstream(200, JSON.stringify(state));
    const res = await evaluate(new Request("http://x"), params());
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual(state);
    const [url, init] = spy.mock.calls[0] ?? [];
    expect(String(url)).toBe(`http://atlas:8080/v1/controls/${C}/evaluate`);
    expect((init as RequestInit).method).toBe("POST");
    expect(
      ((init as RequestInit).headers as Record<string, string>).Authorization,
    ).toBe(`Bearer ${TEST_BEARER_DEFAULT}`);
  });

  test("evaluate passes a 403 through", async () => {
    upstream(
      403,
      JSON.stringify({
        error: "only the control's owner or an admin may evaluate it",
      }),
    );
    const res = await evaluate(new Request("http://x"), params());
    expect(res.status).toBe(403);
  });

  test("evaluate refuses a non-UUID id before calling the platform", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const res = await evaluate(new Request("http://x"), params(".."));
    expect(res.status).toBe(400);
    expect(spy).not.toHaveBeenCalled();
  });

  test("evaluate without a session is 401", async () => {
    mockCookieGet.mockReturnValue(undefined);
    const res = await evaluate(new Request("http://x"), params());
    expect(res.status).toBe(401);
  });

  test("download streams the bundle with the platform's filename", async () => {
    const spy = upstream(200, new Uint8Array([0x1f, 0x8b]), {
      "Content-Type": "application/gzip",
      "Content-Disposition": 'attachment; filename="aaa_ctrl.tar.gz"',
    });
    const res = await downloadBundle(new Request("http://x"), params());
    expect(res.status).toBe(200);
    expect(res.headers.get("content-disposition")).toBe(
      'attachment; filename="aaa_ctrl.tar.gz"',
    );
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      `http://atlas:8080/v1/controls/${C}/bundle`,
    );
  });

  test("download passes a 404 through", async () => {
    upstream(404, JSON.stringify({ error: "control not found" }));
    const res = await downloadBundle(new Request("http://x"), params());
    expect(res.status).toBe(404);
  });

  test("upload re-posts the form to the existing bundle upload", async () => {
    const spy = upstream(200, JSON.stringify({ control_id: C, version: 2 }));
    const form = new FormData();
    form.append(
      "bundle.tar.gz",
      new Blob([new Uint8Array([1])]),
      "bundle.tar.gz",
    );
    const res = await uploadBundle(uploadReq(form));
    expect(res.status).toBe(200);
    expect(String(spy.mock.calls[0]?.[0])).toBe(
      "http://atlas:8080/v1/controls:upload-bundle",
    );
  });

  test("upload refuses a non-multipart body and an oversized one", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    const form = new FormData();
    expect(
      (
        await uploadBundle(
          uploadReq(form, { "content-type": "application/json" }),
        )
      ).status,
    ).toBe(400);
    expect(
      (
        await uploadBundle(
          uploadReq(form, {
            "content-type": "multipart/form-data; boundary=x",
            "content-length": String((8 << 20) + 1),
          }),
        )
      ).status,
    ).toBe(413);
    expect(spy).not.toHaveBeenCalled();
  });
});
