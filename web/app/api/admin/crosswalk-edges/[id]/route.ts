// Slice 536b — BFF for the crosswalk mapping CONTENT edit.
//
// PATCH /api/admin/crosswalk-edges/{id} -> PATCH /v1/admin/crosswalk-edges/{id}
//
// This route edits a mapping's STRM content. It is NOT an approval path — the
// approve/reject action is slice 483's POST .../tier route (the sibling
// `tier/route.ts`), which the same UI calls. Slice 536a's scope reconciliation
// (§1.2) is explicit that 536b must not grow a second approval workflow.
//
// Body discipline: the BFF forwards a RECONSTRUCTED body, never the client's
// object verbatim. Exactly four fields are passed through, so a crafted request
// cannot smuggle `editor_id`, `source_attribution`, `mapping_tier`, or an edge
// endpoint into the upstream decoder even if a future platform version were to
// start honoring one of those names. The platform validates each field itself
// and remains the authority on all of them — including the admin gate and the
// editor identity, which it takes from the verified JWT and which this layer
// never attempts to supply or replicate.

import { forwardJSON, noStore } from "@/lib/api/bff";

const UUID_RE =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

// The five NIST IR 8477 STRM types (canvas §3.2). Mirrors
// internal/crosswalkedit.RelationshipType.IsValid.
const RELATIONSHIP_TYPES = new Set([
  "equal",
  "subset_of",
  "superset_of",
  "intersects_with",
  "no_relationship",
]);

// Mirrors internal/crosswalkedit.maxRationaleLen. Bounding here keeps an
// oversized blob from crossing the network at all; the platform enforces the
// same cap and is the authority.
const MAX_TEXT = 4096;

export async function PATCH(
  request: Request,
  ctx: { params: Promise<{ id: string }> },
): Promise<Response> {
  const { id } = await ctx.params;
  if (!UUID_RE.test(id)) {
    return Response.json({ error: "invalid edge id" }, { status: 400 });
  }

  let raw: unknown;
  try {
    raw = await request.json();
  } catch {
    return Response.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (typeof raw !== "object" || raw === null || Array.isArray(raw)) {
    return Response.json({ error: "invalid JSON body" }, { status: 400 });
  }
  const body = raw as Record<string, unknown>;

  const relationshipType = body.relationship_type;
  if (
    typeof relationshipType !== "string" ||
    !RELATIONSHIP_TYPES.has(relationshipType)
  ) {
    return Response.json(
      {
        error:
          "relationship_type must be one of equal, subset_of, superset_of, intersects_with, no_relationship",
      },
      { status: 400 },
    );
  }

  const strength = body.strength;
  if (
    typeof strength !== "number" ||
    !Number.isFinite(strength) ||
    strength < 0 ||
    strength > 1
  ) {
    return Response.json(
      { error: "strength must be a number within [0,1]" },
      { status: 400 },
    );
  }

  const rationale = body.rationale ?? "";
  const note = body.note ?? "";
  if (typeof rationale !== "string" || typeof note !== "string") {
    return Response.json(
      { error: "rationale and note must be strings" },
      { status: 400 },
    );
  }
  if (rationale.length > MAX_TEXT || note.length > MAX_TEXT) {
    return Response.json(
      { error: "rationale and note are limited to 4096 bytes" },
      { status: 400 },
    );
  }

  // A mutating response over catalog state the reviewer is actively working —
  // a cached PATCH result would let a stale before/after render as the current
  // mapping (the slice-746 posture for mutating routes).
  return noStore(
    await forwardJSON(`/v1/admin/crosswalk-edges/${encodeURIComponent(id)}`, {
      method: "PATCH",
      jsonBody: {
        relationship_type: relationshipType,
        strength,
        rationale,
        note,
      },
    }),
  );
}
