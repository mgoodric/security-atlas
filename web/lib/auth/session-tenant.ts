// #2399 — the session's tenant, read from the `atlas_jwt` cookie's
// `atlas:current_tenant_id` claim, for keying browser caches by tenant.
//
// The payload is decoded, not verified: the value only partitions the
// browser's query cache. Every request the cache fills is still a
// bearer-authenticated call the platform verifies and scopes by RLS, so
// a forged claim can only file the forger's own data under a wrong key.
// Anything malformed is unknown, and unknown renders no cached names.

const UUID_RE =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

/** tenantIdFromJwt returns the token's current tenant, or undefined. */
export function tenantIdFromJwt(token: string | undefined): string | undefined {
  if (!token) return undefined;
  const parts = token.split(".");
  if (parts.length !== 3) return undefined;
  let claims: unknown;
  try {
    const b64 = parts[1].replace(/-/g, "+").replace(/_/g, "/");
    claims = JSON.parse(Buffer.from(b64, "base64").toString("utf8"));
  } catch {
    return undefined;
  }
  if (claims === null || typeof claims !== "object") return undefined;
  const id = (claims as Record<string, unknown>)["atlas:current_tenant_id"];
  if (typeof id !== "string" || !UUID_RE.test(id)) return undefined;
  return id.toLowerCase();
}
