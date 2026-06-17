// Slice 499 — BFF for the tenant-admin cloud-LLM routing config.
//
//   GET    /api/admin/llm-routing  -> GET    /v1/admin/llm-routing
//   PUT    /api/admin/llm-routing  -> PUT    /v1/admin/llm-routing
//   DELETE /api/admin/llm-routing  -> DELETE /v1/admin/llm-routing
//
// The platform derives the tenant from the calling credential; the route
// passes no tenant_id. The provider API key is WRITE-ONLY on the platform side
// (encrypted at rest, never returned) — the BFF only proxies bytes, it never
// stores or logs the key. The GET response is the masked config the visible
// routing banner reads (the config-driven banner, not a per-surface hardcode).

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(): Promise<Response> {
  // no-store: the banner must reflect a just-changed provider immediately.
  return noStore(await forwardJSON("/v1/admin/llm-routing"));
}

export async function PUT(req: Request): Promise<Response> {
  let body: unknown = undefined;
  try {
    body = await req.json();
  } catch {
    body = undefined;
  }
  return noStore(
    await forwardJSON("/v1/admin/llm-routing", {
      method: "PUT",
      jsonBody: body,
    }),
  );
}

export async function DELETE(): Promise<Response> {
  return noStore(
    await forwardJSON("/v1/admin/llm-routing", { method: "DELETE" }),
  );
}
