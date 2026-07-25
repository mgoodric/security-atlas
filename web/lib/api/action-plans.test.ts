// Slice 384 — unit tests for the ActionPlan browser client. Mocks `fetch`
// to assert the BFF URLs + query params + the create→link sequencing.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { APIError } from "./base";
import {
  createActionPlan,
  fetchActionPlan,
  fetchActionPlansForControl,
  fetchActionPlansForRisk,
  fetchActionPlansList,
} from "./action-plans";

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

const fetchMock = vi.fn();

beforeEach(() => {
  fetchMock.mockReset();
  vi.stubGlobal("fetch", fetchMock);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("fetchActionPlansList", () => {
  it("hits the bare BFF with no filters", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ action_plans: [], count: 0 }),
    );
    await fetchActionPlansList();
    expect(fetchMock).toHaveBeenCalledWith("/api/action-plans");
  });

  it("forwards status, limit, and cursor", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ action_plans: [], count: 0 }),
    );
    await fetchActionPlansList({
      status: "in_progress",
      limit: 50,
      cursor: "c1",
    });
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain("status=in_progress");
    expect(url).toContain("limit=50");
    expect(url).toContain("cursor=c1");
  });

  it("throws APIError with the upstream error message on non-2xx", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: "role does not grant access" }, 403),
    );
    await expect(fetchActionPlansList()).rejects.toMatchObject({
      status: 403,
      message: "role does not grant access",
    });
  });
});

describe("fetchActionPlan", () => {
  it("encodes the id into the detail URL", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ action_plan: {}, linkage: { risks: [], controls: [] } }),
    );
    await fetchActionPlan("abc 123");
    expect(fetchMock).toHaveBeenCalledWith("/api/action-plans/abc%20123");
  });
});

describe("createActionPlan", () => {
  it("POSTs the plan then links risks and controls in order", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse({ action_plan: { id: "plan-1" } }, 201),
      )
      .mockResolvedValueOnce(jsonResponse({ linked: true }))
      .mockResolvedValueOnce(jsonResponse({ linked: true }));

    const out = await createActionPlan({
      title: "Close gap",
      owner_id: "owner-1",
      risk_ids: ["risk-1"],
      control_ids: ["control-1"],
    });

    expect(out.id).toBe("plan-1");
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const [createUrl, createInit] = fetchMock.mock.calls[0];
    expect(createUrl).toBe("/api/action-plans");
    expect((createInit as RequestInit).method).toBe("POST");
    expect(fetchMock.mock.calls[1][0]).toBe(
      "/api/action-plans/plan-1/risks/risk-1",
    );
    expect(fetchMock.mock.calls[2][0]).toBe(
      "/api/action-plans/plan-1/controls/control-1",
    );
  });

  it("surfaces a link failure as APIError after the plan is created", async () => {
    fetchMock
      .mockResolvedValueOnce(
        jsonResponse({ action_plan: { id: "plan-2" } }, 201),
      )
      .mockResolvedValueOnce(
        jsonResponse({ error: "link target not found" }, 404),
      );

    await expect(
      createActionPlan({
        title: "x",
        owner_id: "o",
        risk_ids: ["bad-risk"],
      }),
    ).rejects.toBeInstanceOf(APIError);
  });
});

describe("linked-section fetchers", () => {
  it("fetchActionPlansForRisk hits the risk sub-route", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ action_plans: [], count: 0 }),
    );
    await fetchActionPlansForRisk("risk-9");
    expect(fetchMock).toHaveBeenCalledWith("/api/risks/risk-9/action-plans");
  });

  it("fetchActionPlansForControl hits the control sub-route", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ action_plans: [], count: 0 }),
    );
    await fetchActionPlansForControl("ctrl-9");
    expect(fetchMock).toHaveBeenCalledWith("/api/controls/ctrl-9/action-plans");
  });
});
