// Slice 208 — Playwright E2E asserting the Next.js rewrites
// (web/next.config.ts) actually forward /v1/*, /health, /metrics to the
// atlas Go backend.
//
// The vitest unit test (web/next-config.test.ts) verifies the rewrite
// shape in isolation. This spec is the live verification — drive real
// HTTP requests against the Playwright-managed server and confirm the
// atlas backend's responses come back through the rewrite.
//
// AC-4: authenticated /v1/me returns 200 + JSON containing the demo
// user. Proves the rewrite forwards cookies AND atlas's slice-190
// jwtmw middleware verifies the JWT.
//
// AC-5: unauthenticated /v1/anchors returns 401 JSON from atlas (NOT
// 404 from Next.js, NOT 307 to /login). Verifies the rewrite preserves
// atlas as the auth authority.
//
// AC-6: /health returns 200 JSON. Verifies the literal-path rewrite
// works for the public liveness endpoint.
//
// Hard rule (P0-A6 from slice 208 / P0-A9 from slice 069): no
// vendor-prefixed token strings. The authenticated case uses the
// slice-201 globalSetup-minted JWT via the shared authedPage fixture;
// the unauthenticated case uses Playwright's request context with
// extraHTTPHeaders unset so no credential leaks in.

import { expect, test as plainTest } from "@playwright/test";

import { test as authed } from "./fixtures";

authed.describe("Next.js rewrites — /v1/* (slice 208 AC-4)", () => {
  authed(
    "authenticated /v1/me returns 200 JSON via the rewrite",
    async ({ authedPage }) => {
      // page.request shares the BrowserContext cookie jar, so the
      // ATLAS_JWT_COOKIE the fixture set is sent on this request. The
      // rewrite forwards the cookie to atlas; jwtmw shape-checks the
      // JWT; the /v1/me handler returns the user shape.
      const res = await authedPage.request.get("/v1/me", {
        maxRedirects: 0,
      });

      // 200 from atlas (NOT 404 from a Next.js catch-all, NOT 307 from
      // the proxy's redirect-to-login).
      expect(res.status()).toBe(200);
      expect(res.headers()["content-type"] ?? "").toContain("application/json");

      const body = (await res.json()) as {
        user_id?: string;
        tenant_id?: string;
        email?: string;
      };
      // The /v1/me shape exposes the authenticated subject. The exact
      // field names match the slice-190 handler's contract; we assert
      // presence rather than specific values to keep this regression
      // robust against demo-seed renames.
      expect(typeof body.user_id).toBe("string");
      expect(typeof body.tenant_id).toBe("string");
    },
  );
});

plainTest.describe(
  "Next.js rewrites — auth + health (slice 208 AC-5 / AC-6)",
  () => {
    plainTest(
      "unauthenticated /v1/anchors returns 401 JSON from atlas (NOT 404, NOT 307)",
      async ({ request }) => {
        // Plain Playwright `request` context — no fixture-injected
        // cookies. The rewrite forwards the request to atlas with no
        // Authorization header; jwtmw returns 401 + WWW-Authenticate.
        const res = await request.get("/v1/anchors", { maxRedirects: 0 });

        // 401 from atlas — NOT 404 from Next.js catch-all (which would
        // mean the rewrite never fired), NOT 307 to /login (which would
        // mean proxy.ts dropped the slice-206 exemption).
        expect(res.status()).toBe(401);
        expect(res.headers()["content-type"] ?? "").toContain(
          "application/json",
        );

        const body = (await res.json()) as { error?: string };
        // The slice-190 jwtmw middleware emits {"error": "invalid_token"}
        // or {"error": "unauthorized"} depending on the missing-vs-invalid
        // discriminator. Both shapes are acceptable here.
        expect(typeof body.error).toBe("string");
        expect(body.error?.length ?? 0).toBeGreaterThan(0);
      },
    );

    plainTest(
      "/health returns 200 JSON via the literal-path rewrite",
      async ({ request }) => {
        // /health is public by slice-052 contract — no auth required.
        const res = await request.get("/health", { maxRedirects: 0 });

        expect(res.status()).toBe(200);
        expect(res.headers()["content-type"] ?? "").toContain(
          "application/json",
        );

        // The atlas /health endpoint emits a JSON object with at minimum a
        // `status` field. We assert presence + the well-known `ok` value
        // rather than the full shape (which has grown organically across
        // slices) so this regression doesn't fight unrelated /health
        // additions.
        const body = (await res.json()) as { status?: string };
        expect(body.status).toBe("ok");
      },
    );
  },
);
