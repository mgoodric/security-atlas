// Slice 201 — Playwright global-setup hook that mints a JWT for the
// e2e harness via the env-gated POST /v1/test/issue-jwt endpoint and
// writes it into `process.env.TEST_BEARER`. Downstream specs continue
// to read `TEST_BEARER` through `web/e2e/fixtures.ts` unchanged.
//
// Slice 197 retired the slice 034 opaque-bearer middleware. Before
// that retirement, `TEST_BEARER` was a static string ("test-bearer-e2e")
// that authenticated through the legacy `httpAuthMiddlewareWithExemptions`
// mount via the `atlas_test_` carve-out. Slice 197 removed both the
// mount and the carve-out, breaking every authenticated spec.
//
// This module is the runtime analog of slice 197's Go-side
// `Server.IssueTestJWT` helper: where the latter mints a JWT inside the
// test process via the `tokensign.Signer` bound to the in-test
// `*api.Server`, this module mints a JWT against a RUNNING atlas
// server's `s.jwtSigner` — the same signer the production middleware
// is gated on. P0-201-4: there is no parallel test-only signing
// surface.
//
// Playwright calls this module exactly once per test invocation
// (configured via `globalSetup` in `playwright.config.ts`). The minted
// JWT lives for 1h, which outlives every supported test-run length.
// All workers within the same invocation share the same JWT through
// `process.env.TEST_BEARER` because Playwright workers inherit the
// global-setup process env.
//
// Cookie story: the Playwright fixture in `web/e2e/fixtures.ts` reads
// `TEST_BEARER` and sets it as the `sa_session_token` cookie value. The
// Next.js BFF (`web/lib/api/bff.ts`) reads that cookie from the jar and
// forwards it as `Authorization: Bearer <value>` to the atlas Go
// server, where the slice 190 jwtmw middleware shape-checks for the
// `eyJ` JWT prefix. So the fixture continues to work as-is — only the
// VALUE of the cookie has changed (from a static literal to a fresh
// JWT).
//
// Hard rule (P0-201-3): the JWT is never persisted, never logged,
// never baked into an image layer. It lives only in the test process
// env for the duration of the run.

import type { FullConfig } from "@playwright/test";

import { DEMO_TENANT_ID, DEMO_USER_ID } from "./seed";

/**
 * ATLAS_HTTP_URL is the base URL of the running atlas Go server. The
 * Next.js web server lives at PLATFORM_BASE_URL (typically :3000) and
 * proxies to the atlas server (typically :8080). The JWT-issue
 * endpoint is on the atlas server directly because it's a backend
 * concern; the Next.js BFF has no business proxying it.
 *
 * Default `http://localhost:8080` matches the local dev convention +
 * the CI `Frontend · Playwright e2e` job env var.
 */
function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

/**
 * issueTestJWT POSTs to /v1/test/issue-jwt with the demo tenant + user
 * + admin claim shape that the Playwright fixtures need. Returns the
 * minted JWT.
 *
 * Throws on any non-200 — the e2e suite cannot proceed without a
 * working credential, so loud failure is the right semantics. A 404
 * almost certainly means `ATLAS_TEST_MODE=1` is unset on the atlas
 * server; the error message surfaces that hypothesis to the operator.
 */
async function issueTestJWT(): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const body = {
    tenant_id: DEMO_TENANT_ID,
    // Subject = DEMO_USER_ID so PATCH /v1/me + /v1/me/preferences
    // resolve to the real users row inserted by
    // fixtures/e2e/settings.sql (slice 164 — required for AC-3
    // round-trip + AC-8 timezone + the broader settings spec body).
    user_id: DEMO_USER_ID,
    // Admin + grc_engineer roles cover both AC-10 multi-role tail
    // badge (settings) AND every admin-gated route the dashboard
    // suite touches.
    roles: ["admin", "grc_engineer"],
    super_admin: true,
  };

  let res: Response;
  try {
    res = await fetch(url, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
  } catch (err) {
    throw new Error(
      `slice 201 global-setup: failed to reach ${url}: ${
        err instanceof Error ? err.message : String(err)
      }. Is the atlas server running? (set ATLAS_HTTP_URL to override.)`,
    );
  }

  if (res.status === 404) {
    throw new Error(
      `slice 201 global-setup: ${url} returned 404. The atlas server is reachable but the test-mode endpoint is not mounted. Ensure ATLAS_TEST_MODE=1 is set on the atlas server process (NOT on this Playwright runner).`,
    );
  }
  if (!res.ok) {
    const text = await res.text();
    throw new Error(
      `slice 201 global-setup: ${url} returned ${res.status}: ${text}`,
    );
  }
  const parsed = (await res.json()) as { token?: string };
  if (!parsed.token) {
    throw new Error(
      `slice 201 global-setup: ${url} returned 200 but no token field. body = ${JSON.stringify(
        parsed,
      )}`,
    );
  }
  return parsed.token;
}

/**
 * Playwright invokes this default export exactly once per test
 * invocation, BEFORE the webServer step and BEFORE any spec runs.
 * Writes the minted JWT into `process.env.TEST_BEARER` so the
 * existing `web/e2e/fixtures.ts` worker-scoped fixture picks it up
 * unchanged.
 */
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export default async function globalSetup(_config: FullConfig): Promise<void> {
  const token = await issueTestJWT();
  process.env.TEST_BEARER = token;
}
