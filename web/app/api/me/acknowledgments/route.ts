// #2465 — BFF proxy for GET /v1/me/acknowledgments: the published
// policies the caller is asked to acknowledge and has not (for this
// version, within the year). The platform scopes the list to the caller;
// a credential that is not a person gets an empty list.

import { forwardJSON, noStore } from "@/lib/api/bff";

export async function GET(): Promise<Response> {
  return noStore(await forwardJSON("/v1/me/acknowledgments"));
}
