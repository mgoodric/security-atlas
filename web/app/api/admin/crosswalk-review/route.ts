// Slice 536b — BFF for the crosswalk review queue.
//
// Forwards `GET /api/admin/crosswalk-review?...` to the platform
// `GET /v1/admin/crosswalk-review?...`. The bearer cookie is read server-side
// and never reaches the browser; the `atlas_session` cookie is never forwarded
// (the slice-110 P0-A2 narrow-scope rule).
//
// Input discipline: the BFF forwards a RECONSTRUCTED query string, not
// `url.search` verbatim. Only the five parameters the upstream route defines
// are passed, each shape-checked here, so arbitrary client text never reaches
// the platform's query parser. This is a defence-in-depth measure — the
// platform validates every one of these itself and is the authority on all of
// them (including the admin gate, which the BFF never attempts to replicate).

import { forwardJSON } from "@/lib/api/bff";

const UUID_RE =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;
const TIERS = new Set(["draft", "under_review", "verified", "rejected"]);

function boundedInt(raw: string | null, max: number): string | undefined {
  if (raw === null) return undefined;
  if (!/^\d+$/.test(raw)) return undefined;
  const n = Number(raw);
  if (!Number.isSafeInteger(n) || n < 0 || n > max) return undefined;
  return String(n);
}

export async function GET(request: Request): Promise<Response> {
  const url = new URL(request.url);
  const frameworkVersionId = url.searchParams.get("framework_version_id");
  if (!frameworkVersionId || !UUID_RE.test(frameworkVersionId)) {
    return Response.json(
      { error: "framework_version_id must be a uuid" },
      { status: 400 },
    );
  }

  const q = new URLSearchParams({ framework_version_id: frameworkVersionId });

  const tier = url.searchParams.get("tier");
  if (tier && TIERS.has(tier)) q.set("tier", tier);

  if (url.searchParams.get("conflicts_only") === "true") {
    q.set("conflicts_only", "true");
  }

  // The upstream clamps these itself; the bounds here only keep a malformed
  // value from being forwarded at all.
  const limit = boundedInt(url.searchParams.get("limit"), 1000);
  if (limit !== undefined) q.set("limit", limit);
  const offset = boundedInt(url.searchParams.get("offset"), 1_000_000_000);
  if (offset !== undefined) q.set("offset", offset);

  return forwardJSON(`/v1/admin/crosswalk-review?${q.toString()}`);
}
