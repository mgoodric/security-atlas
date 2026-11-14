// #2062 — the signIn server action is the server-side gate for the
// developer token card. It must refuse a pasted token after first install
// unless ATLAS_WEB_DEV_TOKEN_SIGNIN=true, re-reading install-state on every
// call, and must fail closed when install-state cannot be read.

import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

const setCookie = vi.fn();

vi.mock("next/headers", () => ({
  cookies: async () => ({ set: setCookie, delete: vi.fn() }),
  headers: async () => new Headers(),
}));

class RedirectSignal extends Error {
  constructor(readonly target: string) {
    super(`redirect:${target}`);
  }
}

vi.mock("next/navigation", () => ({
  redirect: (target: string) => {
    throw new RedirectSignal(target);
  },
}));

import { signIn } from "./actions";

type FetchCall = { url: string; init?: RequestInit };

function stubFetch(installState: Response | Error): FetchCall[] {
  const calls: FetchCall[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      calls.push({ url, init });
      if (url.endsWith("/v1/install-state")) {
        if (installState instanceof Error) throw installState;
        return installState.clone();
      }
      return new Response(null, { status: 204 });
    }),
  );
  return calls;
}

function form(token: string): FormData {
  const fd = new FormData();
  fd.set("token", token);
  fd.set("from", "/risks");
  return fd;
}

async function redirectOf(p: Promise<void>): Promise<string> {
  try {
    await p;
  } catch (err) {
    if (err instanceof RedirectSignal) return err.target;
    throw err;
  }
  throw new Error("signIn did not redirect");
}

const json = (body: unknown) =>
  new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });

beforeEach(() => {
  setCookie.mockReset();
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.unstubAllEnvs();
});

describe("signIn gate (#2062)", () => {
  test("refuses after first install with the flag off; sets no cookie", async () => {
    stubFetch(json({ first_install: false }));
    const target = await redirectOf(signIn(form("tok")));
    expect(target.startsWith("/login?error=")).toBe(true);
    expect(decodeURIComponent(target)).toContain(
      "token sign-in is not enabled",
    );
    expect(setCookie).not.toHaveBeenCalled();
  });

  test("fails closed when install-state cannot be read", async () => {
    stubFetch(new Error("connection refused"));
    const target = await redirectOf(signIn(form("tok")));
    expect(target.startsWith("/login?error=")).toBe(true);
    expect(setCookie).not.toHaveBeenCalled();
  });

  test("fails closed on a non-2xx install-state", async () => {
    stubFetch(new Response("", { status: 503 }));
    const target = await redirectOf(signIn(form("tok")));
    expect(target.startsWith("/login?error=")).toBe(true);
    expect(setCookie).not.toHaveBeenCalled();
  });

  test("allows the bootstrap/recovery token during first install", async () => {
    const calls = stubFetch(json({ first_install: true }));
    const target = await redirectOf(signIn(form("tok")));
    expect(target).toBe("/risks");
    expect(setCookie).toHaveBeenCalledOnce();
    // install-state is read fresh, never from a cache.
    const read = calls.find((c) => c.url.endsWith("/v1/install-state"));
    expect(read?.init?.cache).toBe("no-store");
  });

  test("allows any time with ATLAS_WEB_DEV_TOKEN_SIGNIN=true", async () => {
    vi.stubEnv("ATLAS_WEB_DEV_TOKEN_SIGNIN", "true");
    stubFetch(json({ first_install: false }));
    const target = await redirectOf(signIn(form("tok")));
    expect(target).toBe("/risks");
    expect(setCookie).toHaveBeenCalledOnce();
  });

  test("re-checks on every call, not once per process", async () => {
    stubFetch(json({ first_install: true }));
    expect(await redirectOf(signIn(form("tok")))).toBe("/risks");
    stubFetch(json({ first_install: false }));
    expect(
      (await redirectOf(signIn(form("tok")))).startsWith("/login?error="),
    ).toBe(true);
  });
});
