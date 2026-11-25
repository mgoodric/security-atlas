// #2645: one table over the error sites moved onto `upstreamErrorMessage`.
// Each row is a fetcher (one per module, plus the admin, SSO and settings
// writers named first in the ticket). A 5xx body's `error` must never reach
// the thrown message; a 4xx body's `error` must, since it is a refusal the
// platform words for the user.

import { afterEach, describe, expect, test, vi } from "vitest";

import { fetchActionPlansList } from "./action-plans";
import { fetchActivity } from "./activity";
import {
  demoteSuperAdmin,
  getAdminDemoStatus,
  grantSuperAdmin,
  patchAdminSSO,
  postAdminDemoSeed,
  postAdminDemoTeardown,
  revokeAdminUser,
} from "./admin";
import { fetchApprovals } from "./approvals";
import { fetchAuditPeriods } from "./audit-periods";
import { addOverlayEdge } from "./audited-under";
import { listBoardPacks } from "./board";
import { createCalendarSubscription } from "./calendar";
import { evaluateControl } from "./control-actions";
import { bulkAssignOwner } from "./controls-bulk";
import { fetchControlsList, fetchScopeCells } from "./controls-list";
import { fetchEvidenceDetail, fetchEvidenceList } from "./evidence";
import { fetchExceptionsList } from "./exceptions";
import { patchMe, patchMyPreferences, setEmailChannelOptIn } from "./me";
import { fetchMetric } from "./metrics";
import { mapClaimScfAnchor } from "./oscal-components";
import { fetchPersonnelChecklists } from "./personnel-security";
import { fetchPoliciesList, fetchPolicyDetail } from "./policies";
import { setPostureTarget } from "./posture-targets";
import { createRisk, fetchRiskCategories, fetchRiskDetail } from "./risks";
import { listSavedViews } from "./saved-views";
import { fetchScopeDimensions } from "./scope-dimensions";
import { listTaxonomyCategories } from "./taxonomy";
import { parseGenerate } from "@/lib/board/narrative-ai";
import { parseAISuggest } from "@/lib/questionnaire/ai-suggest";
import { parseMappingSuggest } from "@/lib/questionnaire/mapping-suggest";

const LEAK = "pq: relation tenants does not exist";
const REFUSAL = "reason is required";

const SITES: [string, () => Promise<unknown>][] = [
  ["admin grantSuperAdmin", () => grantSuperAdmin("b", "u1")],
  ["admin demoteSuperAdmin", () => demoteSuperAdmin("b", "u1")],
  [
    "admin revokeAdminUser",
    () =>
      revokeAdminUser("b", { user_id: "u1", tenant_id: "t1" } as Parameters<
        typeof revokeAdminUser
      >[1]),
  ],
  ["admin getAdminDemoStatus", () => getAdminDemoStatus("b")],
  [
    "admin patchAdminSSO",
    () => patchAdminSSO("b", {} as Parameters<typeof patchAdminSSO>[1]),
  ],
  ["me patchMe", () => patchMe({})],
  ["me setEmailChannelOptIn", () => setEmailChannelOptIn(true)],
  ["me patchMyPreferences", () => patchMyPreferences({})],
  ["action-plans", () => fetchActionPlansList()],
  [
    "activity",
    () =>
      fetchActivity({
        from: "2026-01-01T00:00:00Z",
        to: "2026-01-02T00:00:00Z",
      }),
  ],
  ["approvals", () => fetchApprovals()],
  ["audit-periods", () => fetchAuditPeriods()],
  [
    "audited-under",
    () => addOverlayEdge({} as Parameters<typeof addOverlayEdge>[0]),
  ],
  ["board", () => listBoardPacks()],
  ["calendar", () => createCalendarSubscription()],
  ["control-actions", () => evaluateControl("c1")],
  ["controls-bulk", () => bulkAssignOwner(["c1"], "u1")],
  ["controls-list fetchControlsList", () => fetchControlsList()],
  ["controls-list fetchScopeCells", () => fetchScopeCells()],
  ["evidence list", () => fetchEvidenceList()],
  ["evidence detail", () => fetchEvidenceDetail("e1")],
  ["exceptions", () => fetchExceptionsList()],
  ["metrics", () => fetchMetric("m1")],
  [
    "oscal-components",
    () => mapClaimScfAnchor("b", "c1", "IAC-06", "test-2026.3"),
  ],
  ["personnel-security", () => fetchPersonnelChecklists()],
  ["policies list", () => fetchPoliciesList()],
  ["policies detail", () => fetchPolicyDetail("p1")],
  [
    "posture-targets",
    () => setPostureTarget("fv1", {} as Parameters<typeof setPostureTarget>[1]),
  ],
  ["risks categories", () => fetchRiskCategories()],
  ["risks detail", () => fetchRiskDetail("r1")],
  [
    "risks create",
    () => createRisk("b", {} as Parameters<typeof createRisk>[1]),
  ],
  ["saved-views", () => listSavedViews("controls")],
  ["scope-dimensions", () => fetchScopeDimensions()],
  ["taxonomy", () => listTaxonomyCategories()],
];

function stubError(status: number, error: string) {
  vi.spyOn(globalThis, "fetch").mockImplementation(
    async () =>
      new Response(JSON.stringify({ error }), {
        status,
        statusText: status >= 500 ? "Internal Server Error" : "Bad Request",
        headers: { "Content-Type": "application/json" },
      }),
  );
}

async function thrownMessage(call: () => Promise<unknown>): Promise<string> {
  try {
    await call();
  } catch (err) {
    return (err as Error).message;
  }
  throw new Error("expected the call to reject");
}

afterEach(() => {
  vi.restoreAllMocks();
});

describe("error sites on upstreamErrorMessage (#2645)", () => {
  test.each(SITES)(
    "%s: a 5xx body never reaches the message",
    async (_n, call) => {
      stubError(500, LEAK);
      const msg = await thrownMessage(call);
      expect(msg).not.toContain(LEAK);
    },
  );

  test.each(SITES)(
    "%s: a 4xx body's error passes through",
    async (_n, call) => {
      stubError(400, REFUSAL);
      expect(await thrownMessage(call)).toContain(REFUSAL);
    },
  );

  test.each([
    ["narrative-ai", parseGenerate],
    ["ai-suggest", parseAISuggest],
    ["mapping-suggest", parseMappingSuggest],
  ] as const)("%s parser: 5xx generic, 4xx passes through", (_n, parse) => {
    expect(parse(false, 502, { error: LEAK }).message).not.toContain(LEAK);
    expect(parse(false, 400, { error: REFUSAL }).message).toBe(REFUSAL);
  });
});

// #2730: a feature switched off on this deployment is a 409 with
// `code: "feature_not_enabled"`, so its wording reaches the page. It was a
// 503, whose body #2645 replaced with the status line.
describe("feature_not_enabled refusals show their wording (#2730)", () => {
  const NOT_ENABLED = "demo seed not enabled on this deployment";

  function stubNotEnabled() {
    vi.spyOn(globalThis, "fetch").mockImplementation(
      async () =>
        new Response(
          JSON.stringify({ error: NOT_ENABLED, code: "feature_not_enabled" }),
          {
            status: 409,
            statusText: "Conflict",
            headers: { "Content-Type": "application/json" },
          },
        ),
    );
  }

  test.each([
    ["admin postAdminDemoSeed", () => postAdminDemoSeed("b")],
    ["admin postAdminDemoTeardown", () => postAdminDemoTeardown("b")],
  ] as const)("%s: a 409 shows its message", async (_n, call) => {
    stubNotEnabled();
    expect(await thrownMessage(call)).toBe(NOT_ENABLED);
  });

  test.each([
    ["narrative-ai", parseGenerate],
    ["ai-suggest", parseAISuggest],
    ["mapping-suggest", parseMappingSuggest],
  ] as const)("%s parser: a 409 shows its message", (_n, parse) => {
    const body = { error: NOT_ENABLED, code: "feature_not_enabled" };
    expect(parse(false, 409, body).message).toBe(NOT_ENABLED);
  });
});
