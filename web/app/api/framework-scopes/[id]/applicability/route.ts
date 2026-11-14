import { NextRequest } from "next/server";

import { forwardJSON, noStore } from "@/lib/api/bff";

// #1794 — GET /v1/framework-scopes/{id}/applicability. Lists every
// requirement of the revision's framework version with its decision, the
// counts and the contradiction flag. The ?decision= and ?contradiction=
// filters pass through verbatim; the platform validates them.

export async function GET(
  req: NextRequest,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  return noStore(
    await forwardJSON(
      `/v1/framework-scopes/${encodeURIComponent(id)}/applicability${
        req.nextUrl.search
      }`,
    ),
  );
}
