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
// `TEST_BEARER` and sets it as the `ATLAS_JWT_COOKIE` value (slice 206
// migrated the constant's value from `sa_session_token` to `atlas_jwt`
// so it matches the OAuth callback's cookie writer). The Next.js BFF
// (`web/lib/api/bff.ts`) reads that cookie from the jar and forwards
// it as `Authorization: Bearer <value>` to the atlas Go server, where
// the slice 190 jwtmw middleware shape-checks for the `eyJ` JWT
// prefix. So the fixture continues to work as-is — the ATLAS_JWT_COOKIE
// import keeps resolving to whatever value `web/lib/auth.ts` declares.
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
 * issueTestJWT POSTs to /v1/test/issue-jwt with the given tenant + user
 * + roles claim shape. Returns the minted JWT.
 *
 * Throws on any non-200 — the e2e suite cannot proceed without a
 * working credential, so loud failure is the right semantics. A 404
 * almost certainly means `ATLAS_TEST_MODE=1` is unset on the atlas
 * server; the error message surfaces that hypothesis to the operator.
 */
async function issueTestJWT(
  tenant_id: string,
  user_id: string,
  roles: string[],
): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const body = {
    tenant_id,
    user_id,
    roles,
    super_admin: roles.includes("admin"),
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
 * Writes the minted JWTs into `process.env.TEST_BEARER`,
 * `process.env.TEST_ADMIN_BEARER`, and `process.env.TEST_VIEWER_BEARER`
 * so the e2e specs can use them as needed.
 */
// eslint-disable-next-line @typescript-eslint/no-unused-vars
export default async function globalSetup(_config: FullConfig): Promise<void> {
  // Mint a generic test bearer (admin) for backward compatibility
  const token = await issueTestJWT(DEMO_TENANT_ID, DEMO_USER_ID, [
    "admin",
    "grc_engineer",
  ]);
  process.env.TEST_BEARER = token;

  // Mint an explicit admin bearer for admin-bootstrap spec
  const adminToken = await issueTestJWT(
    "00000000-0000-0000-0000-00000000d3a0",
    "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaa001",
    ["admin"],
  );
  process.env.TEST_ADMIN_BEARER = adminToken;

  // Mint a viewer bearer for admin-bootstrap spec
  const viewerToken = await issueTestJWT(
    "00000000-0000-0000-0000-00000000d3a0",
    "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbb01",
    ["viewer", "grc_engineer"],
  );
  process.env.TEST_VIEWER_BEARER = viewerToken;
}
