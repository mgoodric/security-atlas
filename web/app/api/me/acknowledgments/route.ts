// #2465 — BFF proxy for GET /v1/me/acknowledgments: the published
// policies the caller is asked to acknowledge and has not (for this
// version, within the year). The platform scopes the list to the caller;
// a credential that is not a person gets an empty list.
//
// #2668 — `?include=current` is passed through, adding the policies the
// caller has acknowledged (current version, within the year). No other
// query reaches the platform.

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(req: Request): Promise<Response> {
  const include = new URL(req.url).searchParams.get("include");
  const query = include === "current" ? "?include=current" : "";
  return noStore(await forwardJSON(`/v1/me/acknowledgments${query}`));
}
