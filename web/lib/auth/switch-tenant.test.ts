// switch-tenant.test.ts — slice 192 vitest unit tests for the
// frontend tenant-switch helper.
//
// Tests cover:
//
//   - happy-path: POST to /api/auth/switch-tenant with the target
//     id; on 2xx returns { ok: true }.
//   - missing target_tenant_id returns ok: false with 400.
//   - non-2xx response surfaces status + body in ok: false.
//   - network-error path returns ok: false with status 0.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { switchTenant } from "./switch-tenant";

const TENANT_A = "11111111-1111-1111-1111-111111111111";

describe("switchTenant", () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("returns ok: false with 400 when targetTenantId is empty", async () => {
    const result = await switchTenant("");
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.status).toBe(400);
    }
  });

  it("calls the BFF route with the target id and returns ok: true on 2xx", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(
        new Response(JSON.stringify({ ok: true }), { status: 200 }),
      );

    const result = await switchTenant(TENANT_A);
    expect(result.ok).toBe(true);
    expect(fetchSpy).toHaveBeenCalledTimes(1);
    const [url, init] = fetchSpy.mock.calls[0];
    expect(url).toBe("/api/auth/switch-tenant");
    const opts = init as RequestInit;
    expect(opts.method).toBe("POST");
    const body = JSON.parse(opts.body as string);
    expect(body.target_tenant_id).toBe(TENANT_A);
  });

  it("surfaces a non-2xx status + body in ok: false", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValue(new Response("invalid_target", { status: 403 }));

    const result = await switchTenant(TENANT_A);
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.status).toBe(403);
      expect(result.message).toContain("invalid_target");
    }
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it("returns ok: false with status 0 on a network error", async () => {
    vi.spyOn(globalThis, "fetch").mockRejectedValue(new Error("ECONNREFUSED"));

    const result = await switchTenant(TENANT_A);
    expect(result.ok).toBe(false);
    if (!result.ok) {
      expect(result.status).toBe(0);
      expect(result.message).toBe("ECONNREFUSED");
    }
  });
});
