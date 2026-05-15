// Slice 057 — README-screenshot capture pipeline.
//
// What this script does
// ---------------------
// Renders the four high-leverage views of the merged frontend code and
// captures a PNG of each in both light and dark themes (8 PNGs total),
// then records an ~8-second flow as webm. The output lands under
// `docs/images/` and is committed to the repo. ffmpeg post-processes
// the webm into the final animated GIF.
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
// What this script deliberately is NOT
// -----------------------------------
// * NOT an e2e regression test. The slice-069 e2e suite stays
//   untouched. CI does not run this script.
// * NOT a CI freshness gate. The output is on-demand artifacts
//   (anti-criterion P0-A4).
//
// Running it
// ----------
// Prerequisites: `npm install` in repo root; `npx playwright install
// chromium`; ffmpeg + pngquant on `$PATH`. Then:
//
//     just refresh-screenshots
//
// The recipe wraps this script — see the justfile for the full chain.

import { spawn, type ChildProcess } from "node:child_process";
import { mkdir, readdir, stat, unlink } from "node:fs/promises";
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
const VIDEO_DIR = resolve(REPO_ROOT, "web/test-results/readme-capture-video");
const STUB_PORT = 8787;
const NEXT_PORT = 3300; // avoid colliding with the dev convention :3000
const DEMO_BEARER = "demo-bearer-readme-capture";
// Must match `SESSION_COOKIE` in web/lib/auth.ts. Hard-coded here
// (rather than imported) because esbuild bundles this script in
// isolation from the Next module-resolution context.
const SESSION_COOKIE = "sa_session_token";

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

async function recordFlowVideo(
  browser: Browser,
  baseURL: string,
): Promise<string> {
  await mkdir(VIDEO_DIR, { recursive: true });
  const context = await browser.newContext({
    viewport: { width: 1440, height: 900 },
    recordVideo: { dir: VIDEO_DIR, size: { width: 1440, height: 900 } },
  });
  try {
    await injectSessionCookie(context, baseURL);
    const page = await context.newPage();
    await applyTheme(page, "light");

    // 1. Dashboard.
    await page.goto(`${baseURL}/dashboard`, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    });
    await page.waitForTimeout(1200);

    // 2. Slow scroll down then back up.
    await page.evaluate(() =>
      window.scrollTo({ top: 220, behavior: "smooth" }),
    );
    await page.waitForTimeout(1500);
    await page.evaluate(() => window.scrollTo({ top: 0, behavior: "smooth" }));
    await page.waitForTimeout(1200);

    // 3. Drill into a control.
    await page.goto(`${baseURL}/controls/acme-soc2-ac-1`, {
      waitUntil: "domcontentloaded",
      timeout: 60_000,
    });
    await page.waitForTimeout(1500);

    // 4. Scroll within the control to show UCF coverage.
    await page.evaluate(() =>
      window.scrollTo({ top: 300, behavior: "smooth" }),
    );
    await page.waitForTimeout(1500);

    await page.close();
    const video = page.video();
    const path = await video?.path();
    if (!path) throw new Error("Playwright did not produce a video path");
    return path;
  } finally {
    await context.close();
  }
}

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

async function clearOldVideos(): Promise<void> {
  try {
    const files = await readdir(VIDEO_DIR);
    for (const f of files) {
      if (f.endsWith(".webm")) {
        await unlink(join(VIDEO_DIR, f));
      }
    }
  } catch {
    // dir may not exist yet — fine
  }
}

async function main(): Promise<void> {
  await mkdir(OUT_DIR, { recursive: true });
  await clearOldVideos();

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

    log("recording flow video for GIF source…");
    const videoPath = await recordFlowVideo(browser, baseURL);
    log(`  flow video → ${videoPath}`);

    log("capture complete");
  } finally {
    await browser.close();
    next.kill();
    await stub.close();
  }
}

main().catch((err) => {
  // eslint-disable-next-line no-console
  console.error("[capture] failed:", err);
  process.exit(1);
});
