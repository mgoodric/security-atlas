// OE-664 — unit tests for the personnel-security browser client. Mocks
// `fetch` to assert the BFF URLs, query params, POST bodies, and the
// APIError unwrap — the slice-384 action-plans.test.ts pattern.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  completePersonnelChecklistItem,
  createPersonnelChecklist,
  fetchPersonnelChecklist,
  fetchPersonnelChecklists,
} from "./personnel-security";

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

describe("fetchPersonnelChecklists", () => {
  it("hits the bare BFF with no filters", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ checklists: [], count: 0 }));
    await fetchPersonnelChecklists();
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/personnel-security/checklists",
    );
  });

  it("forwards kind, status, and overdue", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ checklists: [], count: 0 }));
    await fetchPersonnelChecklists({
      kind: "offboarding",
      status: "open",
      overdue: true,
    });
    const url = fetchMock.mock.calls[0][0] as string;
    expect(url).toContain("kind=offboarding");
    expect(url).toContain("status=open");
    expect(url).toContain("overdue=true");
  });

  it("omits overdue when false", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ checklists: [], count: 0 }));
    await fetchPersonnelChecklists({ overdue: false });
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/personnel-security/checklists",
    );
  });

  it("throws APIError with the upstream error message on non-2xx", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse(
        { error: "role does not grant personnel-security access" },
        403,
      ),
    );
    await expect(fetchPersonnelChecklists()).rejects.toMatchObject({
      status: 403,
      message: "role does not grant personnel-security access",
    });
  });
});

describe("fetchPersonnelChecklist", () => {
  it("encodes the id into the detail URL", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ checklist: {} }));
    await fetchPersonnelChecklist("abc 123");
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/personnel-security/checklists/abc%20123",
    );
  });
});

describe("createPersonnelChecklist", () => {
  it("POSTs the create wire shape with due_at only when set", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ checklist: { id: "c1" } }, 201),
    );
    await createPersonnelChecklist({
      kind: "offboarding",
      person_external_id: "emp-1",
      person_display_name: "Sam Lee",
    });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/personnel-security/checklists");
    expect(init.method).toBe("POST");
    const body = JSON.parse(init.body as string);
    expect(body).toEqual({
      kind: "offboarding",
      person_external_id: "emp-1",
      person_work_email: "",
      person_display_name: "Sam Lee",
      control_id: "",
    });
    expect(body).not.toHaveProperty("due_at");
  });

  it("includes due_at when provided", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ checklist: { id: "c1" } }, 201),
    );
    await createPersonnelChecklist({
      kind: "onboarding",
      person_external_id: "emp-2",
      due_at: "2026-08-10T00:00:00Z",
    });
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    const body = JSON.parse(init.body as string);
    expect(body.due_at).toBe("2026-08-10T00:00:00Z");
  });
});

describe("completePersonnelChecklistItem", () => {
  it("POSTs to the complete endpoint with the completion fields", async () => {
    fetchMock.mockResolvedValueOnce(jsonResponse({ item: { id: "i1" } }));
    await completePersonnelChecklistItem("i1", {
      evidence_uri: "https://tickets/T-1",
      notes: "badge returned",
    });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/personnel-security/checklist-items/i1/complete");
    expect(init.method).toBe("POST");
    expect(JSON.parse(init.body as string)).toEqual({
      completed_by: "",
      evidence_uri: "https://tickets/T-1",
      notes: "badge returned",
    });
  });

  it("surfaces the upstream 404 for an unknown or already-completed item", async () => {
    fetchMock.mockResolvedValueOnce(
      jsonResponse({ error: "checklist item not found" }, 404),
    );
    await expect(completePersonnelChecklistItem("i9")).rejects.toMatchObject({
      status: 404,
      message: "checklist item not found",
    });
  });
});
