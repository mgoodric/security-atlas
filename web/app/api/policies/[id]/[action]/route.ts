// #2116 — /policies/[id] BFF for the workflow transitions.
//
// One proxy for the three steps after draft: submit and approve
// (PATCH upstream) and publish (POST upstream, with the version, effective
// date and next review). Any other segment is refused here with 404 and
// never reaches the platform. The platform owns every other refusal: 403
// for the approver role or the approval rule (the submitter cannot approve
// unless self-approval is on), 409 for a wrong state or a publish with no
// linked control.
//
// #2465 — acknowledge (POST upstream) records the caller's own
// acknowledgment of a published policy. The platform admits any member
// whose role is in the policy's audience, and an ack is always for the
// caller. The body's optional `policy_version_id` pins the version the
// caller was shown; a mismatch is the platform's 409 (#2668).

import { NextResponse } from "next/server";

import { forwardJSON, noStore, rejectNonUUID } from "@/lib/api/bff";

const UPSTREAM_METHOD: Record<string, "PATCH" | "POST"> = {
  submit: "PATCH",
  approve: "PATCH",
  publish: "POST",
  acknowledge: "POST",
};

export async function POST(
  req: Request,
  ctx: { params: Promise<{ id: string; action: string }> },
) {
  const { id, action } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  const method = Object.hasOwn(UPSTREAM_METHOD, action)
    ? UPSTREAM_METHOD[action]
    : undefined;
  if (!method) {
    return NextResponse.json({ error: "unknown transition" }, { status: 404 });
  }
  let body: unknown;
  const text = await req.text();
  if (text) {
    try {
      body = JSON.parse(text);
    } catch {
      return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
    }
  }
  return noStore(
    await forwardJSON(`/v1/policies/${encodeURIComponent(id)}/${action}`, {
      method,
      jsonBody: body,
    }),
  );
}
