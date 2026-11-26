// #2113 — the exception request form and lifecycle actions.
//
// The security-relevant case: the scope picker emits null for an
// incomplete choice, and the platform reads a stored null predicate as
// every cell. The form must build no body for it, and the client must
// never send it.

import { afterEach, describe, expect, test, vi } from "vitest";

import { createException } from "@/lib/api/exceptions";

import {
  bulkIdempotencyKey,
  dayAfter,
  dayStart,
  idempotencyKeyFor,
  isWholeControl,
  MAX_DAYS,
  nextAction,
  validateAction,
  validateRequest,
  type RequestDraft,
} from "./lifecycle";

const NOW = new Date("2026-11-15T12:00:00Z");
const CONTROL = "00000000-0000-0000-0000-000000002113";

function draft(over: Partial<RequestDraft> = {}): RequestDraft {
  return {
    controlId: CONTROL,
    predicate: { op: "true" },
    justification: "Vendor cannot support SSO until Q2",
    compensating: "Weekly access review\n\n  Manual MFA check  ",
    expiresOn: "2027-02-13",
    ...over,
  };
}

describe("validateRequest", () => {
  test("a complete draft builds the request body", () => {
    const r = validateRequest(draft(), NOW);
    expect(r).toEqual({
      ok: true,
      body: {
        control_id: CONTROL,
        scope_cell_predicate: { op: "true" },
        justification: "Vendor cannot support SSO until Q2",
        compensating_controls: ["Weekly access review", "Manual MFA check"],
        expires_at: "2027-02-13T00:00:00.000Z",
      },
    });
  });

  test("a null predicate (picker incomplete) blocks submit and builds no body", () => {
    const r = validateRequest(draft({ predicate: null }), NOW);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.errors.scope).toMatch(/choose Everything/);
    expect(r).not.toHaveProperty("body");
  });

  test("an array or non-object predicate is refused too", () => {
    for (const predicate of [[] as unknown as object]) {
      const r = validateRequest(draft({ predicate }), NOW);
      expect(r.ok).toBe(false);
    }
  });

  test("the whole control is the explicit always-true predicate, never null", () => {
    const r = validateRequest(draft({ predicate: { op: "true" } }), NOW);
    expect(r.ok && r.body.scope_cell_predicate).toEqual({ op: "true" });
  });

  test("justification and control are required", () => {
    const r = validateRequest(
      draft({ justification: "   ", controlId: "" }),
      NOW,
    );
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.errors.justification).toBeTruthy();
    expect(r.errors.control).toBeTruthy();
  });

  test("expiry must be after today and at most 365 days out", () => {
    const at = (expiresOn: string) =>
      validateRequest(draft({ expiresOn }), NOW);
    expect(at("2026-11-15").ok).toBe(false);
    expect(at("").ok).toBe(false);
    expect(at("not-a-date").ok).toBe(false);
    expect(at(dayAfter(NOW, 1)).ok).toBe(true);
    expect(at(dayAfter(NOW, MAX_DAYS)).ok).toBe(true);
    const over = at(dayAfter(NOW, MAX_DAYS + 1));
    expect(over.ok).toBe(false);
    if (!over.ok) expect(over.errors.expires).toContain("365 days");
  });
});

describe("createException", () => {
  afterEach(() => vi.restoreAllMocks());

  test("never sends a null predicate", async () => {
    const spy = vi.spyOn(globalThis, "fetch");
    await expect(
      createException({
        control_id: CONTROL,
        scope_cell_predicate: null as unknown as object,
        justification: "x",
        compensating_controls: [],
        expires_at: "2027-01-01T00:00:00Z",
      }),
    ).rejects.toThrow(/scope/);
    expect(spy).not.toHaveBeenCalled();
  });

  test("posts a complete request to the BFF", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exception: { id: "e1" } }), {
        status: 201,
      }),
    );
    const body = {
      control_id: CONTROL,
      scope_cell_predicate: { op: "true" },
      justification: "x",
      compensating_controls: [],
      expires_at: "2027-01-01T00:00:00Z",
    };
    await createException(body);
    expect(String(spy.mock.calls[0]?.[0])).toBe("/api/exceptions");
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    expect(init.method).toBe("POST");
    expect(JSON.parse(String(init.body))).toEqual(body);
    const headers = init.headers as Record<string, string>;
    expect(headers["Idempotency-Key"]).toBeUndefined();
  });

  test("sends the Idempotency-Key it is given (#2618)", async () => {
    const spy = vi.spyOn(globalThis, "fetch").mockResolvedValueOnce(
      new Response(JSON.stringify({ exception: { id: "e1" } }), {
        status: 201,
      }),
    );
    await createException(
      {
        control_id: CONTROL,
        scope_cell_predicate: { op: "true" },
        justification: "x",
        compensating_controls: [],
        expires_at: "2027-01-01T00:00:00Z",
      },
      "key-1",
    );
    const init = spy.mock.calls[0]?.[1] as RequestInit;
    const headers = init.headers as Record<string, string>;
    expect(headers["Idempotency-Key"]).toBe("key-1");
  });
});

describe("idempotencyKeyFor (#2618)", () => {
  let n = 0;
  const fresh = () => `k${++n}`;

  test("a first submit gets a fresh key", () => {
    expect(idempotencyKeyFor(null, "b1", fresh).key).toMatch(/^k\d+$/);
  });

  test("resubmitting the same body reuses the key", () => {
    const first = idempotencyKeyFor(null, "b1", fresh);
    expect(idempotencyKeyFor(first, "b1", fresh).key).toBe(first.key);
  });

  test("a changed body gets a new key", () => {
    const first = idempotencyKeyFor(null, "b1", fresh);
    const second = idempotencyKeyFor(first, "b2", fresh);
    expect(second.key).not.toBe(first.key);
    expect(second.body).toBe("b2");
  });

  test("a bulk request keys each control apart", () => {
    expect(bulkIdempotencyKey("k", "c1")).not.toBe(
      bulkIdempotencyKey("k", "c2"),
    );
  });
});

describe("validateAction", () => {
  test("deny and revoke need a reason", () => {
    expect(validateAction("deny", { reason: " ", effectiveOn: "" }).ok).toBe(
      false,
    );
    expect(
      validateAction("revoke", { reason: " fixed early ", effectiveOn: "" }),
    ).toEqual({ ok: true, body: { reason: "fixed early" } });
  });

  test("activate sends effective_from only when a day is chosen", () => {
    expect(validateAction("activate", { reason: "", effectiveOn: "" })).toEqual(
      { ok: true, body: {} },
    );
    expect(
      validateAction("activate", { reason: "", effectiveOn: "2026-12-01" }),
    ).toEqual({
      ok: true,
      body: { effective_from: "2026-12-01T00:00:00.000Z" },
    });
  });
});

describe("nextAction", () => {
  test("one primary action per live state, none once final", () => {
    expect(nextAction("requested")).toEqual({ kind: "approve" });
    expect(nextAction("approved")).toMatchObject({ action: "activate" });
    expect(nextAction("active")).toMatchObject({ action: "revoke" });
    for (const s of ["denied", "expired", "revoked"] as const) {
      expect(nextAction(s)).toBeNull();
    }
  });
});

// #2600: {"op":"true"} and the legacy {} / null all read as the whole
// control; anything narrower, or not a predicate object, does not.
test("isWholeControl", () => {
  expect(isWholeControl({ op: "true" })).toBe(true);
  expect(isWholeControl({})).toBe(true);
  expect(isWholeControl(null)).toBe(true);
  expect(isWholeControl(undefined)).toBe(true);
  expect(isWholeControl({ op: "true", extra: 1 })).toBe(false);
  expect(isWholeControl({ op: "eq", dim: "environment", value: "prod" })).toBe(
    false,
  );
  expect(isWholeControl([])).toBe(false);
  expect(isWholeControl("true")).toBe(false);
});

test("dayStart rejects malformed days", () => {
  expect(dayStart("2026-13-40")).toBeNull();
  expect(dayStart("2026-11-15")).toBe("2026-11-15T00:00:00.000Z");
});
