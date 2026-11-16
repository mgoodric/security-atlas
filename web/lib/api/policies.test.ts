// #2116 — the browser-side policy calls: each goes to its BFF route with
// its method and body, returns the policy, and turns a refusal into an
// APIError carrying the platform's message.

import { afterEach, describe, expect, test, vi } from "vitest";

import { APIError } from "./base";
import {
  createPolicy,
  discardPolicyDraft,
  fetchPoliciesList,
  fetchPolicyDetail,
  transitionPolicy,
  updatePolicyDraft,
} from "./policies";

const ID = "11111111-1111-1111-1111-111111112116";
const DRAFT = {
  title: "T",
  body_md: "# T",
  owner_role: "security_lead",
  approver_role: "cto",
  linked_control_ids: [],
  acknowledgment_required_roles: [],
};

function respond(status: number, body: unknown) {
  const calls: { url: string; init?: RequestInit }[] = [];
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push({ url: String(input), init });
      return new Response(
        status === 204
          ? null
          : typeof body === "string"
            ? body
            : JSON.stringify(body),
        { status, statusText: status === 500 ? "Internal Server Error" : "" },
      );
    },
  );
  return calls;
}

afterEach(() => vi.restoreAllMocks());

describe("policy writes (#2116)", () => {
  test("createPolicy POSTs the draft and returns the policy", async () => {
    const calls = respond(201, { policy: { id: ID } });
    const p = await createPolicy({
      ...DRAFT,
      version: "1.0",
      predecessor_id: ID,
    });
    expect(p.id).toBe(ID);
    expect(calls[0].url).toBe("/api/policies");
    expect(calls[0].init?.method).toBe("POST");
    expect(JSON.parse(String(calls[0].init?.body))).toMatchObject({
      version: "1.0",
      predecessor_id: ID,
    });
  });

  test("updatePolicyDraft PATCHes the draft", async () => {
    const calls = respond(200, { policy: { id: ID, title: "T" } });
    expect((await updatePolicyDraft(ID, DRAFT)).title).toBe("T");
    expect(calls[0].url).toBe(`/api/policies/${ID}`);
    expect(calls[0].init?.method).toBe("PATCH");
  });

  test("a refusal surfaces the platform's message and status", async () => {
    respond(409, { error: "only a draft policy can be edited" });
    const err = await updatePolicyDraft(ID, DRAFT).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(APIError);
    expect((err as APIError).status).toBe(409);
    expect((err as APIError).message).toBe("only a draft policy can be edited");
  });

  test("a non-JSON refusal keeps the status line", async () => {
    respond(500, "<html>");
    await expect(createPolicy({ ...DRAFT, version: "1" })).rejects.toThrow(
      "500 Internal Server Error",
    );
  });

  test("discardPolicyDraft DELETEs and resolves on 204, rejects otherwise", async () => {
    const calls = respond(204, null);
    await expect(discardPolicyDraft(ID)).resolves.toBeUndefined();
    expect(calls[0].init?.method).toBe("DELETE");
    vi.restoreAllMocks();
    respond(409, { error: "only a draft policy can be discarded" });
    await expect(discardPolicyDraft(ID)).rejects.toThrow("only a draft");
  });

  test("transitionPolicy posts to the action route, with a body only for publish", async () => {
    let calls = respond(200, { policy: { id: ID, status: "under_review" } });
    expect((await transitionPolicy(ID, "submit")).status).toBe("under_review");
    expect(calls[0].url).toBe(`/api/policies/${ID}/submit`);
    expect(calls[0].init?.method).toBe("POST");
    expect(calls[0].init?.body).toBeUndefined();

    vi.restoreAllMocks();
    calls = respond(201, { policy: { id: ID, status: "published" } });
    await transitionPolicy(ID, "publish", {
      new_version: "1.0",
      next_review_at: "2027-11-16",
    });
    expect(calls[0].url).toBe(`/api/policies/${ID}/publish`);
    expect(JSON.parse(String(calls[0].init?.body))).toEqual({
      new_version: "1.0",
      next_review_at: "2027-11-16",
    });
  });
});

describe("policy reads", () => {
  test("fetchPoliciesList and fetchPolicyDetail read their BFF routes", async () => {
    let calls = respond(200, { policies: [] });
    expect(await fetchPoliciesList()).toEqual({ policies: [] });
    expect(calls[0].url).toBe("/api/policies");

    vi.restoreAllMocks();
    calls = respond(200, { policy: { id: ID }, ack_rate: null });
    expect((await fetchPolicyDetail(ID)).policy.id).toBe(ID);
    expect(calls[0].url).toBe(`/api/policies/${ID}`);
  });

  test("a failed read throws an APIError with the platform's message", async () => {
    respond(404, { error: "policy not found" });
    await expect(fetchPolicyDetail(ID)).rejects.toThrow("policy not found");
    vi.restoreAllMocks();
    respond(500, "oops");
    await expect(fetchPoliciesList()).rejects.toThrow("500");
  });
});
