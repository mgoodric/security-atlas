// #2325 — the remap list is paged by the platform; the review page's client
// reads every page so its per-class counts cover the whole queue.

import { afterEach, describe, expect, test, vi } from "vitest";

import {
  REMAP_PAGE_LIMIT,
  fetchRemapProposals,
  type RemapProposal,
} from "./remap";

function proposal(id: string): RemapProposal {
  return {
    id,
    control_id: `c-${id}`,
    bundle_id: `b-${id}`,
    title: id,
    legacy_scf_id: "TRM-01",
    legacy_release: "a",
    target_release: "b",
    class: "unchanged",
    status: "open",
    successor_scf_ids: ["TRM-01"],
    created_at: "2026-11-17T00:00:00Z",
  };
}

const page = (ids: string[], next?: string) =>
  new Response(
    JSON.stringify({
      proposals: ids.map(proposal),
      count: ids.length,
      total: 3,
      next_cursor: next,
    }),
    { status: 200 },
  );

describe("fetchRemapProposals", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  test("follows next_cursor until the last page", async () => {
    const fetchSpy = vi
      .spyOn(globalThis, "fetch")
      .mockResolvedValueOnce(page(["p1", "p2"], "cur-1"))
      .mockResolvedValueOnce(page(["p3"]));
    const got = await fetchRemapProposals();
    expect(got.proposals.map((p) => p.id)).toEqual(["p1", "p2", "p3"]);
    expect(got.count).toBe(3);
    expect(got.total).toBe(3);
    expect(fetchSpy.mock.calls.map((c) => String(c[0]))).toEqual([
      `/api/controls/remap-proposals?limit=${REMAP_PAGE_LIMIT}`,
      `/api/controls/remap-proposals?limit=${REMAP_PAGE_LIMIT}&cursor=cur-1`,
    ]);
  });
});
