// Slice 536b — BFF for the crosswalk mapping TIER transition.
//
// POST /api/admin/crosswalk-edges/{id}/tier
//   -> POST /v1/admin/crosswalk-edges/{id}/tier
//
// This is the approve/reject action, and the upstream is SLICE 483's route —
// the one already-shipped review lifecycle. Slice 536b deliberately ships no
// approval endpoint of its own (536a §1.2): a second workflow alongside 483's
// tier state machine is the anti-criterion the slice forbids, so this BFF is a
// thin proxy onto 483 rather than a new path.
//
// Nothing auto-approves. `verified` is reachable only through this transition,
// only from `under_review`, and only because a human clicked — the platform
// state machine (internal/crosswalktier) refuses every other move 422 and
// writes an append-only transition row for the ones it accepts.
//
// Body discipline, same rule as the content-edit route: a reconstructed
// `{tier, note}` is forwarded, never the client object. `reviewer_id` in
// particular can never ride in from the browser — the platform takes the
// reviewer identity from the verified admin JWT, and reconstructing the body
// here means the field cannot reach the upstream decoder at all.

import { forwardJSON, noStore } from "@/lib/api/bff";

const UUID_RE =
  /^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$/;

// The four slice-483 trust tiers. Shape-checked here so arbitrary text never
// reaches the platform's enum parser; the platform validates the tier AND the
// legality of the transition, and is the authority on both.
const TIERS = new Set(["draft", "under_review", "verified", "rejected"]);

const MAX_TEXT = 4096;

export async function POST(
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

  const tier = body.tier;
  if (typeof tier !== "string" || !TIERS.has(tier)) {
    return Response.json(
      {
        error: "tier must be one of draft, under_review, verified, rejected",
      },
      { status: 400 },
    );
  }

  const note = body.note ?? "";
  if (typeof note !== "string") {
    return Response.json({ error: "note must be a string" }, { status: 400 });
  }
  if (note.length > MAX_TEXT) {
    return Response.json(
      { error: "note is limited to 4096 bytes" },
      { status: 400 },
    );
  }

  return noStore(
    await forwardJSON(
      `/v1/admin/crosswalk-edges/${encodeURIComponent(id)}/tier`,
      { method: "POST", jsonBody: { tier, note } },
    ),
  );
}
