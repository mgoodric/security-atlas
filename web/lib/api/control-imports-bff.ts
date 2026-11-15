// #1785 — server-side helpers for the control import BFF routes. Kept out
// of `control-imports.ts`, which client pages import, because it returns
// NextResponse.

import { NextResponse } from "next/server";

/** readJSONObject parses the request body as a JSON object, or returns
 *  the 400 to send back. */
export async function readJSONObject(req: {
  json: () => Promise<unknown>;
}): Promise<Record<string, unknown> | NextResponse> {
  let body: unknown;
  try {
    body = await req.json();
  } catch {
    body = undefined;
  }
  if (body === null || typeof body !== "object" || Array.isArray(body)) {
    return NextResponse.json(
      { error: "body must be a JSON object" },
      { status: 400 },
    );
  }
  return body as Record<string, unknown>;
}

/** pickBody keeps only the allow-listed fields that are present. */
export function pickBody(
  body: Record<string, unknown>,
  fields: readonly string[],
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const f of fields) {
    if (Object.hasOwn(body, f)) out[f] = body[f];
  }
  return out;
}
