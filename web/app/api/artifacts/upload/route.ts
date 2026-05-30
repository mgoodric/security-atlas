import { cookies } from "next/headers";
import { NextRequest, NextResponse } from "next/server";

import { SESSION_COOKIE } from "@/lib/auth";
import { apiBaseURL } from "@/lib/api/base";

// Slice 011 — server-side proxy for POST /v1/artifacts:upload (slice 036).
// The browser posts a multipart form; we forward the form body unchanged
// so we never have to decode the file in JS. Slice 036's handler enforces
// the tenant-prefixed S3 storage; we only thread auth through.
//
// Body cap mirrors slice 011's spec (≤10 MiB attestation artifact). Slice
// 036 still enforces the absolute 100 MiB cap on its side; if our cap
// is hit first the user gets a friendly 413 instead of a slice-036
// surface error.

const MAX_ATTESTATION_ARTIFACT_BYTES = 10 * 1024 * 1024;

export async function POST(req: NextRequest) {
  const jar = await cookies();
  const bearer = jar.get(SESSION_COOKIE)?.value;
  if (!bearer) {
    return NextResponse.json({ error: "unauthenticated" }, { status: 401 });
  }
  const contentLength = Number(req.headers.get("content-length") ?? "0");
  if (contentLength > MAX_ATTESTATION_ARTIFACT_BYTES) {
    return NextResponse.json(
      {
        error: `attestation artifact exceeds ${MAX_ATTESTATION_ARTIFACT_BYTES} bytes`,
      },
      { status: 413 },
    );
  }
  const contentType = req.headers.get("content-type") ?? "";
  if (!contentType.startsWith("multipart/form-data")) {
    return NextResponse.json(
      { error: "content-type must be multipart/form-data" },
      { status: 400 },
    );
  }
  const upstream = await fetch(`${apiBaseURL()}/v1/artifacts:upload`, {
    method: "POST",
    headers: {
      Authorization: `Bearer ${bearer}`,
      "Content-Type": contentType,
    },
    body: req.body,
    // Required for streaming a request body in Node 18+.
    duplex: "half",
  } as RequestInit & { duplex: "half" });
  const raw = await upstream.text();
  return new NextResponse(raw, {
    status: upstream.status,
    headers: { "Content-Type": "application/json" },
  });
}
