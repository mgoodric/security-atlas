// oauth-client.test.ts — vitest unit tests for the slice 189
// frontend OAuth PKCE helpers.
//
// Tests cover:
//
//   - verifier generation: length, base64url charset
//   - challenge derivation: known RFC 7636 vector
//   - state generation: distinct values
//   - sessionStorage round-trip via initLoginFlow (stubbed sessionStorage + window)
//
// Test environment: node (default) — we stub sessionStorage,
// localStorage, window.location, btoa onto globalThis. The web
// workspace does NOT depend on jsdom; we keep that decision (slice
// 069 P0-A3) by avoiding @testing-library/react in this slice.

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  generateCodeChallenge,
  generateCodeVerifier,
  generateState,
  STATE_STORAGE_KEY,
  VERIFIER_STORAGE_KEY,
} from "./oauth-client";

describe("generateCodeVerifier", () => {
  it("returns a 43-character base64url string (32 random bytes)", () => {
    const v = generateCodeVerifier();
    expect(v.length).toBe(43);
    // base64url charset: A-Za-z0-9-_
    expect(v).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it("returns distinct values across calls", () => {
    const a = generateCodeVerifier();
    const b = generateCodeVerifier();
    expect(a).not.toBe(b);
  });
});

describe("generateCodeChallenge", () => {
  it("matches the RFC 7636 Appendix B test vector", async () => {
    // From RFC 7636 Appendix B.
    const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk";
    const expected = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM";
    const got = await generateCodeChallenge(verifier);
    expect(got).toBe(expected);
  });

  it("is deterministic for a given verifier", async () => {
    const v = generateCodeVerifier();
    const a = await generateCodeChallenge(v);
    const b = await generateCodeChallenge(v);
    expect(a).toBe(b);
  });

  it("produces a 43-character base64url string (sha256 → 32 bytes → 43 chars)", async () => {
    const v = generateCodeVerifier();
    const c = await generateCodeChallenge(v);
    expect(c.length).toBe(43);
    expect(c).toMatch(/^[A-Za-z0-9_-]+$/);
  });
});

describe("generateState", () => {
  it("returns a base64url string of 22 chars (16 random bytes)", () => {
    const s = generateState();
    expect(s.length).toBe(22);
    expect(s).toMatch(/^[A-Za-z0-9_-]+$/);
  });

  it("returns distinct values across calls", () => {
    const a = generateState();
    const b = generateState();
    expect(a).not.toBe(b);
  });
});

describe("initLoginFlow + completeLoginFlow sessionStorage discipline", () => {
  // node env doesn't ship sessionStorage / localStorage / window — we
  // stub minimal in-memory replacements so the module can run.

  type MemStore = {
    data: Map<string, string>;
    getItem: (k: string) => string | null;
    setItem: (k: string, v: string) => void;
    removeItem: (k: string) => void;
    clear: () => void;
  };

  function makeStore(): MemStore {
    const store: MemStore = {
      data: new Map(),
      getItem(k) {
        return store.data.has(k) ? store.data.get(k)! : null;
      },
      setItem(k, v) {
        store.data.set(k, v);
      },
      removeItem(k) {
        store.data.delete(k);
      },
      clear() {
        store.data.clear();
      },
    };
    return store;
  }

  let sessionStore: MemStore;
  let localStore: MemStore;
  let assignSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    sessionStore = makeStore();
    localStore = makeStore();
    assignSpy = vi.fn();
    // @ts-expect-error stub
    globalThis.sessionStorage = sessionStore;
    // @ts-expect-error stub
    globalThis.localStorage = localStore;
    globalThis.window = {
      location: {
        assign: assignSpy,
        origin: "https://atlas.example.test",
      } as unknown as Location,
    } as Window & typeof globalThis;
    // btoa is in node 20+; ensure it's present
    if (typeof globalThis.btoa === "undefined") {
      globalThis.btoa = (s: string) =>
        Buffer.from(s, "binary").toString("base64");
    }
  });

  afterEach(() => {
    // @ts-expect-error cleanup
    delete globalThis.sessionStorage;
    // @ts-expect-error cleanup
    delete globalThis.localStorage;
    // @ts-expect-error cleanup
    delete globalThis.window;
  });

  it("initLoginFlow persists verifier + state to sessionStorage", async () => {
    const { initLoginFlow } = await import("./oauth-client");
    await initLoginFlow({
      issuer: "https://atlas.example.test",
      clientId: "client-123",
      redirectUri: "https://atlas.example.test/oauth/callback",
      tenantId: "00000000-0000-0000-0000-000000000001",
    });
    const verifier = sessionStore.getItem(VERIFIER_STORAGE_KEY);
    const state = sessionStore.getItem(STATE_STORAGE_KEY);
    expect(verifier).not.toBeNull();
    expect(state).not.toBeNull();
    expect(verifier!.length).toBe(43);
    expect(state!.length).toBe(22);
    // P0-189-8: localStorage MUST NOT have been touched.
    expect(localStore.getItem(VERIFIER_STORAGE_KEY)).toBeNull();
    expect(localStore.getItem(STATE_STORAGE_KEY)).toBeNull();
  });

  it("initLoginFlow navigates to /oauth/authorize with PKCE params", async () => {
    const { initLoginFlow } = await import("./oauth-client");
    await initLoginFlow({
      issuer: "https://atlas.example.test",
      clientId: "client-456",
      redirectUri: "https://atlas.example.test/oauth/callback",
      tenantId: "00000000-0000-0000-0000-000000000002",
    });
    expect(assignSpy).toHaveBeenCalledOnce();
    const target = assignSpy.mock.calls[0][0] as string;
    const url = new URL(target);
    expect(url.pathname).toBe("/oauth/authorize");
    expect(url.searchParams.get("response_type")).toBe("code");
    expect(url.searchParams.get("client_id")).toBe("client-456");
    expect(url.searchParams.get("code_challenge_method")).toBe("S256");
    expect(url.searchParams.get("code_challenge")).toMatch(
      /^[A-Za-z0-9_-]{43}$/,
    );
    expect(url.searchParams.get("state")).toMatch(/^[A-Za-z0-9_-]{22}$/);
    expect(url.searchParams.get("tenant_id")).toBe(
      "00000000-0000-0000-0000-000000000002",
    );
  });
});
