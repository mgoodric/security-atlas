// Slice 057 (authored) + Slice 132 (hardened) — README-screenshot
// capture pipeline.
//
// What this script does
// ---------------------
// Renders the four high-leverage views of the merged frontend code and
// captures a PNG of each in both light and dark themes (8 PNGs total).
// The output lands under `docs/images/` and is committed to the repo.
//
// Slice 132 removed the flow GIF recording (slice 132 P0 scope: static
// PNG only — animated GIFs / video are out of scope for the README to
// keep the page diff-friendly, a11y-friendly, and under the 2 MB total
// image budget). The `recordFlowVideo()` helper was deleted along with
// the `webm → ffmpeg → GIF` post-step in the justfile.
//
// Why a standalone Node script (D5 revised)
// -----------------------------------------
// The initial design used a Playwright test spec under
// `web/scripts/capture-readme-screenshots.spec.ts` and reused
// `web/playwright.config.ts`. The reuse was clean on paper but ran into
// two friction points in execution:
//
//   1. `web/playwright.config.ts` pins `testDir: "./e2e"`. A spec under
//      `web/scripts/` is silently ignored unless we pass `--test-dir`,
//      and changing the config would couple this capture pipeline to
//      slice 069's e2e regression suite.
//   2. The config's `webServer.command` is `npm start`, which fails
//      under `output: standalone` (the slice-037 Docker shape). Working
//      around that means either changing the config (couples to 069) or
//      bypassing webServer (defeats the reuse argument).
//
// Switching to a standalone Node script that drives the `playwright`
// core API directly resolves both — no config coupling, no
// `webServer` orchestration, capture pipeline is just code. The
// `@playwright/test` devDependency is still the only Playwright
// install in the tree (P0-A7 honored).
//
// Slice 132 safety gate (AC-2)
// ----------------------------
// The script refuses to run unless BOTH of the following are true:
//
//   1. The environment variable `ATLAS_DEMO_SEED=1` is set. This is the
//      operator-typed safety affirmation that the upstream platform the
//      script is about to capture is a sanitized demo seed, NOT a real
//      tenant. The variable is deliberately a hard string-match — not
//      `${ATLAS_DEMO_SEED:-0}` parsing — so a typo (`true`, `yes`,
//      `ATLAS_DEMOSEED`) trips the gate.
//   2. The upstream HTTP target — read from `ATLAS_HTTP_URL` if set,
//      otherwise the stub-server localhost default — has a hostname
//      that resolves to a loopback or RFC1918 private-range address.
//      This catches the "I forgot to switch off prod" footgun: the
//      script will REFUSE if the hostname is anything that looks like
//      it could be a customer endpoint.
//
// The gate is information-disclosure mitigation. Slice 132's threat
// model treats the README as a permanent public PII leak vector — every
// captured PNG is public forever the moment the PR merges. The gate is
// the cheapest defense-in-depth against a future capture-run operator
// accidentally pointing this script at a live tenant.
//
// What this script deliberately is NOT
// -----------------------------------
// * NOT an e2e regression test. The slice-069 e2e suite stays
//   untouched. CI does not run this script.
// * NOT a CI freshness gate. The output is on-demand artifacts
//   (anti-criterion P0-A4 from slice 057; preserved by slice 132).
//
// Running it
// ----------
// Prerequisites: `npm install` in repo root; `npx playwright install
// chromium`; pngquant on `$PATH`. Then:
//
//     ATLAS_DEMO_SEED=1 just refresh-screenshots
//
// The recipe wraps this script and exports `ATLAS_DEMO_SEED=1` for the
// operator — see the justfile for the full chain.
//
// Demo seed vs stub-server fixtures (slice 132 D1)
// ------------------------------------------------
// Slice 132 AC-3 says the script should invoke `web/e2e/seed.ts`'s
// demo-seed path "or a documented equivalent". The slice-057 stub server
// at `web/scripts/stub-platform-server.ts` IS the documented equivalent:
// it serves neutral, hermetic JSON from `fixtures/readme-demo/*.json`,
// no docker-compose dependency, deterministic byte-for-byte across runs.
// The stub fixtures encode the same demo-tenant shape the slice-082 e2e
// seed produces — neutral org names, synthetic emails, no real customer
// data. This satisfies P0-A1/A2/A3 at the design level (the fixtures
// THEMSELVES contain no real data), independent of the safety gate
// above (which catches the live-tenant case if a future maintainer
// rewires the script onto the real platform).

import { spawn, type ChildProcess } from "node:child_process";
import { mkdir } from "node:fs/promises";
import { join, resolve } from "node:path";

import {
  chromium,
  type Browser,
  type BrowserContext,
  type Page,
} from "playwright";

import { startStubServer } from "./stub-platform-server";

const REPO_ROOT = resolve(__dirname, "../..");
const OUT_DIR = resolve(REPO_ROOT, "docs/images");
const STUB_PORT = 8787;
const NEXT_PORT = 3300; // avoid colliding with the dev convention :3000
const DEMO_BEARER = "demo-bearer-readme-capture";
// Must match `SESSION_COOKIE` in web/lib/auth.ts. Hard-coded here
// (rather than imported) because esbuild bundles this script in
// isolation from the Next module-resolution context. Slice 206
// migrated the value from `sa_session_token` to `atlas_jwt`.
const SESSION_COOKIE = "atlas_jwt";

const CAPTURE_TARGETS = [
  { name: "hero-dashboard", path: "/dashboard" },
  { name: "control-detail", path: "/controls/acme-soc2-ac-1" },
  { name: "audit-workspace", path: "/audit/acme-soc2-ac-1" },
  {
    name: "board-pack-preview",
    path: "/board-packs/00000000-0000-0000-0000-000000000501",
  },
] as const;

function log(msg: string): void {
  // eslint-disable-next-line no-console
  console.log(`[capture] ${msg}`);
}

// Slice 132 AC-2 — the safety gate. Asserts the operator has
// affirmatively flagged the capture target as a demo seed AND the
// target hostname is a loopback / RFC1918 private-range address. Throws
// with an actionable diagnostic on any violation.
//
// Exported for unit testing — `web/scripts/capture-readme-screenshots.test.ts`
// covers the seven branches (env-missing, env-typo, env-correct,
// localhost-permitted, 127.0.0.1-permitted, 10.x-permitted,
// public-IP-rejected, hostname-cannot-be-resolved-rejected).
//
// The gate is intentionally STRICT — failing closed is the right
// information-disclosure posture. A capture run that aborts is cheap
// to retry; a screenshot of a real tenant in the public README is
// permanent and unrecoverable.
export function assertCaptureSafe(
  // Accepts a partial environment: the gate only reads `ATLAS_DEMO_SEED`
  // and `ATLAS_HTTP_URL`, so callers (and tests) may pass a subset. The
  // `process.env` default (a full `NodeJS.ProcessEnv`) remains assignable.
  env: Partial<NodeJS.ProcessEnv> = process.env,
): void {
  // Gate 1 — `ATLAS_DEMO_SEED=1` operator affirmation. The literal "1"
  // is intentional. We do NOT accept "true" / "yes" / "on" because
  // ambiguity creates false-positive bypass paths via a typo.
  if (env.ATLAS_DEMO_SEED !== "1") {
    throw new Error(
      "ATLAS_DEMO_SEED=1 is required. This is slice 132's AC-2 safety " +
        "gate: every captured PNG goes into the public README, and the " +
        "capture target MUST be a sanitized demo seed (or the slice-057 " +
        "stub-server which uses hermetic fixtures). Set " +
        "`ATLAS_DEMO_SEED=1` in your environment before running this " +
        "script (the `just refresh-screenshots` recipe sets it for you).",
    );
  }

  // Gate 2 — host must be loopback / private-range. Read from
  // ATLAS_HTTP_URL if set (the script's upstream platform target);
  // default to "localhost" (the stub-server case).
  const httpURL = env.ATLAS_HTTP_URL ?? `http://localhost:${STUB_PORT}`;
  const host = extractHost(httpURL);
  if (!isLoopbackOrPrivate(host)) {
    throw new Error(
      `Capture target ${httpURL} is not a loopback or RFC1918 private ` +
        `address (host="${host}"). Slice 132 AC-2 refuses to capture ` +
        `against a remote tenant. If you need to capture against a ` +
        `non-localhost target, port-forward it to localhost first.`,
    );
  }
}

function extractHost(httpURL: string): string {
  try {
    return new URL(httpURL).hostname;
  } catch {
    // If the value is not a parseable URL, treat the whole string as
    // the host. Better-safe-than-sorry: this will almost certainly fail
    // the loopback / private-range check below, which is the right
    // outcome.
    return httpURL;
  }
}

// Returns true iff `host` is a loopback name / IPv4 / IPv6 OR an
// RFC1918 private-range IPv4 OR a unique-local IPv6 (fc00::/7).
// Returns false for public IPs, public DNS names, and unresolvable
// strings. Intentionally conservative — false-negative (refuse safe)
// beats false-positive (allow unsafe).
export function isLoopbackOrPrivate(host: string): boolean {
  if (host === "localhost") return true;
  if (host === "127.0.0.1" || host === "::1") return true;
  if (host === "0.0.0.0") return true;

  // IPv4 RFC1918 + carrier NAT
  const v4 = host.match(/^(\d{1,3})\.(\d{1,3})\.(\d{1,3})\.(\d{1,3})$/);
  if (v4) {
    const [a, b] = [parseInt(v4[1], 10), parseInt(v4[2], 10)];
    if (a === 10) return true; // 10.0.0.0/8
    if (a === 172 && b >= 16 && b <= 31) return true; // 172.16.0.0/12
    if (a === 192 && b === 168) return true; // 192.168.0.0/16
    if (a === 100 && b >= 64 && b <= 127) return true; // 100.64.0.0/10 (CGN)
    if (a === 127) return true; // 127.0.0.0/8 loopback
    return false;
  }

  // IPv6 unique-local (fc00::/7) — covers fc00:: and fd00:: prefixes
  if (/^f[cd][0-9a-f]{2}:/i.test(host)) return true;

  // Anything else (public DNS name, public IP, garbage): refuse.
  return false;
}

async function waitForPort(port: number, timeoutMs: number): Promise<void> {
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    try {
      const res = await fetch(`http://localhost:${port}/`, {
        redirect: "manual",
      });
      // Any HTTP response (even a 307 to /login) means the server is up.
      if (res.status > 0) return;
    } catch {
      // not up yet
    }
    await new Promise((r) => setTimeout(r, 300));
  }
  throw new Error(
    `timed out waiting for http://localhost:${port}/ after ${timeoutMs}ms`,
  );
}

async function applyTheme(page: Page, theme: "light" | "dark"): Promise<void> {
  if (theme === "dark") {
    // Tailwind config uses class-strategy dark mode
    // (`@custom-variant dark (&:is(.dark *))` in globals.css; `.dark`
    // is a top-level rule overriding the CSS variables). React
    // hydration rewrites the root <html> className on mount, so
    // addInitScript alone gets clobbered. We do both: addInitScript
    // sets the class for pre-hydration paint, and a post-navigation
    // evaluate re-applies it after hydration. emulateMedia ensures
    // native form controls (scrollbar, focus ring) follow the scheme.
    await page.addInitScript(() => {
      document.documentElement.classList.add("dark");
      document.documentElement.style.colorScheme = "dark";
    });
    await page.emulateMedia({ colorScheme: "dark" });
  } else {
    await page.emulateMedia({ colorScheme: "light" });
  }
}

async function reapplyThemeAfterHydration(
  page: Page,
  theme: "light" | "dark",
): Promise<void> {
  if (theme !== "dark") return;
  await page.evaluate(() => {
    document.documentElement.classList.add("dark");
    document.documentElement.style.colorScheme = "dark";
  });
}

async function injectSessionCookie(
  context: BrowserContext,
  baseURL: string,
): Promise<void> {
  const url = new URL(baseURL);
  await context.addCookies([
    {
      name: SESSION_COOKIE,
      value: DEMO_BEARER,
      domain: url.hostname,
      path: "/",
      httpOnly: true,
      secure: url.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}

async function captureOne(
  browser: Browser,
  baseURL: string,
  target: { name: string; path: string },
  theme: "light" | "dark",
): Promise<void> {
  const suffix = theme === "dark" ? "-dark" : "";
  const out = join(OUT_DIR, `${target.name}${suffix}.png`);
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
  });
  try {
    await injectSessionCookie(context, baseURL);
    const page = await context.newPage();
    await applyTheme(page, theme);
    log(`  ${target.name}${suffix}.png ← ${target.path}`);
    await page.goto(`${baseURL}${target.path}`, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    });
    // Re-apply dark theme after React hydration may have rewritten the
    // root className. Idempotent for light theme.
    await reapplyThemeAfterHydration(page, theme);
    // Wait for the BFF round-trips to land + TanStack Query to populate
    // + Tailwind transitions to settle. `networkidle` is unreliable in
    // Next dev mode because HMR keeps a long-poll connection open; an
    // explicit timeout is more predictable. 2.5s is empirically enough
    // for dashboard panels (six TanStack queries fan out in parallel)
    // and avoids capturing spinners or skeletons mid-fade (P0-A5).
    await page.waitForTimeout(2500);
    await page.screenshot({
      path: out,
      fullPage: false,
      animations: "disabled",
    });
  } finally {
    await context.close();
  }
}

// Slice 132 removed `recordFlowVideo()` — the README no longer embeds
// the animated GIF (slice 132 explicitly scopes to static PNG only;
// the previous 1.8 MB `flow-create-control.gif` blew through slice
// 132's 2 MB total-README image budget single-handedly).

function spawnNextServer(): ChildProcess {
  // Launch the standalone production server. The slice-037 build
  // emits `web/.next/standalone/web/server.js`; we point PORT and
  // HOSTNAME at it, plus ATLAS_HTTP_URL at our stub. Production mode
  // is mandatory here — dev mode renders the Next 16 error indicator
  // (the "N · 1 Issue" badge in the corner) which is an ephemeral
  // overlay (P0-A5 violation).
  //
  // The recipe ensures `npm run build` ran before this script.
  const env = {
    ...process.env,
    PORT: String(NEXT_PORT),
    HOSTNAME: "127.0.0.1",
    ATLAS_HTTP_URL: `http://localhost:${STUB_PORT}`,
    NEXT_TELEMETRY_DISABLED: "1",
  };
  return spawn(
    "node",
    [resolve(__dirname, "..", ".next/standalone/web/server.js")],
    {
      cwd: resolve(__dirname, ".."),
      env,
      stdio: ["ignore", "pipe", "pipe"],
    },
  );
}

async function main(): Promise<void> {
  // Slice 132 AC-2 — assert safety gate BEFORE any side effect.
  // Throws with an actionable diagnostic if ATLAS_DEMO_SEED!=1 or the
  // upstream HTTP target is not a loopback / private-range host.
  assertCaptureSafe();

  await mkdir(OUT_DIR, { recursive: true });

  log(`starting stub platform server on :${STUB_PORT}`);
  const stub = startStubServer(STUB_PORT);

  log(`starting Next dev server on :${NEXT_PORT}`);
  const next = spawnNextServer();
  // Echo Next stderr; helpful when debugging a failed capture run.
  next.stderr?.on("data", (b: Buffer) => {
    const s = b.toString();
    if (s.includes("error") || s.includes("Error")) {
      process.stderr.write(`[next] ${s}`);
    }
  });

  const baseURL = `http://localhost:${NEXT_PORT}`;
  const browser = await chromium.launch({ headless: true });

  try {
    await waitForPort(NEXT_PORT, 60_000);
    log("Next dev server is up");

    log("capturing 8 PNGs (4 views × 2 themes)…");
    for (const target of CAPTURE_TARGETS) {
      for (const theme of ["light", "dark"] as const) {
        await captureOne(browser, baseURL, target, theme);
      }
    }

    log("capture complete");
  } finally {
    await browser.close();
    next.kill();
    await stub.close();
  }
}

// Only run main() when this file is the script entry point. Importing
// the module (e.g. from a vitest test of `assertCaptureSafe`) must NOT
// kick off the capture. esbuild's bundled output preserves
// `require.main === module` semantics in CJS — the script invocation
// flows through that branch; the test import does not.
if (require.main === module) {
  main().catch((err) => {
    // eslint-disable-next-line no-console
    console.error("[capture] failed:", err);
    process.exit(1);
  });
}
