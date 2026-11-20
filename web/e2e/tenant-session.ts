// #2755 — sign a page in to a tenant of the spec's own.
//
// A spec that asserts on row counts, empty states or filter results cannot
// share the demo tenant: other specs add rows to it, and fixtures are
// additive across a run. Such a spec seeds a tenant of its own (a
// `fixtures/e2e/<spec>.sql` with a dedicated UUID) and signs in to it here.
// The JWT comes from the env-gated `/v1/test/issue-jwt` endpoint that
// global-setup.ts uses for the demo session.

import type { Page } from "@playwright/test";

import { ATLAS_JWT_COOKIE } from "../lib/auth";

export type TenantSession = {
  tenantId: string;
  /** Omit to let the endpoint pick its default subject. */
  userId?: string;
  roles: string[];
  superAdmin?: boolean;
};

function atlasBaseURL(): string {
  return process.env.ATLAS_HTTP_URL ?? "http://localhost:8080";
}

/** Mints a JWT for `session` through `/v1/test/issue-jwt`. */
export async function mintTenantJWT(session: TenantSession): Promise<string> {
  const url = `${atlasBaseURL()}/v1/test/issue-jwt`;
  const res = await fetch(url, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      tenant_id: session.tenantId,
      ...(session.userId ? { user_id: session.userId } : {}),
      roles: session.roles,
      super_admin: session.superAdmin ?? false,
    }),
  });
  if (!res.ok) {
    throw new Error(`${url} returned ${res.status}: ${await res.text()}`);
  }
  const { token } = (await res.json()) as { token?: string };
  if (!token) throw new Error(`${url} returned 200 but no token field`);
  return token;
}

/** Replaces the page's cookies with a session for `session`. */
export async function signInToTenant(
  page: Page,
  session: TenantSession,
): Promise<void> {
  const web = new URL(process.env.PLATFORM_BASE_URL ?? "http://localhost:3000");
  const token = await mintTenantJWT(session);
  await page.context().clearCookies();
  await page.context().addCookies([
    {
      name: ATLAS_JWT_COOKIE,
      value: token,
      domain: web.hostname,
      path: "/",
      httpOnly: true,
      secure: web.protocol === "https:",
      sameSite: "Lax",
    },
  ]);
}
