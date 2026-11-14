// #2040 — prod-build check that every path through web hands atlas a
// vetted X-Forwarded-For, never a browser-forged one.
//
// Runs the standalone build (`npm run build:standalone`) the way
// web.Dockerfile does, with the web/peer-chain.cjs preload, in front of a
// stand-in atlas on :8080 that records the X-Forwarded-For it receives.
// It drives every path that reaches atlas:
//
//   - the /v1/* rewrite (Next's proxy forwards headers verbatim);
//   - a WebSocket upgrade on the same rewrite (httpxy `ws: true`);
//   - the /login server action (signInLocal's server-side fetch).
//
// Port 8080 is the build-time rewrite destination (next.config.ts
// defaults ATLAS_HTTP_URL to http://localhost:8080), so CI runs this
// before the real atlas starts. Exit 0 on pass, 1 on any failed
// assertion.
//
// Usage (from web/): node scripts/check-forwarded-for.mjs

import { spawn } from "node:child_process";
import http from "node:http";
import { fileURLToPath } from "node:url";

const WEB_DIR = fileURLToPath(new URL("..", import.meta.url));
const ATLAS_PORT = 8080;
const WEB_PORT = 13999;
const WEB = `http://127.0.0.1:${WEB_PORT}`;

let failures = 0;
function check(name, got, want) {
  const ok = JSON.stringify(got) === JSON.stringify(want);
  console.log(
    `${ok ? "ok  " : "FAIL"} ${name}: got ${JSON.stringify(got)}${
      ok ? "" : `, want ${JSON.stringify(want)}`
    }`,
  );
  if (!ok) failures++;
}

// Stand-in atlas: record the X-Forwarded-For per path, answer 401.
const seen = [];
const atlas = http.createServer((req, res) => {
  seen.push({ path: req.url, xff: req.headers["x-forwarded-for"] ?? null });
  res.writeHead(401, { "content-type": "application/json" });
  res.end("{}");
});
// WebSocket upgrades proxied by the rewrite arrive as "upgrade", not as
// a request.
atlas.on("upgrade", (req, socket) => {
  seen.push({ path: req.url, xff: req.headers["x-forwarded-for"] ?? null });
  socket.end("HTTP/1.1 401 Unauthorized\r\nContent-Length: 0\r\n\r\n");
});
// No host: dual-stack, so `localhost` reaches it as 127.0.0.1 or ::1.
await new Promise((r) => atlas.listen(ATLAS_PORT, r));

// A stale server on WEB_PORT would answer in place of the one under test.
async function assertPortFree() {
  const taken = await fetch(`${WEB}/`).then(
    () => true,
    () => false,
  );
  if (taken) throw new Error(`port ${WEB_PORT} is already in use`);
}

function startWeb(env) {
  const proc = spawn(
    process.execPath,
    ["--require", "./peer-chain.cjs", ".next/standalone/web/server.js"],
    {
      cwd: WEB_DIR,
      env: {
        ...process.env,
        NODE_ENV: "production",
        PORT: String(WEB_PORT),
        HOSTNAME: "127.0.0.1",
        ATLAS_HTTP_URL: `http://127.0.0.1:${ATLAS_PORT}`,
        ...env,
      },
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
  let log = "";
  proc.stdout.on("data", (d) => (log += d));
  proc.stderr.on("data", (d) => (log += d));
  const exited = new Promise((r) => proc.on("exit", (code) => r(code)));
  return { proc, exited, log: () => log };
}

async function waitReady(web) {
  for (let i = 0; i < 100; i++) {
    try {
      await fetch(`${WEB}/login`);
      return true;
    } catch {
      if (web.proc.exitCode !== null) return false;
      await new Promise((r) => setTimeout(r, 200));
    }
  }
  return false;
}

async function stopWeb(web) {
  web.proc.kill();
  await web.exited;
}

// The login form renders the server action ids as hidden inputs.
async function loginActionIDs() {
  const html = await (await fetch(`${WEB}/login`)).text();
  return [
    ...new Set(
      [...html.matchAll(/name="(\$ACTION_ID_[0-9a-f]+)"/g)].map((m) => m[1]),
    ),
  ];
}

async function launch(env) {
  await assertPortFree();
  return startWeb(env);
}

// viaRewrite and viaLogin send a request through web and return the
// X-Forwarded-For values atlas recorded for it.
async function viaRewrite(headers) {
  seen.length = 0;
  await fetch(`${WEB}/v1/me`, { headers });
  return seen.filter((s) => s.path === "/v1/me").map((s) => s.xff);
}

// viaUpgrade sends a raw WebSocket upgrade for /v1/me (fetch cannot set
// Upgrade/Connection), which Next proxies to the rewrite target as an
// upgrade.
async function viaUpgrade(headers) {
  seen.length = 0;
  await new Promise((resolve) => {
    const req = http.request(`${WEB}/v1/me`, {
      headers: {
        ...headers,
        Connection: "Upgrade",
        Upgrade: "websocket",
        "Sec-WebSocket-Version": "13",
        "Sec-WebSocket-Key": "dGhlIHNhbXBsZSBub25jZQ==",
      },
    });
    const done = () => {
      req.destroy();
      resolve();
    };
    req.on("response", done);
    req.on("upgrade", done);
    req.on("error", done);
    req.setTimeout(10_000, done);
    req.end();
  });
  return seen.filter((s) => s.path === "/v1/me").map((s) => s.xff);
}

async function viaLogin(headers) {
  seen.length = 0;
  for (const id of await loginActionIDs()) {
    const fd = new FormData();
    fd.set(id, "");
    fd.set("email", "a@example.com");
    fd.set("password", "x");
    fd.set("from", "/dashboard");
    await fetch(`${WEB}/login`, {
      method: "POST",
      body: fd,
      redirect: "manual",
      headers,
    });
  }
  return [
    ...new Set(
      seen.filter((s) => s.path === "/auth/local/login").map((s) => s.xff),
    ),
  ];
}

const forged = {
  "X-Forwarded-For": "198.51.100.66",
  "X-Atlas-Peer-Chain": "198.51.100.67",
};

try {
  // 1. Browsers connect to web directly: a forged header is replaced by
  //    the socket peer on every path.
  let web = await launch({ WEB_TRUSTED_PROXY_CIDRS: "" });
  if (!(await waitReady(web)))
    throw new Error(`web did not start:\n${web.log()}`);
  check("rewrite, direct browser, forged XFF", await viaRewrite(forged), [
    "127.0.0.1",
  ]);
  check("login, direct browser, forged XFF", await viaLogin(forged), [
    "127.0.0.1",
  ]);
  check(
    "websocket upgrade, direct browser, forged XFF",
    await viaUpgrade(forged),
    ["127.0.0.1"],
  );
  await stopWeb(web);

  // 2. Loopback stands in for the operator's proxy in front of web: two
  //    clients behind it reach atlas as two addresses.
  web = await launch({ WEB_TRUSTED_PROXY_CIDRS: "127.0.0.1/32" });
  if (!(await waitReady(web)))
    throw new Error(`web did not start:\n${web.log()}`);
  check(
    "rewrite, client A behind proxy",
    await viaRewrite({ "X-Forwarded-For": "198.51.100.1" }),
    ["198.51.100.1"],
  );
  check(
    "rewrite, client B behind proxy",
    await viaRewrite({ "X-Forwarded-For": "203.0.113.2" }),
    ["203.0.113.2"],
  );
  check(
    "login, client A behind proxy",
    await viaLogin({ "X-Forwarded-For": "198.51.100.1" }),
    ["198.51.100.1"],
  );
  check(
    "login, client B behind proxy",
    await viaLogin({ "X-Forwarded-For": "203.0.113.2" }),
    ["203.0.113.2"],
  );
  check(
    "rewrite, forged prefix behind proxy",
    await viaRewrite({ "X-Forwarded-For": "6.6.6.6, 198.51.100.1" }),
    ["198.51.100.1"],
  );
  await stopWeb(web);

  // 3. A malformed allowlist stops web at boot instead of trusting the
  //    wrong range.
  web = await launch({ WEB_TRUSTED_PROXY_CIDRS: "not-a-cidr" });
  const code = await Promise.race([
    web.exited,
    new Promise((r) => setTimeout(() => r("still running"), 30_000)),
  ]);
  if (code === "still running") await stopWeb(web);
  check(
    "malformed WEB_TRUSTED_PROXY_CIDRS exits non-zero",
    typeof code === "number" && code !== 0,
    true,
  );
} catch (err) {
  console.error(err);
  failures++;
} finally {
  atlas.close();
}

process.exit(failures === 0 ? 0 : 1);
