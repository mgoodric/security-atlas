// #2085 — the people directory never outlives a tenant switch or a
// sign-out: after either, the next read fetches it again.

import { QueryClient, QueryObserver } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { postTenantSwitched } from "@/lib/auth/tenant-broadcast";

import {
  PEOPLE_STALE_MS,
  forgetPeopleDirectory,
  forgetPeopleOnTenantSwitch,
  peopleDirectoryQuery,
} from "./people-cache";

const TENANT_A = "aaaaaaaa-0000-4000-8000-000000000001";
const TENANT_B = "bbbbbbbb-0000-4000-8000-000000000002";

// Minimal same-origin BroadcastChannel: a post reaches every other open
// channel of the same name, as in a browser (same tab or sibling tab).
const open = new Set<MockChannel>();
class MockChannel {
  private listeners = new Set<(ev: MessageEvent) => void>();
  constructor(readonly name: string) {
    open.add(this);
  }
  postMessage(data: unknown) {
    for (const peer of open) {
      if (peer === this || peer.name !== this.name) continue;
      for (const l of peer.listeners) l({ data } as MessageEvent);
    }
  }
  addEventListener(_: string, l: (ev: MessageEvent) => void) {
    this.listeners.add(l);
  }
  removeEventListener(_: string, l: (ev: MessageEvent) => void) {
    this.listeners.delete(l);
  }
  close() {
    open.delete(this);
  }
}

function readDirectory(
  qc: QueryClient,
  fetcher: () => Promise<string>,
  tenantId: string = TENANT_A,
) {
  return qc.fetchQuery({
    queryKey: peopleDirectoryQuery(tenantId).queryKey,
    queryFn: fetcher,
    staleTime: PEOPLE_STALE_MS,
  });
}

describe("people directory cache", () => {
  beforeEach(() => {
    vi.stubGlobal("BroadcastChannel", MockChannel);
  });
  afterEach(() => {
    vi.unstubAllGlobals();
    open.clear();
  });

  it("serves one fetch to every read within a visit", async () => {
    const qc = new QueryClient();
    const fetcher = vi.fn(async () => "tenant-a");
    await readDirectory(qc, fetcher);
    await readDirectory(qc, fetcher);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("re-fetches after a tenant switch is broadcast", async () => {
    const qc = new QueryClient();
    const unsubscribe = forgetPeopleOnTenantSwitch(qc);
    const fetcher = vi
      .fn<() => Promise<string>>()
      .mockResolvedValueOnce("tenant-a")
      .mockResolvedValueOnce("tenant-b");

    expect(await readDirectory(qc, fetcher)).toBe("tenant-a");
    postTenantSwitched("bbbbbbbb-0000-4000-8000-000000000002");
    expect(await readDirectory(qc, fetcher)).toBe("tenant-b");
    expect(fetcher).toHaveBeenCalledTimes(2);
    unsubscribe();
  });

  it("re-fetches after the directory is forgotten (sign-out, in-tab switch)", async () => {
    const qc = new QueryClient();
    const fetcher = vi.fn(async () => "x");
    await readDirectory(qc, fetcher);
    forgetPeopleDirectory(qc);
    expect(
      qc.getQueryData(peopleDirectoryQuery(TENANT_A).queryKey),
    ).toBeUndefined();
    await readDirectory(qc, fetcher);
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("stops forgetting once unsubscribed", async () => {
    const qc = new QueryClient();
    forgetPeopleOnTenantSwitch(qc)();
    const fetcher = vi.fn(async () => "x");
    await readDirectory(qc, fetcher);
    postTenantSwitched("bbbbbbbb-0000-4000-8000-000000000002");
    await readDirectory(qc, fetcher);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
});

// #2399 — a session expiry followed by a sign-in to another tenant in the
// same tab drops nothing (no switch, no sign-out), so the key itself must
// keep tenant A's directory away from tenant B.
describe("people directory keyed by tenant", () => {
  // Every result a <Person> would see for `tenantId`, from mount until the
  // fetch settles.
  async function observe(
    qc: QueryClient,
    tenantId: string | undefined,
    fetcher: () => Promise<string>,
  ) {
    const seen: (string | undefined)[] = [];
    const observer = new QueryObserver(qc, {
      ...peopleDirectoryQuery(tenantId),
      queryFn: fetcher,
      staleTime: PEOPLE_STALE_MS,
      retry: false,
    });
    seen.push(observer.getCurrentResult().data);
    const unsubscribe = observer.subscribe((r) => seen.push(r.data));
    if (peopleDirectoryQuery(tenantId).enabled) {
      await vi.waitFor(() =>
        expect(observer.getCurrentResult().isSuccess).toBe(true),
      );
    }
    unsubscribe();
    return seen;
  }

  it("never serves tenant A's names to a sign-in as tenant B", async () => {
    const qc = new QueryClient();
    await readDirectory(qc, async () => "names-of-a", TENANT_A);

    const fetchB = vi.fn(async () => "names-of-b");
    const seen = await observe(qc, TENANT_B, fetchB);

    expect(seen).not.toContain("names-of-a");
    expect(seen.at(-1)).toBe("names-of-b");
    expect(fetchB).toHaveBeenCalledTimes(1);
  });

  it("with the tenant unknown, reads nothing cached and fetches nothing", async () => {
    const qc = new QueryClient();
    await readDirectory(qc, async () => "names-of-a", TENANT_A);

    const fetcher = vi.fn(async () => "x");
    const seen = await observe(qc, undefined, fetcher);

    expect(seen.every((d) => d === undefined)).toBe(true);
    expect(fetcher).not.toHaveBeenCalled();
    expect(peopleDirectoryQuery("").enabled).toBe(false);
  });

  it("keys the module-filtered directory by tenant too", () => {
    expect(peopleDirectoryQuery(TENANT_A, "vendors").queryKey).toEqual([
      "people",
      TENANT_A,
      { module: "vendors" },
    ]);
    expect(peopleDirectoryQuery(TENANT_B).queryKey).toEqual([
      "people",
      TENANT_B,
    ]);
  });

  it("forgetting drops every tenant's entries", async () => {
    const qc = new QueryClient();
    await readDirectory(qc, async () => "a", TENANT_A);
    await readDirectory(qc, async () => "b", TENANT_B);
    forgetPeopleDirectory(qc);
    expect(qc.getQueryCache().findAll({ queryKey: ["people"] })).toEqual([]);
  });
});
