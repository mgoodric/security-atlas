// Unit tests for OAuthClient.
//
// Uses Node's built-in `node:test` runner — no external dependencies.
// Run via `node --test --experimental-strip-types tests/oauth.test.ts`
// or from the monorepo root by adding the script to package.json.

import { describe, it, beforeEach, afterEach } from "node:test";
import assert from "node:assert/strict";
import { createServer, type Server } from "node:http";
import { AddressInfo } from "node:net";

import { InvalidConfigError, OAuthClient, OAuthError } from "../src/oauth.js";

interface IssuerState {
  tokens: string[];
  expiresIn: number;
  calls: number;
  status: number;
}

function startIssuer(state: IssuerState): {
  server: Server;
  url: string;
  close: () => Promise<void>;
} {
  const server = createServer((req, res) => {
    if (req.method !== "POST" || req.url !== "/oauth/token") {
      res.writeHead(404);
      res.end();
      return;
    }
    let body = "";
    req.on("data", (chunk) => (body += chunk));
    req.on("end", () => {
      const form = new URLSearchParams(body);
      if (form.get("grant_type") !== "client_credentials") {
        res.writeHead(400);
        res.end();
        return;
      }
      if (state.status !== 200) {
        res.writeHead(state.status, { "Content-Type": "application/json" });
        res.end(JSON.stringify({ error: "invalid_client" }));
        return;
      }
      const tok = state.tokens[state.calls % state.tokens.length];
      state.calls++;
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(
        JSON.stringify({
          access_token: tok,
          token_type: "Bearer",
          expires_in: state.expiresIn,
        }),
      );
    });
  });
  return {
    server,
    url: "",
    close: () =>
      new Promise<void>((resolve) => {
        server.close(() => resolve());
      }),
  };
}

async function listen(server: Server): Promise<string> {
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address() as AddressInfo;
  return `http://127.0.0.1:${addr.port}`;
}

describe("OAuthClient", () => {
  let issuer: { server: Server; url: string; close: () => Promise<void> };
  let state: IssuerState;

  beforeEach(async () => {
    state = { tokens: ["tok-1"], expiresIn: 3600, calls: 0, status: 200 };
    issuer = startIssuer(state);
    issuer.url = await listen(issuer.server);
  });

  afterEach(async () => {
    await issuer.close();
  });

  it("requires clientId", () => {
    assert.throws(
      () =>
        new OAuthClient({
          clientId: "",
          clientSecret: "s",
          issuerUrl: "https://x",
        }),
      InvalidConfigError,
    );
  });

  it("requires clientSecret", () => {
    assert.throws(
      () =>
        new OAuthClient({
          clientId: "i",
          clientSecret: "",
          issuerUrl: "https://x",
        }),
      InvalidConfigError,
    );
  });

  it("requires issuerUrl", () => {
    assert.throws(
      () =>
        new OAuthClient({
          clientId: "i",
          clientSecret: "s",
          issuerUrl: "",
        }),
      InvalidConfigError,
    );
  });

  it("caches the token until near expiry", async () => {
    const oc = new OAuthClient({
      clientId: "i",
      clientSecret: "s",
      issuerUrl: issuer.url,
    });
    const t1 = await oc.getToken();
    const t2 = await oc.getToken();
    assert.equal(t1, "tok-1");
    assert.equal(t1, t2);
    assert.equal(state.calls, 1);
  });

  it("refreshes when inside the leeway window", async () => {
    state.tokens = ["tok-1", "tok-2"];
    state.expiresIn = 60;
    let clock = 1_700_000_000_000;
    const oc = new OAuthClient({
      clientId: "i",
      clientSecret: "s",
      issuerUrl: issuer.url,
      now: () => clock,
    });
    const t1 = await oc.getToken();
    assert.equal(t1, "tok-1");
    // Advance into the 60-second leeway window.
    clock += 30_000;
    const t2 = await oc.getToken();
    assert.equal(t2, "tok-2");
    assert.equal(state.calls, 2);
  });

  it("serializes concurrent callers through a single in-flight promise", async () => {
    const oc = new OAuthClient({
      clientId: "i",
      clientSecret: "s",
      issuerUrl: issuer.url,
    });
    const promises = Array.from({ length: 10 }, () => oc.getToken());
    const results = await Promise.all(promises);
    assert.equal(new Set(results).size, 1);
    assert.equal(results[0], "tok-1");
    assert.equal(state.calls, 1);
  });

  it("surfaces issuer 401 as OAuthError", async () => {
    state.status = 401;
    const oc = new OAuthClient({
      clientId: "i",
      clientSecret: "s",
      issuerUrl: issuer.url,
    });
    await assert.rejects(() => oc.getToken(), OAuthError);
  });
});
