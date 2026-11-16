// #1778 — SCF anchors for the remap review page. Read-only, global
// catalog only; every upstream call carries the caller's own bearer.
//
//   ?release=R&codes=A,B   look up up to 10 codes in release R
//                          (GET /v1/anchors/{code}?scf_release=R each);
//                          a code the release lacks maps to null.
//   ?release=R&q=text      search release R's anchors by code or title
//                          (pages GET /v1/anchors?framework_version_id=…),
//                          returning the first 25 matches.
//
// Codes and release labels are validated before they reach an upstream
// path or query.

import { cookies } from "next/headers";
import { NextResponse } from "next/server";

import { apiFetch } from "@/lib/api/_shared";
import { APIError } from "@/lib/api/base";
import type {
  AnchorLookupResponse,
  AnchorSearchResponse,
  SCFAnchor,
} from "@/lib/api/remap";
import { ATLAS_JWT_COOKIE } from "@/lib/auth";
import {
  isReleaseLabel,
  isSCFCode,
  matchesAnchorQuery,
} from "@/lib/remap-review";

const MAX_CODES = 10;
const MAX_RESULTS = 25;
const PAGE = 500;
// SCF carries ~1,400 anchors a release; 10 pages is a generous ceiling.
const MAX_PAGES = 10;

function bad(msg: string, status = 400) {
  return NextResponse.json({ error: msg }, { status });
}

// resolveRelease returns the SCF framework_version id for a release label,
// or throws a 404 APIError, so an unknown release never passes for "this
// code is not in the release".
async function resolveRelease(bearer: string, release: string) {
  const vres = await apiFetch(`/v1/frameworks/scf/versions`, bearer);
  const { versions } = (await vres.json()) as {
    versions: { id: string; version: string }[];
  };
  const v = versions.find((x) => x.version === release);
  if (!v) throw new APIError(404, "unknown SCF release");
  return v.id;
}

async function lookup(
  bearer: string,
  release: string,
  codes: string[],
): Promise<AnchorLookupResponse> {
  await resolveRelease(bearer, release);
  const entries = await Promise.all(
    codes.map(async (code): Promise<[string, SCFAnchor | null]> => {
      try {
        const res = await apiFetch(
          `/v1/anchors/${encodeURIComponent(
            code,
          )}?scf_release=${encodeURIComponent(release)}`,
          bearer,
        );
        const j = (await res.json()) as { anchor: SCFAnchor };
        return [code, j.anchor];
      } catch (err) {
        if ((err as { status?: number }).status === 404) return [code, null];
        throw err;
      }
    }),
  );
  return { scf_release: release, anchors: Object.fromEntries(entries) };
}

async function search(
  bearer: string,
  release: string,
  q: string,
): Promise<AnchorSearchResponse> {
  const versionID = await resolveRelease(bearer, release);
  const matches: SCFAnchor[] = [];
  let truncated = false;
  for (let page = 0; page < MAX_PAGES; page++) {
    const res = await apiFetch(
      `/v1/anchors?framework_version_id=${encodeURIComponent(
        versionID,
      )}&limit=${PAGE}&offset=${page * PAGE}`,
      bearer,
    );
    const { anchors } = (await res.json()) as { anchors: SCFAnchor[] };
    for (const a of anchors) {
      if (!matchesAnchorQuery(a, q)) continue;
      if (matches.length === MAX_RESULTS) {
        truncated = true;
        break;
      }
      matches.push(a);
    }
    if (truncated || anchors.length < PAGE) break;
  }
  return { scf_release: release, anchors: matches, truncated };
}

export async function GET(req: Request) {
  const jar = await cookies();
  const bearer = jar.get(ATLAS_JWT_COOKIE)?.value;
  if (!bearer) return bad("unauthenticated", 401);

  const sp = new URL(req.url).searchParams;
  const release = sp.get("release") ?? "";
  if (!isReleaseLabel(release))
    return bad("release must be an SCF release label");

  const codesParam = sp.get("codes");
  const q = (sp.get("q") ?? "").trim();
  try {
    if (codesParam !== null) {
      const codes = [...new Set(codesParam.split(",").filter(Boolean))];
      if (codes.length === 0 || codes.length > MAX_CODES) {
        return bad(`codes must name 1 to ${MAX_CODES} anchors`);
      }
      if (!codes.every(isSCFCode)) return bad("codes must be SCF codes");
      return NextResponse.json(await lookup(bearer, release, codes));
    }
    if (q.length < 2 || q.length > 64) {
      return bad("q must be 2 to 64 characters");
    }
    return NextResponse.json(await search(bearer, release, q));
  } catch (err) {
    // Only a platform status reaches the client; anything else (a network
    // failure, a malformed body) is a generic 502, never its raw message.
    if (err instanceof APIError) return bad(err.message, err.status);
    return bad("upstream error", 502);
  }
}
