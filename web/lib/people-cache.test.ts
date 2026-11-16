// #2085 — the people directory never outlives a tenant switch or a
// sign-out: after either, the next read fetches it again.

import { QueryClient } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { postTenantSwitched } from "@/lib/auth/tenant-broadcast";

import {
  PEOPLE_QUERY_KEY,
  PEOPLE_STALE_MS,
  forgetPeopleDirectory,
  forgetPeopleOnTenantSwitch,
} from "./people-cache";

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

function readDirectory(qc: QueryClient, fetcher: () => Promise<string>) {
  return qc.fetchQuery({
    queryKey: PEOPLE_QUERY_KEY,
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
    expect(qc.getQueryData(PEOPLE_QUERY_KEY)).toBeUndefined();
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
