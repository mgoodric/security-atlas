// #2848 — BFF for PUT /v1/risks/imports/{id}/values: map one category or
// theme value of a staged batch to an existing one, or create it. Only the
// fields the platform reads are forwarded; it validates them and refuses
// a create from a non-approver (403).

import { NextResponse } from "next/server";

import { forwardJSON, rejectNonUUID } from "@/lib/api/bff";

function str(v: unknown): string | undefined {
  return typeof v === "string" ? v : undefined;
}

export async function PUT(
  req: Request,
  ctx: { params: Promise<{ id: string }> },
) {
  const { id } = await ctx.params;
  const bad = rejectNonUUID({ id });
  if (bad) return bad;
  let raw: unknown;
  try {
    raw = await req.json();
  } catch {
    return NextResponse.json({ error: "invalid JSON body" }, { status: 400 });
  }
  if (!raw || typeof raw !== "object" || Array.isArray(raw)) {
    return NextResponse.json(
      { error: "body must be a JSON object" },
      { status: 400 },
    );
  }
  const b = raw as Record<string, unknown>;
  const c = b.create;
  const create =
    c && typeof c === "object" && !Array.isArray(c)
      ? (c as Record<string, unknown>)
      : undefined;
  return forwardJSON(`/v1/risks/imports/${id}/values`, {
    method: "PUT",
    jsonBody: {
      kind: str(b.kind),
      value: str(b.value),
      target: str(b.target),
      create: create
        ? {
            slug: str(create.slug),
            name: str(create.name),
            description: str(create.description),
          }
        : undefined,
    },
  });
}
