// #2399 — reads the session's tenant from the request's `atlas_jwt`
// cookie (see ./session-tenant.ts). Server components only.

import { cookies } from "next/headers";

import { ATLAS_JWT_COOKIE } from "@/lib/auth";

import { tenantIdFromJwt } from "./session-tenant";

export async function readSessionTenantId(): Promise<string | undefined> {
  const jar = await cookies();
  return tenantIdFromJwt(jar.get(ATLAS_JWT_COOKIE)?.value);
}
